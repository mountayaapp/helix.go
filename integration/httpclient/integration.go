package httpclient

import (
	"context"
	"net/http"
	"sync"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"
)

// Ensure dependency complies to the integration.Dependency type.
var _ integration.Dependency = dependency{}

// dependency adapts a connection to integration.Dependency, and is what gets
// registered with the Service.
//
// It is a distinct type rather than methods on connection so that the value handed
// back to the caller does not carry the lifecycle contract — not even reachable
// through a type assertion. The Service owns the HTTP client;
// the caller only gets to use it.
type dependency struct {
	conn *connection
}

// Close stops the background probe of an optional client (Config.Optional), and
// waits for a probe in flight within ctx, then releases idle connections held by
// the underlying transport. The instrumented transport forwards the call to the
// one it wraps — the client's own, or Config.Transport — which the probe shares.
func (d dependency) Close(ctx context.Context) error {
	d.conn.prober.close(ctx)
	d.conn.client.CloseIdleConnections()

	return nil
}

// CheckHealth reports whether the client can reach its endpoints.
//
// When a custom HealthCheck is configured it decides, and a failure it reports is
// wrapped with integration.Unhealthy. Otherwise, when a HealthPath is configured,
// every endpoint is probed concurrently with GET {endpoint}{HealthPath}: the
// integration is healthy only when every endpoint is reachable and responds below
// 500, and unhealthy otherwise, with one entry per failing endpoint, in
// configuration order, whose path is its index in Endpoints. Because a 4xx still
// counts as reachable, an API that exposes no health endpoint is healthy. When
// neither is configured, the client is always healthy and nothing is sent.
//
// An optional client (Config.Optional) is reported healthy at once, without a
// probe: what its upstream does never gates the Service's readiness, and a probe
// to an upstream that hangs would hold a readiness probe for as long as the
// client's Timeout. Its background probe reports the upstream on the
// httpclient.reachable gauge instead.
//
// Probes are neither traced nor measured: see connection.probe.
func (d dependency) CheckHealth(ctx context.Context) error {
	conn := d.conn

	if conn.config.Optional {
		return nil
	}

	return conn.checkHealth(ctx)
}

// checkHealth probes the client's upstream as CheckHealth describes, whether or
// not the client is optional: the background probe of an optional one runs it too.
func (conn *connection) checkHealth(ctx context.Context) error {
	if conn.config.HealthCheck != nil {
		return integration.Unhealthy(conn.config.HealthCheck(ctx, conn.probe))
	}

	if conn.config.health == nil {
		return nil
	}

	var (
		errs = make([]error, len(conn.endpoints))
		wg   sync.WaitGroup
	)

	for i, e := range conn.endpoints {
		wg.Go(func() {
			errs[i] = conn.probe.checkEndpoint(ctx, e)
		})
	}

	wg.Wait()

	// Collected by index rather than as the probes finish, so that the same
	// failures always read the same way.
	var entries []errorstack.Entry
	for i, err := range errs {
		entries = append(entries, errorstack.Nest(integration.Unhealthy(err), "endpoints", i)...)
	}

	if len(entries) == 0 {
		return nil
	}

	return &errorstack.Error{Entries: entries}
}

// checkEndpoint checks a single endpoint by issuing GET {endpoint}{HealthPath}.
// The endpoint is healthy as long as it is reachable and responds with a status
// below 500 — so an API that exposes no health endpoint (returning 404) is still
// healthy. A transport error or a 5xx response is returned as the reason it is
// not.
func (conn *connection) checkEndpoint(ctx context.Context, e *endpoint) error {
	req := (&http.Request{
		Method: http.MethodGet,
		URL:    conn.config.health,
		Header: make(http.Header),
	}).WithContext(ctx)

	resp, err := conn.attempt(ctx, e, req, true)
	if err != nil {
		return err
	}

	defer drainClose(resp)

	// Reachable and not server-erroring is healthy: a 4xx (e.g. 404 when the API
	// has no health endpoint) still means the endpoint is up and serving requests.
	if resp.StatusCode < http.StatusInternalServerError {
		return nil
	}

	return statusError(resp.StatusCode)
}
