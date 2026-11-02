package integration

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startLifecycle starts l on a free local port and waits until it accepts.
func startLifecycle(t *testing.T, l *HTTPLifecycle) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	l.server.Addr = address
	go l.Start(t.Context())

	require.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", address)
		if err != nil {
			return false
		}

		conn.Close()
		return true
	}, 5*time.Second, 10*time.Millisecond)

	return address
}

func TestHTTPLifecycle_AppliesDefaults(t *testing.T) {
	server := &http.Server{}
	NewHTTPLifecycle(context.Background(), server)

	assert.Equal(t, DefaultReadHeaderTimeout, server.ReadHeaderTimeout)
	assert.Equal(t, DefaultIdleTimeout, server.IdleTimeout)
	assert.NotNil(t, server.ErrorLog)
	assert.NotNil(t, server.BaseContext)
}

func TestHTTPLifecycle_KeepsExplicitTimeouts(t *testing.T) {
	server := &http.Server{ReadHeaderTimeout: time.Second, IdleTimeout: 2 * time.Second}
	NewHTTPLifecycle(context.Background(), server)

	assert.Equal(t, time.Second, server.ReadHeaderTimeout)
	assert.Equal(t, 2*time.Second, server.IdleTimeout)
}

type baseKey struct{}

func TestHTTPLifecycle_RequestsDeriveFromTheBaseContext(t *testing.T) {
	seen := make(chan any, 1)
	l := NewHTTPLifecycle(context.WithValue(context.Background(), baseKey{}, "service"), &http.Server{
		Handler: http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
			seen <- req.Context().Value(baseKey{})
		}),
	})

	address := startLifecycle(t, l)
	t.Cleanup(func() { l.Stop(context.Background()) })

	resp, err := http.Get("http://" + address)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, "service", <-seen)
}

func TestHTTPLifecycle_StopWithoutStart(t *testing.T) {
	l := NewHTTPLifecycle(context.Background(), &http.Server{Addr: "127.0.0.1:0"})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	assert.NoError(t, l.Stop(ctx))
}

// TestHTTPLifecycle_StopEndsAStream pins that a request which never ends on its
// own — a server-sent event stream — cannot hold shutdown past its budget: its
// context is cancelled once draining runs out of time.
func TestHTTPLifecycle_StopEndsAStream(t *testing.T) {
	streaming := make(chan struct{})
	l := NewHTTPLifecycle(context.Background(), &http.Server{
		Handler: http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			rw.WriteHeader(http.StatusOK)
			http.NewResponseController(rw).Flush()
			close(streaming)
			<-req.Context().Done()
		}),
	})

	address := startLifecycle(t, l)

	go func() {
		resp, err := http.Get("http://" + address)
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-streaming

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	started := time.Now()
	assert.NoError(t, l.Stop(ctx))
	assert.Less(t, time.Since(started), time.Second)
}
