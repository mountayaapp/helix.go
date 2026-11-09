package httpclient

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	otelmetric "go.opentelemetry.io/otel/metric"
)

// reachabilityInterval is how often an optional client probes its upstream for
// the httpclient.reachable gauge, and reachabilityTimeout bounds each probe.
// Variables, so tests can shorten them.
var (
	reachabilityInterval = 15 * time.Second
	reachabilityTimeout  = 5 * time.Second
)

// Whether an upstream answered its last probe, as a prober keeps it: unknown until
// the first probe ends, which reports nothing.
const (
	unknown int32 = iota
	unreachable
	reachable
)

// prober probes an optional client's upstream (Config.Optional) every
// reachabilityInterval, through the health check CheckHealth would run, and reports
// on the httpclient.reachable gauge whether it answered: per endpoint when the
// client probes HealthPath, for the client as a whole when a custom HealthCheck
// decides. It is what reports the upstream once readiness no longer does.
type prober struct {
	conn *connection

	// states holds, per endpoint, or once for a custom HealthCheck, whether the
	// last probe was answered.
	states []atomic.Int32

	// ctx ends when the prober is closed, ending the probe in flight with it.
	ctx    context.Context
	cancel context.CancelFunc

	mu           sync.Mutex
	started      bool
	done         chan struct{}
	registration otelmetric.Registration
}

// newProber returns the prober of conn, not started, and nil when conn is not
// optional or has nothing to probe.
func newProber(conn *connection) *prober {
	if !conn.config.Optional || (conn.config.HealthCheck == nil && conn.config.health == nil) {
		return nil
	}

	states := 1
	if conn.config.HealthCheck == nil {
		states = len(conn.endpoints)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &prober{
		conn:   conn,
		states: make([]atomic.Int32, states),
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
}

// start registers the gauge and starts probing, once, the first probe an interval
// later.
func (p *prober) start() {
	if p == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.started || p.ctx.Err() != nil {
		return
	}

	p.started = true
	p.register()

	go p.run(reachabilityInterval, reachabilityTimeout)
}

// run probes the upstream every interval, each probe bounded by timeout, until the
// prober is closed. The first probe comes an interval after the client is attached,
// not at it: a client a short-lived process attaches and closes, a one-off command
// or a test, probes nothing, and the replicas a rollout starts together do not all
// probe in the same second they start. A probe that lasts delays the next one rather
// than stacking them.
func (p *prober) run(interval, timeout time.Duration) {
	defer close(p.done)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
		}

		p.probe(timeout)
	}
}

// probe probes the upstream once, within timeout, and records what it found,
// unless the prober was closed meanwhile.
func (p *prober) probe(timeout time.Duration) {
	ctx, cancel := context.WithTimeout(p.ctx, timeout)
	defer cancel()

	conn := p.conn
	if conn.config.HealthCheck != nil {
		err := conn.config.HealthCheck(ctx, conn.probe)
		p.record(0, err)

		return
	}

	var wg sync.WaitGroup
	for i, e := range conn.endpoints {
		wg.Go(func() {
			p.record(i, conn.probe.checkEndpoint(ctx, e))
		})
	}

	wg.Wait()
}

// record keeps whether the probe of state i was answered, err nil, unless the
// prober was closed meanwhile, which ends a probe in flight with an error of its
// own.
func (p *prober) record(i int, err error) {
	if p.ctx.Err() != nil {
		return
	}

	state := unreachable
	if err == nil {
		state = reachable
	}

	p.states[i].Store(state)
}

// register registers the httpclient.reachable gauge. A provider that rejects the
// instrument hands back a working no-op, so a failure is dropped rather than
// turned into a client failure over telemetry.
func (p *prober) register() {
	inst := p.conn.inst
	meter := inst.Meter()

	gauge, err := meter.Int64ObservableGauge(inst.Identifier()+".reachable",
		otelmetric.WithUnit("1"),
		otelmetric.WithDescription("Whether the upstream answered the last background probe of an optional client: 1 when it did, 0 when it did not."),
	)
	if err != nil {
		return
	}

	attrs := make([]otelmetric.ObserveOption, len(p.states))
	for i := range p.states {
		identity := inst.AppendIdentity(nil)
		if p.conn.config.HealthCheck == nil {
			identity = append(identity, attrKeyEndpoint.String(p.conn.endpoints[i].origin))
		}

		attrs[i] = otelmetric.WithAttributes(identity...)
	}

	registration, err := meter.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		for i := range p.states {
			state := p.states[i].Load()
			if state == unknown {
				continue
			}

			var answered int64
			if state == reachable {
				answered = 1
			}
			observer.ObserveInt64(gauge, answered, attrs[i])
		}

		return nil
	}, gauge)
	if err != nil {
		return
	}

	p.registration = registration
}

// close stops probing and the gauge, and waits for a probe in flight within ctx.
func (p *prober) close(ctx context.Context) {
	if p == nil {
		return
	}

	p.mu.Lock()
	started := p.started
	p.cancel()
	if p.registration != nil {
		_ = p.registration.Unregister()
		p.registration = nil
	}
	p.mu.Unlock()

	if !started {
		return
	}

	select {
	case <-p.done:
	case <-ctx.Done():
	}
}
