package graphql

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mountaya/helix.go/service/servicetest"

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

	require.NoError(t, New(t.Context(), svc, Config{Address: "127.0.0.1:0", Schema: testSchema(nil)}))

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

	require.NoError(t, New(t.Context(), svc, Config{Address: "127.0.0.1:0", Schema: testSchema(nil)}))

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

// TestStopDrainsInFlightRequest pins the drain: a request still being resolved
// when the Service stops is answered, not cut off, and Stop returns once it is.
func TestStopDrainsInFlightRequest(t *testing.T) {
	addr := freePort(t)
	svc, _ := servicetest.New(t)

	started := make(chan struct{})
	release := make(chan struct{})
	cfg := testConfig(map[string]resolverFunc{"name": func(context.Context) (any, error) {
		close(started)
		<-release
		return "helix", nil
	}})
	cfg.Address = addr

	require.NoError(t, New(t.Context(), svc, cfg))
	require.NoError(t, svc.Start(t.Context()))

	waitReachable(t, addr)

	answered := make(chan int, 1)
	go func() {
		resp, err := http.Post("http://"+addr+"/graphql", "application/json", strings.NewReader(`{"query":"{ name }"}`))
		if err != nil {
			answered <- 0
			return
		}
		_ = resp.Body.Close()
		answered <- resp.StatusCode
	}()

	<-started

	stopped := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stopped <- svc.Stop(ctx)
	}()

	// Let Stop begin draining before the request completes.
	time.Sleep(50 * time.Millisecond)
	close(release)

	select {
	case status := <-answered:
		assert.Equal(t, http.StatusOK, status, "the in-flight request must be answered")
	case <-time.After(5 * time.Second):
		t.Fatal("the in-flight request was never answered")
	}

	select {
	case err := <-stopped:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop(ctx) did not return once the request was answered")
	}
}

// TestSlowClient_IsDisconnected pins the P0: the http.Server had no timeouts at
// all, so a client sending its headers one byte at a time — or never finishing
// them — held a connection and a goroutine for as long as it liked.
func TestSlowClient_IsDisconnected(t *testing.T) {
	addr := freePort(t)
	svc, _ := servicetest.New(t)

	cfg := testConfig(nil)
	cfg.Address = addr
	cfg.ReadHeaderTimeout = 100 * time.Millisecond

	require.NoError(t, New(t.Context(), svc, cfg))
	require.NoError(t, svc.Start(t.Context()))

	waitReachable(t, addr)

	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	// Headers that never end.
	_, err = conn.Write([]byte("POST /graphql HTTP/1.1\r\nHost: localhost\r\n"))
	require.NoError(t, err)

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = io.ReadAll(conn)
	assert.NoError(t, err, "the server must close the connection rather than wait for the headers")
}
