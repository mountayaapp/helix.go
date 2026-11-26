package graphql

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

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

// waitReachable waits until the server listening on addr answers its liveness
// probe.
func waitReachable(t *testing.T, addr string) {
	t.Helper()

	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + addr + "/health")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 20*time.Millisecond, "server never became reachable")
}

// TestTelemetryGolden pins what this integration emits, so that changing how a
// span is produced cannot silently change what is produced. It records span
// names, emitting scope, kind and attribute keys - never values, timings or IDs,
// which differ on every run.
//
// It drives a real listener, through New and the Service, rather than calling a
// handler directly: what it pins is what an application running this integration
// emits, from the HTTP span down.
//
// A diff here is not automatically a failure: it is a change to the library's
// observable output, and it has to be an intentional one. Update the golden in
// the same commit that changes the instrumentation, never separately.
func TestTelemetryGolden(t *testing.T) {
	addr := freePort(t)
	svc, rec := servicetest.New(t)

	cfg := testConfig(map[string]resolverFunc{"name": constant("helix")})
	cfg.Address = addr
	require.NoError(t, New(t.Context(), svc, cfg))
	require.NoError(t, svc.Start(t.Context()))

	waitReachable(t, addr)
	base := "http://" + addr

	resp, err := http.Post(base+"/graphql", "application/json", strings.NewReader(`{"query":"query Golden { name }"}`))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, svc.Stop(context.WithoutCancel(t.Context())))

	// Everything but the readiness polling above, whose repeat count is not
	// deterministic.
	var got []string
	for _, s := range servicetest.Summarize(rec.Spans()) {
		if s.Name != "GraphQL: GET" {
			got = append(got, s.String())
		}
	}

	// Three spans for one request, and the inner two are the point.
	//
	// A GraphQL API serves every operation from a single path, so the HTTP span is
	// precisely the span that cannot tell two requests apart. The operation span
	// can: it is named after the operation type, never after the name a client
	// chose, which is an attribute. Under it, the root field's resolver — the work
	// the operation actually did.
	//
	// They end innermost first, each nested inside the one that carried it.
	want := []string{
		"github.com/mountaya/helix.go | internal | GraphQL: Query.name | graphql.field,graphql.name,graphql.object",
		"github.com/mountaya/helix.go | internal | GraphQL: Query | graphql.name,graphql.operation.name,graphql.operation.type",
		"github.com/mountaya/helix.go | server | GraphQL: POST | graphql.method,graphql.name,graphql.path,graphql.request.size,graphql.response.size,graphql.status_code",
	}

	assert.Equal(t, want, got)

	servicetest.AssertConvention(t, rec.Spans())

	// The instruments are pinned by name rather than merely asserted non-empty: a
	// rename is exactly the kind of change that should surface here rather than in
	// somebody's dashboard.
	var metrics []string
	for _, m := range rec.Metrics() {
		metrics = append(metrics, m.Name)
	}

	assert.ElementsMatch(t, []string{
		"graphql.operation.duration",
		"graphql.request.duration",
		"graphql.request.size",
		"graphql.response.size",
	}, metrics)
}
