package httpclient

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mountaya/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTelemetryGolden pins what this integration emits. See the equivalent test
// in integration/bucket for the rationale: a diff here is a change to the
// library's observable output and must be intentional, updated in the same commit
// that changes the instrumentation.
func TestTelemetryGolden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc, rec := servicetest.New(t)

	client, err := Connect(t.Context(), svc, Config{
		Name:      "golden",
		Endpoints: []string{srv.URL},
	})
	require.NoError(t, err)

	resp, err := client.Get(t.Context(), "/thing")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	var got []string
	for _, s := range servicetest.Summarize(rec.Spans()) {
		got = append(got, s.String())
	}

	// Two spans for one call, and the split is deliberate.
	//
	// The outer one describes the *logical* request: how many endpoints were
	// tried (httpclient.attempts — failover happens above the transport, so the
	// transport cannot know it), and which endpoint, with which status, the caller
	// ended up with. It is internal rather than client: the attempts are the calls
	// that left the process, and two nested client spans would be drawn as two
	// calls on a service map.
	//
	// The inner one is one transport attempt: the layer that actually dials, and
	// the layer that injects the trace context into the outgoing headers. On a
	// three-endpoint failover there are three of these under one operation. It
	// alone carries the method, the path and the sizes, which the outer one would
	// only repeat. Both name the endpoint the same way, "<scheme>://<host>", and
	// neither carries a query string.
	//
	// The inner span ends first, which is the visible half of a bug fix. The
	// otelhttp transport this replaced ended its client span when the response body
	// was closed rather than when the round trip returned, so it sorted *after* the
	// span that contained it — and a caller who never closed the body never ended
	// the span at all, which is silent, because a span that never ends is never
	// exported. Reading the body size off Content-Length costs nothing and holds
	// nothing open.
	//
	// Both spans are named for the method rather than for the layer alone: a trace
	// making twenty outbound calls used to show twenty spans called "Attempt" and
	// twenty called "Request". The endpoint and the path stay attributes, where
	// their cardinality costs nothing and a golden can be pinned — a name carrying
	// an ephemeral port would test the environment instead of the instrumentation.
	want := []string{
		"github.com/mountaya/helix.go | client | HTTP Client: GET / Attempt | httpclient.endpoint,httpclient.method,httpclient.name,httpclient.path,httpclient.request.size,httpclient.response.size,httpclient.status_code",
		"github.com/mountaya/helix.go | internal | HTTP Client: GET | httpclient.attempts,httpclient.endpoint,httpclient.name,httpclient.status_code",
	}

	assert.Equal(t, want, got)

	servicetest.AssertConvention(t, rec.Spans())

	// The instruments are pinned by name rather than merely asserted non-empty: a
	// rename is exactly the kind of change that should surface here rather than in
	// somebody's dashboard. They are named in this integration's own namespace, not
	// http.client.*, for the same reason its attributes are.
	var metrics []string
	for _, m := range rec.Metrics() {
		metrics = append(metrics, m.Name)
	}

	assert.ElementsMatch(t, []string{
		"httpclient.operation.duration",
		"httpclient.request.duration",
		"httpclient.request.size",
		"httpclient.response.size",
	}, metrics)
}
