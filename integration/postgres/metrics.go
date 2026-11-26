package postgres

import (
	"context"

	"github.com/mountaya/helix.go/integration"

	"github.com/jackc/pgx/v5/pgxpool"
	otelmetric "go.opentelemetry.io/otel/metric"
)

// poolMetrics observes the connection pool.
//
// A pool is the one part of a database integration that a trace describes badly:
// the span for an acquire says this request waited, but nothing says the pool has
// been saturated for ten minutes. The levels are gauges rather than counters
// because what matters is the level, and everything is observed on collection
// rather than recorded on use, so an idle service still reports the shape of its
// pool.
//
// The connections of a Listener are not the pool's, and are not counted here.
type poolMetrics struct {
	registration otelmetric.Registration
}

// newPoolMetrics registers the observable instruments for pool. A provider that
// rejects an instrument hands back a working no-op, so failures are dropped
// rather than turned into a connection failure over telemetry.
func newPoolMetrics(inst *integration.Instrument, pool *pgxpool.Pool) *poolMetrics {
	meter := inst.Meter()

	total, err := meter.Int64ObservableGauge(inst.Identifier()+".pool.connections",
		otelmetric.WithUnit("{connection}"),
		otelmetric.WithDescription("Connections currently held by the pool, by state."),
	)
	if err != nil {
		return &poolMetrics{}
	}

	limit, err := meter.Int64ObservableGauge(inst.Identifier()+".pool.connections.max",
		otelmetric.WithUnit("{connection}"),
		otelmetric.WithDescription("Maximum number of connections the pool may hold."),
	)
	if err != nil {
		return &poolMetrics{}
	}

	waits, err := meter.Int64ObservableCounter(inst.Identifier()+".pool.acquire.waits",
		otelmetric.WithUnit("{acquire}"),
		otelmetric.WithDescription("Acquires that had to wait for a connection to become available."),
	)
	if err != nil {
		return &poolMetrics{}
	}

	waitDuration, err := meter.Float64ObservableCounter(inst.Identifier()+".pool.acquire.wait.duration",
		otelmetric.WithUnit("s"),
		otelmetric.WithDescription("Time spent waiting for a connection to become available."),
	)
	if err != nil {
		return &poolMetrics{}
	}

	// The state attribute is what makes one gauge answer "how saturated is this
	// pool": idle, acquired and constructing sum to the total, and the total
	// against the maximum is the headroom left. Constructing is its own state
	// because a connection being opened is neither available nor doing work, and
	// a pool stuck opening connections — a slow handshake, an unreachable server —
	// shows up there and nowhere else.
	inState := func(state string) otelmetric.MeasurementOption {
		return otelmetric.WithAttributes(append(inst.AppendIdentity(nil), attrKeyPoolState.String(state))...)
	}

	idle := inState("idle")
	acquired := inState("acquired")
	constructing := inState("constructing")
	identity := otelmetric.WithAttributes(inst.AppendIdentity(nil)...)

	registration, err := meter.RegisterCallback(
		func(_ context.Context, observer otelmetric.Observer) error {
			stat := pool.Stat()

			observer.ObserveInt64(total, int64(stat.IdleConns()), idle)
			observer.ObserveInt64(total, int64(stat.AcquiredConns()), acquired)
			observer.ObserveInt64(total, int64(stat.ConstructingConns()), constructing)
			observer.ObserveInt64(limit, int64(stat.MaxConns()), identity)

			// Both describe the acquires that found the pool empty and had to wait
			// for a connection to be released or opened: how many, and for how
			// long in total. An acquire served from an idle connection waited for
			// nothing and is in neither.
			observer.ObserveInt64(waits, stat.EmptyAcquireCount(), identity)
			observer.ObserveFloat64(waitDuration, stat.EmptyAcquireWaitTime().Seconds(), identity)

			return nil
		},
		total, limit, waits, waitDuration,
	)
	if err != nil {
		return &poolMetrics{}
	}

	return &poolMetrics{registration: registration}
}

// unregister stops the callback. It runs before the pool is closed: a callback
// still registered would go on calling Stat on a pool that no longer exists.
func (m *poolMetrics) unregister() {
	if m == nil || m.registration == nil {
		return
	}

	_ = m.registration.Unregister()
}
