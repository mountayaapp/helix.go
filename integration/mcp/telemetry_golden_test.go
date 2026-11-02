package mcp

import (
	"context"
	"net"
	"testing"

	"github.com/mountayaapp/helix.go/service/servicetest"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
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

// callGreet drives a real MCP client against the server: initialize, then one
// tools/call. Anything less would not exercise the transport the integration
// actually instruments.
func callGreet(t *testing.T, base string) {
	t.Helper()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "golden-client", Version: "v1.0.0"}, nil)

	// The server runs stateless by default, where GET on the MCP path is answered
	// with 405: there is no standalone SSE stream to establish.
	session, err := client.Connect(t.Context(), &mcpsdk.StreamableClientTransport{
		Endpoint:             base + "/mcp",
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	defer session.Close()

	result, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "helix"},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
}

// TestTelemetryGolden pins what this integration emits, so that changing how a
// span is produced cannot silently change what is produced. It records span
// names, emitting scope, kind and attribute keys - never values, timings or IDs,
// which differ on every run.
//
// It drives a real listener rather than calling the handler directly, so that
// what it pins is what New wires up and the Service serves.
//
// A diff here is not automatically a failure: it is a change to the library's
// observable output, and it has to be an intentional one. Update the golden in
// the same commit that changes the instrumentation, never separately.
func TestTelemetryGolden(t *testing.T) {
	addr := freePort(t)
	svc, rec := servicetest.New(t)

	cfg := toyConfig()
	cfg.Address = addr
	require.NoError(t, New(t.Context(), svc, cfg))
	require.NoError(t, svc.Start(t.Context()))

	base := "http://" + addr
	waitReachable(t, base)

	callGreet(t, base)

	require.NoError(t, svc.Stop(context.WithoutCancel(t.Context())))

	// Everything but the readiness polling above, whose repeat count is not
	// deterministic.
	var got []string
	for _, s := range servicetest.Summarize(rec.Spans()) {
		if s.Name != "MCP: GET" {
			got = append(got, s.String())
		}
	}

	// Every MCP call is a POST to the same path, so the HTTP spans are
	// indistinguishable from one another: "MCP: POST" twice, for a discovery and a
	// tool call. The inner spans say which is which, and mcp.tool says which tool.
	// They are named for the method only - the tool name is chosen by the client -
	// and are internal, since the HTTP span is the server one they nest under.
	//
	// Each inner span ends before the POST that carried it, which is what nesting
	// looks like once sorted by end time.
	want := []string{
		"github.com/mountayaapp/helix.go | internal | MCP: server/discover | mcp.name,mcp.rpc.method",
		"github.com/mountayaapp/helix.go | server | MCP: POST | mcp.method,mcp.name,mcp.path,mcp.request.size,mcp.response.size,mcp.status_code",
		"github.com/mountayaapp/helix.go | internal | MCP: tools/call | mcp.name,mcp.rpc.method,mcp.tool",
		"github.com/mountayaapp/helix.go | server | MCP: POST | mcp.method,mcp.name,mcp.path,mcp.request.size,mcp.response.size,mcp.status_code",
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
		"mcp.request.duration",
		"mcp.request.size",
		"mcp.response.size",
		"mcp.operation.duration",
	}, metrics)
}
