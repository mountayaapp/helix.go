package rest

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/mountaya/helix.go/integration"
	"github.com/mountaya/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// freePort returns a loopback address that was bindable a moment ago. The gap
// between closing the probe listener and the server binding is inherently racy,
// but it is the standard way to get a port the server itself can own - the server
// needs the address in its Config before it starts.
func freePort(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	return addr
}

// newClient returns an HTTP client with a connection pool of its own, emptied when
// the test ends.
//
// A test stops its Service while the client may still hold a connection, and
// net/http's Shutdown counts a connection that has not sent a request yet as busy
// for its first five seconds. A shared pool can hold one, dialed for a request
// that found another connection first; a pool of its own is one the test can
// empty before stopping, so that Stop drains only what the test is about.
func newClient(t *testing.T) *http.Client {
	t.Helper()

	client := &http.Client{Transport: &http.Transport{}}
	t.Cleanup(client.CloseIdleConnections)

	return client
}

// waitUntilServing polls the liveness probe at base until the server answers it.
func waitUntilServing(t *testing.T, client *http.Client, base string) {
	t.Helper()

	require.Eventually(t, func() bool {
		resp, err := client.Get(base + integration.PathLiveness)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 20*time.Millisecond, "server never became reachable")
}

// get sends a GET to url and discards the response.
func get(t *testing.T, client *http.Client, url string) {
	t.Helper()

	resp, err := client.Get(url)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

// goldenSpans returns the summaries of the spans recorded, leaving out the probes:
// the readiness polling is noise, and how many times it polled is not
// deterministic.
func goldenSpans(rec *servicetest.Recorder) []string {
	var got []string
	for _, s := range servicetest.Summarize(rec.Spans()) {
		if s.Name == "REST: GET "+integration.PathLiveness || s.Name == "REST: GET "+integration.PathReadiness {
			continue
		}

		got = append(got, s.String())
	}

	return got
}

// goldenMetrics returns the names of the instruments recorded into.
func goldenMetrics(rec *servicetest.Recorder) []string {
	var metrics []string
	for _, m := range rec.Metrics() {
		metrics = append(metrics, m.Name)
	}

	return metrics
}

// TestTelemetryGolden pins what this integration emits. It drives a real listener
// rather than the Handler, so that what is pinned is what a running server emits.
//
// A diff here is not automatically a failure: it is a change to the library's
// observable output, and it has to be an intentional one. Update the golden in
// the same commit that changes the instrumentation, never separately.
func TestTelemetryGolden(t *testing.T) {
	addr := freePort(t)
	svc, rec := servicetest.New(t)

	api, err := New(t.Context(), svc, Config{Address: addr})
	require.NoError(t, err)

	api.GET("/thing", func(rw http.ResponseWriter, req *http.Request) {
		NewResponseSuccess[NoMetadata, NoData](req).Write(rw)
	})

	api.GET("/things/:id", func(rw http.ResponseWriter, req *http.Request) {
		NewResponseSuccess[NoMetadata, NoData](req).Write(rw)
	})

	require.NoError(t, svc.Start(t.Context()))

	client := newClient(t)
	base := "http://" + addr
	waitUntilServing(t, client, base)

	get(t, client, base+"/thing")
	get(t, client, base+"/things/42")

	// A path nobody routes, to pin that a 404 is traced and that the unrouted path
	// does not become a span name.
	get(t, client, base+"/nowhere")
	client.CloseIdleConnections()

	// Stop before reading: the server span ends when the request completes, but
	// draining makes the ordering deterministic rather than dependent on when the
	// handler goroutine unwinds.
	require.NoError(t, svc.Stop(t.Context()))

	// One span per request, in one vocabulary. The span is opened around the whole
	// handler chain — before the application's middleware, so a response that
	// middleware writes is traced too — and named after the route once the router
	// has matched one: "/things/:id" rather than "/things/42", which keeps the span
	// name and the instruments from growing a new value per identifier. The
	// matched parameter's value is not recorded unless Config.RecordRouteParams
	// asks for it.
	//
	// The 404 is traced too, and carries no route, which is exactly why "/nowhere"
	// did not become a span name.
	want := []string{
		"github.com/mountaya/helix.go | server | REST: GET /thing | rest.method,rest.name,rest.path,rest.request.size,rest.response.size,rest.route,rest.status_code",
		"github.com/mountaya/helix.go | server | REST: GET /things/:id | rest.method,rest.name,rest.path,rest.request.size,rest.response.size,rest.route,rest.status_code",
		"github.com/mountaya/helix.go | server | REST: GET | rest.method,rest.name,rest.path,rest.request.size,rest.response.size,rest.status_code",
	}

	assert.Equal(t, want, goldenSpans(rec))
	servicetest.AssertConvention(t, rec.Spans())

	// The instruments are pinned by name rather than merely asserted non-empty: a
	// rename is exactly the kind of change that should surface here rather than in
	// somebody's dashboard.
	assert.ElementsMatch(t, []string{
		"rest.request.duration",
		"rest.request.size",
		"rest.response.size",
	}, goldenMetrics(rec))
}

// TestTelemetryGolden_OpenAPI pins what the OpenAPI validation adds: an internal
// span per validation, measured as an operation, and whether the request was
// validated at all on its entry span.
func TestTelemetryGolden_OpenAPI(t *testing.T) {
	addr := freePort(t)
	svc, rec := servicetest.New(t)

	api, err := New(t.Context(), svc, Config{
		Address: addr,
		OpenAPI: ConfigOpenAPI{
			Enabled:      true,
			Descriptions: []string{"./testdata/openapi.yaml"},
		},
	})
	require.NoError(t, err)

	api.GET("/things/:id", func(rw http.ResponseWriter, req *http.Request) {
		id, _ := ParamFromContext(req.Context(), "id")
		NewResponseSuccess[NoMetadata, map[string]string](req).
			SetData(map[string]string{"id": id}).
			Write(rw)
	})

	// A route no description declares, which is served unvalidated.
	api.GET("/undescribed", func(rw http.ResponseWriter, req *http.Request) {
		NewResponseSuccess[NoMetadata, NoData](req).Write(rw)
	})

	require.NoError(t, svc.Start(t.Context()))

	client := newClient(t)
	base := "http://" + addr
	waitUntilServing(t, client, base)

	get(t, client, base+"/things/42")
	get(t, client, base+"/undescribed")
	client.CloseIdleConnections()
	require.NoError(t, svc.Stop(t.Context()))

	// Spans are listed in the order they ended: the two validations of the first
	// request, then its entry span, then the entry span of the second, which was
	// never validated. Neither validation failed, and the probes — which no
	// description declares — were never matched against one.
	want := []string{
		"github.com/mountaya/helix.go | internal | REST: OpenAPI / Request Validation | rest.name",
		"github.com/mountaya/helix.go | internal | REST: OpenAPI / Response Validation | rest.name",
		"github.com/mountaya/helix.go | server | REST: GET /things/:id | rest.method,rest.name,rest.openapi.validated,rest.path,rest.request.size,rest.response.size,rest.route,rest.status_code",
		"github.com/mountaya/helix.go | server | REST: GET /undescribed | rest.method,rest.name,rest.openapi.validated,rest.path,rest.request.size,rest.response.size,rest.route,rest.status_code",
	}

	assert.Equal(t, want, goldenSpans(rec))
	servicetest.AssertConvention(t, rec.Spans())

	for _, span := range rec.Spans() {
		assert.Empty(t, span.Events(), "span %q recorded an error", span.Name())
	}

	assert.ElementsMatch(t, []string{
		"rest.operation.duration",
		"rest.request.duration",
		"rest.request.size",
		"rest.response.size",
	}, goldenMetrics(rec))
}
