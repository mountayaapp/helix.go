package rest

import (
	"bufio"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mountaya/helix.go/integration"
	"github.com/mountaya/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReturnedHandleCarriesNoLifecycle pins the invariant: what New hands back must
// not implement integration.Server, so that no type assertion can reach Start or
// Stop. The Service owns the server's lifecycle.
func TestReturnedHandleCarriesNoLifecycle(t *testing.T) {
	svc, _ := servicetest.New(t)

	api, err := New(t.Context(), svc, Config{Address: "127.0.0.1:0"})
	require.NoError(t, err)

	var handle any = api
	_, isServer := handle.(integration.Server)
	assert.False(t, isServer,
		"the value handed to a caller must not implement integration.Server")

	// Nor may a group, which is the other value a caller holds.
	var group any = api.Group("/v1")
	_, isServer = group.(integration.Server)
	assert.False(t, isServer,
		"a group handed to a caller must not implement integration.Server")
}

// TestTeardownWithoutStart pins the defect that made Stop hang forever: the
// http.Server used to be built inside Start, which runs on a goroutine the
// Service owns, while Stop runs on another. When teardown won the race — a
// context already cancelled when Run was called, a SIGTERM during startup, or a
// caller stopping a Service it never started — Shutdown was called on a nil
// *http.Server, which does not panic. It blocks forever, ignoring its context,
// and took the whole teardown with it.
//
// Both halves are asserted: Stop returns, and it returns within the budget.
func TestTeardownWithoutStart(t *testing.T) {
	svc, _ := servicetest.New(t)

	_, err := New(t.Context(), svc, Config{Address: "127.0.0.1:0"})
	require.NoError(t, err)

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

	_, err := New(t.Context(), svc, Config{Address: "127.0.0.1:0"})
	require.NoError(t, err)

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

// TestStopEndsAnOpenStream pins that a stream — a server-sent event route waived
// from every timeout, which never ends on its own — does not hold shutdown past
// its budget: once draining runs out of half the budget, the stream's context is
// cancelled, and the handler returns.
func TestStopEndsAnOpenStream(t *testing.T) {
	addr := freePort(t)
	svc, _ := servicetest.New(t)

	api, err := New(t.Context(), svc, Config{Address: addr})
	require.NoError(t, err)

	ended := make(chan struct{})
	api.GET("/events", func(rw http.ResponseWriter, req *http.Request) {
		defer close(ended)

		rw.Header().Set("Content-Type", "text/event-stream")
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("data: open\n\n"))
		_ = http.NewResponseController(rw).Flush()

		<-req.Context().Done()
	}, WithoutTimeout())

	require.NoError(t, svc.Start(t.Context()))

	client := newClient(t)
	waitUntilServing(t, client, "http://"+addr)

	res, err := client.Get("http://" + addr + "/events")
	require.NoError(t, err)
	defer res.Body.Close()

	// The first event proves the stream is open before shutdown begins.
	line, err := bufio.NewReader(res.Body).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "data: open\n", line)

	// The stream's is now the only connection left to drain.
	client.CloseIdleConnections()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	started := time.Now()
	assert.NoError(t, svc.Stop(ctx))
	assert.Less(t, time.Since(started), 2*time.Second, "Stop must end within its budget")

	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("the stream's handler never returned")
	}
}
