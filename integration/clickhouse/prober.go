package clickhouse

import (
	"context"
	"sync"
	"time"
)

// reachabilityInterval is how often a connection pings its server for the
// clickhouse.reachable gauge, and reachabilityTimeout bounds each ping once a
// connection is established; a dial is bounded by Config.DialTimeout alone.
// Variables, so tests can shorten them.
var (
	reachabilityInterval = 15 * time.Second
	reachabilityTimeout  = 5 * time.Second
)

// prober pings a connection's server every reachabilityInterval and records
// whether it answered, so clickhouse.reachable follows the server whether or not
// anything else pings it: a readiness probe that an optional connection answers
// without a ping, or no probe at all.
type prober struct {
	conn *connection

	// ctx ends when the prober is closed, ending the ping in flight with it.
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	started bool
	done    chan struct{}
}

// newProber returns the prober of conn, not started.
func newProber(conn *connection) *prober {
	ctx, cancel := context.WithCancel(context.Background())

	return &prober{conn: conn, ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

// start starts pinging, once.
func (p *prober) start() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.started || p.ctx.Err() != nil {
		return
	}

	p.started = true
	go p.run()
}

// run pings the server on every tick until the prober is closed. A ping that
// lasts, a dial to a server that does not answer, delays the next one rather
// than stacking them.
func (p *prober) run() {
	defer close(p.done)

	ticker := time.NewTicker(reachabilityInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
		}

		ctx, cancel := context.WithTimeout(p.ctx, reachabilityTimeout)
		answered := p.conn.client.Ping(ctx) == nil
		cancel()

		if p.ctx.Err() == nil {
			p.conn.reachable.Store(answered)
		}
	}
}

// close stops pinging, and waits for a ping in flight within ctx: a ping that is
// dialing ignores its context, and Close does not wait on it past its own.
func (p *prober) close(ctx context.Context) {
	if p == nil {
		return
	}

	p.mu.Lock()
	started := p.started
	p.cancel()
	p.mu.Unlock()

	if !started {
		return
	}

	select {
	case <-p.done:
	case <-ctx.Done():
	}
}
