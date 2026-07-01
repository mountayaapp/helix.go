package httpclient

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/telemetry/trace"
)

/*
Ensure *connection complies to the integration.Dependency type.
*/
var _ integration.Dependency = (*connection)(nil)

/*
Name returns the human-readable name of the client.
*/
func (conn *connection) Name() string {
	return conn.config.Name
}

/*
Close releases idle connections held by the underlying HTTP client.
*/
func (conn *connection) Close(ctx context.Context) error {
	conn.client.CloseIdleConnections()

	return nil
}

/*
Status reports the health of the client. When a custom Status function is
configured in the Config, it is used as is. Otherwise, every endpoint is probed
concurrently with GET {endpoint}{HealthPath}: the integration is healthy (`200`)
only when every endpoint is reachable and responds below 500, and unhealthy
(`503`) otherwise, with one error entry naming each failing endpoint. Because a
4xx still counts as reachable, an API that exposes no health endpoint is healthy
without any configuration.
*/
func (conn *connection) Status(ctx context.Context) (int, error) {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanStatus)
	defer span.End()

	if conn.config.Status != nil {
		status, err := conn.config.Status(ctx, conn)
		if err != nil {
			span.RecordError("health check failed", err)
		}

		return status, err
	}

	var (
		mu      sync.Mutex
		worst   = http.StatusOK
		entries []errorstack.Entry
		wg      sync.WaitGroup
	)

	check := func(status int, err error) {
		mu.Lock()
		if status > worst {
			worst = status
		}
		if err != nil {
			entries = append(entries, errorstack.EntriesOf(err)...)
		}
		mu.Unlock()
	}

	for _, endpoint := range conn.endpoints {
		wg.Go(func() {
			check(conn.probe(ctx, endpoint))
		})
	}

	wg.Wait()

	if len(entries) > 0 {
		err := errorstack.New("Integration is not in a healthy state",
			errorstack.WithCode(errorstack.CodeServiceUnavailable),
		).Append(entries...)

		span.RecordError("health check failed", err)
		return worst, err
	}

	return worst, nil
}

/*
probe checks a single endpoint by issuing GET {endpoint}{HealthPath}. The endpoint
is healthy (`200`) as long as it is reachable and responds with a status below 500
— so an API that exposes no health endpoint (returning 404) is still healthy. A
transport error or a 5xx response yields `503` and an error naming the endpoint.
*/
func (conn *connection) probe(ctx context.Context, endpoint string) (int, error) {
	resp, err := conn.attempt(ctx, endpoint, http.MethodGet, conn.config.HealthPath, nil)
	if err != nil {
		return http.StatusServiceUnavailable, errorstack.Wrap(err, fmt.Sprintf("Endpoint %q is not reachable", endpoint),
			errorstack.WithCode(errorstack.CodeServiceUnavailable),
			errorstack.WithPath("endpoints"),
		)
	}

	defer drainClose(resp)

	// Reachable and not server-erroring is healthy: a 4xx (e.g. 404 when the API
	// has no health endpoint) still means the endpoint is up and serving requests.
	if resp.StatusCode < 500 {
		return http.StatusOK, nil
	}

	return http.StatusServiceUnavailable, errorstack.New(fmt.Sprintf("Endpoint %q returned an unhealthy status (%d)", endpoint, resp.StatusCode),
		errorstack.WithCode(errorstack.CodeServiceUnavailable),
		errorstack.WithPath("endpoints"),
	)
}
