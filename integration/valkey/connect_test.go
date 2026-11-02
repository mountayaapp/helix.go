package valkey

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listen opens a TCP listener that never accepts on its own: a connection dialed
// to it completes in the kernel and waits, unanswered, in the backlog.
func listen(t *testing.T) *net.TCPListener {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	return ln.(*net.TCPListener)
}

// assertNothingDialed fails the test if a connection reached ln.
func assertNothingDialed(t *testing.T, ln *net.TCPListener) {
	t.Helper()

	require.NoError(t, ln.SetDeadline(time.Now().Add(200*time.Millisecond)))

	conn, err := ln.Accept()
	if err == nil {
		conn.Close()
	}

	var netErr net.Error
	assert.True(t, errors.As(err, &netErr) && netErr.Timeout(), "a connection was dialed; err=%v", err)
}

// TestConnect_InvalidTLSDoesNotDial pins that a TLS configuration that does not
// convert stops Connect before anything is dialed. The client used to be built
// anyway, with no TLS config, and its handshake sent HELLO with the credentials
// over plain TCP before the validation error came back.
func TestConnect_InvalidTLSDoesNotDial(t *testing.T) {
	ln := listen(t)
	svc, _ := servicetest.New(t)

	v, err := Connect(t.Context(), svc, Config{
		Address:  ln.Addr().String(),
		User:     "user",
		Password: "secret",
		TLS: integration.ConfigTLS{
			Enabled:    true,
			RootCAPEMs: [][]byte{[]byte("not a certificate")},
		},
	})

	require.Nil(t, v)
	assert.Equal(t, errorstack.NewValidation(errorstack.Entry{
		Message: "Must be a valid PEM-encoded root certificate",
		Path:    []any{"config", "tls", "root_ca_pems"},
	}), err)

	assertNothingDialed(t, ln)
}

// TestConnect_ConfigErrorsDoNotDial pins that nothing is dialed for a Config that
// does not validate.
func TestConnect_ConfigErrorsDoNotDial(t *testing.T) {
	ln := listen(t)
	svc, _ := servicetest.New(t)

	_, err := Connect(t.Context(), svc, Config{Address: ln.Addr().String(), Database: -1})

	assert.Equal(t, errorstack.CodeValidationFailed, codeOf(t, err))
	assertNothingDialed(t, ln)
}

// TestConnect_NilService pins that a missing Service is a validation error rather
// than a nil pointer dereference.
func TestConnect_NilService(t *testing.T) {
	v, err := Connect(t.Context(), nil, Config{})

	assert.Nil(t, v)
	assert.Equal(t, errorstack.NewValidation(errorstack.Entry{
		Message: "Must be set",
		Path:    []any{"service"},
	}), err)
}

// TestConnect_UnreachableIsServiceUnavailable pins how a server that cannot be
// reached is reported: as the dependency being unavailable, with the driver's
// reason, rather than as the Config failing validation.
func TestConnect_UnreachableIsServiceUnavailable(t *testing.T) {
	ln := listen(t)
	address := ln.Addr().String()
	ln.Close()

	svc, _ := servicetest.New(t)

	v, err := Connect(t.Context(), svc, Config{Address: address})

	require.Nil(t, v)
	entries := errorstack.EntriesOf(err)
	require.Len(t, entries, 1)
	assert.Equal(t, "Failed to connect to Valkey", entries[0].Message)
	assert.Equal(t, errorstack.CodeServiceUnavailable, entries[0].Extensions["code"])
	assert.Contains(t, entries[0].Extensions["reason"], "refused")
}

// TestConnect_BoundByContext pins that Connect returns once ctx is done. ctx used
// to be ignored: against a server that accepts connections and never answers,
// Connect waited out the driver's 5-second handshake timeout whatever the
// caller's deadline.
func TestConnect_BoundByContext(t *testing.T) {
	ln := listen(t)
	svc, _ := servicetest.New(t)

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	started := time.Now()
	v, err := Connect(ctx, svc, Config{Address: ln.Addr().String()})

	require.Nil(t, v)
	assert.Less(t, time.Since(started), 2*time.Second)
	assert.Equal(t, errorstack.CodeServiceUnavailable, codeOf(t, err))
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestConnect_DoneContextDoesNotDial pins that a Connect whose ctx is already
// done dials nothing.
func TestConnect_DoneContextDoesNotDial(t *testing.T) {
	ln := listen(t)
	svc, _ := servicetest.New(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := Connect(ctx, svc, Config{Address: ln.Addr().String()})

	assert.ErrorIs(t, err, context.Canceled)
	assertNothingDialed(t, ln)
}

// TestConnect_AttachFailureClosesClient pins that a client the Service refused to
// take is closed. It used to be returned from nowhere and kept open: every
// Connect failing on a duplicate name leaked a connection to the server.
func TestConnect_AttachFailureClosesClient(t *testing.T) {
	address := startValkey(t)
	svc, _ := servicetest.New(t)

	v, err := Connect(t.Context(), svc, Config{Address: address})
	require.NoError(t, err)

	_, err = Connect(t.Context(), svc, Config{Address: address, ClientName: "refused"})
	require.Error(t, err)
	assert.Equal(t, errorstack.CodeValidationFailed, codeOf(t, err))

	assert.Eventually(t, func() bool {
		list, err := v.Do(t.Context(), v.B().ClientList().Build()).ToString()
		return err == nil && !strings.Contains(list, "name=refused")
	}, 5*time.Second, 50*time.Millisecond, "the refused client's connection is still open")
}

// countingWriter is an io.Writer counting what goes through it.
type countingWriter struct {
	w io.Writer
	n *atomic.Int64
}

// Write writes p to the underlying writer and counts what was written.
func (cw countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.n.Add(int64(n))

	return n, err
}

// slowProxy forwards connections to upstream, holding the server's replies back
// by delay at first. Once the client closes a connection, it reports on the
// returned channel how many bytes of replies had reached the client.
func slowProxy(t *testing.T, upstream string, delay time.Duration) (string, <-chan int64) {
	t.Helper()

	ln := listen(t)
	closed := make(chan int64, 8)

	// Connections still open at the end — a client leaked by the code under test
	// — are closed, so that the cleanup does not wait on them forever.
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		conns []net.Conn
	)
	t.Cleanup(func() {
		ln.Close()

		mu.Lock()
		for _, conn := range conns {
			conn.Close()
		}
		mu.Unlock()

		wg.Wait()
	})

	wg.Go(func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}

			mu.Lock()
			conns = append(conns, client)
			mu.Unlock()

			wg.Go(func() {
				defer client.Close()

				server, err := net.Dial("tcp", upstream)
				if err != nil {
					return
				}
				defer server.Close()

				var answered atomic.Int64
				wg.Go(func() {
					time.Sleep(delay)
					_, _ = io.Copy(countingWriter{w: client, n: &answered}, server)
				})

				// The client side ending is the client closing the connection: the
				// server never closes first here.
				_, _ = io.Copy(server, client)
				closed <- answered.Load()
			})
		}
	})

	return ln.Addr().String(), closed
}

// TestConnect_LateClientIsClosed pins the other side of a Connect bounded by ctx:
// a client whose handshake completes after Connect stopped waiting is closed, not
// leaked.
func TestConnect_LateClientIsClosed(t *testing.T) {
	address := startValkey(t)
	proxy, closed := slowProxy(t, address, time.Second)
	svc, _ := servicetest.New(t)

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	_, err := Connect(ctx, svc, Config{Address: proxy})
	require.ErrorIs(t, err, context.DeadlineExceeded)

	select {
	case answered := <-closed:
		assert.Positive(t, answered, "the connection closed before its handshake completed")
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the client that connected after Connect returned is still open")
	}
}
