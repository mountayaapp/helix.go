package temporal

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingListener accepts TCP connections and closes them at once, counting
// them: a stand-in for a server that tells whether anything dialed it at all.
func countingListener(t *testing.T) (string, *atomic.Int32) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	var accepted atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			accepted.Add(1)
			_ = conn.Close()
		}
	}()

	return listener.Addr().String(), &accepted
}

// invalidTLS is a TLS configuration that passes Sanitize — both halves of the pair
// are there — and cannot be built.
var invalidTLS = integration.ConfigTLS{
	Enabled: true,
	CertPEM: []byte("not a certificate"),
	KeyPEM:  []byte("not a key"),
}

// TestConnect_RefusesPlaintextWhenTLSCannotBeBuilt pins the defect that sent a
// client which asked for TLS to the server in plaintext: the TLS conversion error
// was collected, the TLS option left nil, and the client dialed anyway — and
// handed back the connection it made on the side.
func TestConnect_RefusesPlaintextWhenTLSCannotBeBuilt(t *testing.T) {
	address, accepted := countingListener(t)
	svc, _ := servicetest.New(t)

	c, err := Connect(t.Context(), svc, ConfigClient{Address: address, TLS: invalidTLS})

	assert.Nil(t, c)

	var e *errorstack.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errorstack.CodeValidationFailed, e.Entries[0].Extensions["code"])
	assert.Equal(t, []any{"config", "tls"}, e.Entries[0].Path)

	assert.Never(t, func() bool { return accepted.Load() > 0 }, 250*time.Millisecond, 10*time.Millisecond,
		"the client dialed the server although its TLS configuration could not be built")
}

func TestNew_RefusesPlaintextWhenTLSCannotBeBuilt(t *testing.T) {
	address, accepted := countingListener(t)
	svc, _ := servicetest.New(t)

	c, w, err := New(t.Context(), svc, ConfigWorker{
		Client:    ConfigClient{Address: address, TLS: invalidTLS},
		TaskQueue: "orders",
	})

	assert.Nil(t, c)
	assert.Nil(t, w)

	var e *errorstack.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, []any{"config", "client", "tls"}, e.Entries[0].Path)

	assert.Never(t, func() bool { return accepted.Load() > 0 }, 250*time.Millisecond, 10*time.Millisecond,
		"the client dialed the server although its TLS configuration could not be built")
}

func TestConnect_UnreachableServerIsUnavailable(t *testing.T) {
	address, _ := countingListener(t)
	svc, _ := servicetest.New(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// The listener closes every connection it accepts: there is no Temporal
	// server behind it.
	_, err := Connect(ctx, svc, ConfigClient{Address: address})

	var e *errorstack.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "Failed to connect to Temporal", e.Entries[0].Message)
	assert.Equal(t, errorstack.CodeServiceUnavailable, e.Entries[0].Extensions["code"])
}

func TestConnect_InvalidConfigurationDialsNothing(t *testing.T) {
	svc, _ := servicetest.New(t)

	_, err := Connect(t.Context(), svc, ConfigClient{Address: "no-port"})

	var e *errorstack.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, []any{"config", "address"}, e.Entries[0].Path)
}

// TestConnect_RefusedByTheService covers the path where the Service will not take
// the connection — here, a second one under a name already attached. Connect has
// dialed by then, closes what it dialed, and hands back nothing.
func TestConnect_RefusedByTheService(t *testing.T) {
	address := startTemporal(t)
	svc, _ := servicetest.New(t)

	_, err := Connect(t.Context(), svc, ConfigClient{Name: "orders", Address: address})
	require.NoError(t, err)

	c, err := Connect(t.Context(), svc, ConfigClient{Name: "orders", Address: address})
	assert.Nil(t, c)
	assert.ErrorContains(t, err, "Must be unique")
}
