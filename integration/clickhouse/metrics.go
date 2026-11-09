package clickhouse

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/mountayaapp/helix.go/integration"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	otelmetric "go.opentelemetry.io/otel/metric"
)

// poolMetrics observes the connection pool.
//
// A pool is the one part of a database integration a trace describes badly: the
// span for a query says this one was slow, but nothing says the pool has been
// saturated for ten minutes. These are gauges because what matters is the level,
// and they are observed on collection rather than recorded on use, so an idle
// service still reports the shape of its pool.
//
// It is registered only once the connection has been attached to the Service,
// and unregistered when the Service closes it. Close can run on the Service's
// goroutine while Connect is still finishing on the caller's, hence the lock.
type poolMetrics struct {
	mu           sync.Mutex
	registration otelmetric.Registration
	closed       bool
}

// register registers the observable gauges for conn, and for whether its server
// was reachable at the last ping. A provider that rejects an instrument hands
// back a working no-op, so failures are dropped rather than turned into a
// connection failure over telemetry. It does nothing once unregister has run: a
// callback registered after Close would go on reading a pool that is gone.
func (m *poolMetrics) register(inst *integration.Instrument, conn driver.Conn, reachable *atomic.Bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return
	}

	meter := inst.Meter()

	connections, err := meter.Int64ObservableGauge(inst.Identifier()+".pool.connections",
		otelmetric.WithUnit("{connection}"),
		otelmetric.WithDescription("Connections currently held by the pool, by state."),
	)
	if err != nil {
		return
	}

	maxConnections, err := meter.Int64ObservableGauge(inst.Identifier()+".pool.connections.max",
		otelmetric.WithUnit("{connection}"),
		otelmetric.WithDescription("Maximum number of connections the pool may hold open."),
	)
	if err != nil {
		return
	}

	up, err := meter.Int64ObservableGauge(inst.Identifier()+".reachable",
		otelmetric.WithUnit("1"),
		otelmetric.WithDescription("Whether the server answered the last ping, at connect or at a health check: 1 when it did, 0 when it did not."),
	)
	if err != nil {
		return
	}

	// The state attribute is what makes one gauge answer "how saturated is this
	// pool". A connection is either idle in the pool or acquired by a statement —
	// including one being dialed for it — so the two sum to what is open, and
	// acquired against the maximum is the headroom left before a statement has to
	// wait for a connection.
	idle := inst.AppendIdentity(nil)
	idle = append(idle, attrKeyPoolState.String("idle"))

	acquired := inst.AppendIdentity(nil)
	acquired = append(acquired, attrKeyPoolState.String("acquired"))

	identity := otelmetric.WithAttributes(inst.AppendIdentity(nil)...)

	registration, err := meter.RegisterCallback(
		func(_ context.Context, observer otelmetric.Observer) error {
			stats := conn.Stats()

			// The driver's Open is the count of connections handed out, not of
			// connections open: it is the semaphore a statement takes before it
			// gets a connection, idle or new, and gives back when it is done.
			observer.ObserveInt64(connections, int64(stats.Idle), otelmetric.WithAttributes(idle...))
			observer.ObserveInt64(connections, int64(stats.Open), otelmetric.WithAttributes(acquired...))
			observer.ObserveInt64(maxConnections, int64(stats.MaxOpenConns), identity)

			var reached int64
			if reachable.Load() {
				reached = 1
			}
			observer.ObserveInt64(up, reached, identity)

			return nil
		},
		connections, maxConnections, up,
	)
	if err != nil {
		return
	}

	m.registration = registration
}

// unregister stops the callback, and stops a later register from starting it. It
// runs before the connection is closed: a callback still registered would go on
// calling Stats on a pool that is gone.
func (m *poolMetrics) unregister() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closed = true
	if m.registration == nil {
		return
	}

	_ = m.registration.Unregister()
	m.registration = nil
}
