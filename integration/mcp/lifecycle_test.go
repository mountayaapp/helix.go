package mcp

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mountaya/helix.go/service/servicetest"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTeardownWithoutStart pins the defect that made Stop hang forever: the
// http.Server used to be built inside Start, which runs on a goroutine the
// Service owns, while Stop runs on another. When teardown won the race — a
// context already cancelled when Run was called, a SIGTERM during startup, or a
// caller stopping a Service it never started — Shutdown was called on a nil
// *http.Server, which does not panic. It blocks forever, ignoring its context,
// and took the whole teardown with it.
func TestTeardownWithoutStart(t *testing.T) {
	svc, _ := servicetest.New(t)

	require.NoError(t, New(t.Context(), svc, teardownConfig()))

	done := make(chan error, 1)
	go func() { done <- svc.Stop(context.Background()) }()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop(ctx) on a Service that never started did not return")
	}
}

// TestRunWithCancelledContext is the same defect reached through the path an
// application actually takes: Run starts the servers and, finding the context
// already done, goes straight to teardown while the server goroutine has not yet
// reached its listener.
func TestRunWithCancelledContext(t *testing.T) {
	svc, _ := servicetest.New(t)

	require.NoError(t, New(t.Context(), svc, teardownConfig()))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run(cancelled ctx) did not return")
	}
}

// TestStopWithOpenStream pins that a stream that never ends on its own — the
// standalone server-sent event stream a stateful session keeps open — does not
// hold shutdown past its budget.
func TestStopWithOpenStream(t *testing.T) {
	addr := freePort(t)
	svc, _ := servicetest.New(t)

	cfg := toyConfig()
	cfg.Address = addr
	cfg.Stateful = true
	require.NoError(t, New(t.Context(), svc, cfg))
	require.NoError(t, svc.Start(t.Context()))

	base := "http://" + addr
	waitReachable(t, base)

	// The client opens the standalone GET stream as part of connecting.
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v1.0.0"}, nil)
	session, err := client.Connect(t.Context(), &mcpsdk.StreamableClientTransport{Endpoint: base + "/mcp"}, nil)
	require.NoError(t, err)
	defer session.Close()

	_, err = session.ListTools(t.Context(), &mcpsdk.ListToolsParams{})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	started := time.Now()
	_ = svc.Stop(ctx)

	assert.Less(t, time.Since(started), 3*time.Second, "Stop overran its budget")
}

// waitReachable waits for the server at base to answer its liveness probe.
func waitReachable(t *testing.T, base string) {
	t.Helper()

	require.Eventually(t, func() bool {
		resp, err := http.Get(base + "/health")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 20*time.Millisecond, "server never became reachable")
}

// teardownConfig is the smallest valid Config: these tests care about the
// lifecycle, not about what the server serves.
func teardownConfig() Config {
	cfg := toyConfig()
	cfg.Address = "127.0.0.1:0"

	return cfg
}
