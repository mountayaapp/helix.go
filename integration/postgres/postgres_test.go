package postgres

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"
	"github.com/mountaya/helix.go/service/servicetest"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// server is the PostgreSQL server the package's tests share. Starting one takes
// seconds, so it is started once, by the first test that needs it, and each test
// gets a database of its own on it, which keeps the tests independent of each
// other and of -count.
var server struct {
	once      sync.Once
	container *pgcontainer.PostgresContainer
	address   string
	err       error
}

// databases numbers the databases testDatabase creates.
var databases atomic.Int64

// TestMain terminates the shared server once every test has run.
func TestMain(m *testing.M) {
	code := m.Run()

	if server.container != nil {
		_ = testcontainers.TerminateContainer(server.container)
	}

	os.Exit(code)
}

// startServer starts a PostgreSQL container and returns it with its
// "host:port" address. opts are applied after the defaults.
func startServer(ctx context.Context, opts ...testcontainers.ContainerCustomizer) (*pgcontainer.PostgresContainer, string, error) {
	opts = append([]testcontainers.ContainerCustomizer{
		pgcontainer.WithDatabase("helix"),
		pgcontainer.WithUsername("helix"),
		pgcontainer.WithPassword("helix"),
		pgcontainer.BasicWaitStrategies(),
	}, opts...)

	container, err := pgcontainer.Run(ctx, "postgres:18-alpine", opts...)
	if err != nil {
		return container, "", err
	}

	host, err := container.Host(ctx)
	if err != nil {
		return container, "", err
	}

	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return container, "", err
	}

	return container, net.JoinHostPort(host, port.Port()), nil
}

// testDatabase creates a database of its own on the shared server and returns a
// Config pointing at it.
//
// Call it *before* servicetest.New, and do not touch the container afterwards.
// testcontainers-go instruments its own Docker API calls with otelhttp against
// the global tracer provider, which servicetest installs; starting the container
// while the Recorder is live puts several dozen "POST /v1.53/containers/.../exec"
// spans in the middle of what the test records.
func testDatabase(tb testing.TB) Config {
	tb.Helper()

	server.once.Do(func() {
		server.container, server.address, server.err = startServer(context.Background())
	})
	require.NoError(tb, server.err)

	name := fmt.Sprintf("test_%d", databases.Add(1))
	err := admin(func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "CREATE DATABASE "+name)
		return err
	})
	require.NoError(tb, err)

	return Config{
		Address:  server.address,
		Database: name,
		User:     "helix",
		Password: "helix",
	}
}

// admin hands fn a connection to the shared server's own database, and closes it
// once fn returns. The connection is not instrumented, so nothing it does shows
// up in what a test records.
func admin(fn func(ctx context.Context, conn *pgx.Conn) error) error {
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, "postgres://helix:helix@"+server.address+"/helix?sslmode=disable")
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()

	return fn(ctx, conn)
}

// codeOf returns the extensions.code of err's first entry, or "" when err is not
// an *errorstack.Error.
func codeOf(err error) string {
	e, ok := errors.AsType[*errorstack.Error](err)
	if !ok || len(e.Entries) == 0 {
		return ""
	}

	code, _ := e.Entries[0].Extensions["code"].(string)

	return code
}

// closedAddress returns the address of a port nothing listens on.
func closedAddress(tb testing.TB) string {
	tb.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(tb, err)

	address := listener.Addr().String()
	require.NoError(tb, listener.Close())

	return address
}

func TestConnect(t *testing.T) {
	cfg := testDatabase(t)
	svc, _ := servicetest.New(t)

	db, err := Connect(t.Context(), svc, cfg)
	require.NoError(t, err)

	var one int
	require.NoError(t, db.QueryRow(t.Context(), "SELECT 1").Scan(&one))
	assert.Equal(t, 1, one)
}

// TestConnect_InvalidConfigDialsNothing pins that a Config that does not
// validate — TLS settings included — is reported before anything is built or
// dialed.
func TestConnect_InvalidConfigDialsNothing(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	var accepted atomic.Int64
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

	svc, _ := servicetest.New(t)

	_, err = Connect(t.Context(), svc, Config{
		Address:  listener.Addr().String(),
		Database: "helix",
		User:     "helix",
		Password: "helix",
		TLS: integration.ConfigTLS{
			Enabled:    true,
			RootCAPEMs: [][]byte{[]byte("not a certificate")},
		},
	})
	require.Error(t, err)
	assert.Equal(t, errorstack.CodeValidationFailed, codeOf(err))

	_, err = Connect(t.Context(), svc, Config{Address: listener.Addr().String()})
	require.Error(t, err)
	assert.Equal(t, errorstack.CodeValidationFailed, codeOf(err))

	assert.Zero(t, accepted.Load(), "an invalid Config must not reach the network")
}

// TestConnect_Unreachable pins that Connect fails when the database cannot be
// reached, rather than returning a pool that fails on first use, and that the
// failure is reported as the database being unavailable rather than as the
// Config being invalid.
func TestConnect_Unreachable(t *testing.T) {
	svc, _ := servicetest.New(t)

	_, err := Connect(t.Context(), svc, Config{
		Address:        closedAddress(t),
		Database:       "helix",
		User:           "helix",
		Password:       "helix",
		ConnectTimeout: 5 * time.Second,
	})
	require.Error(t, err)
	assert.Equal(t, errorstack.CodeServiceUnavailable, codeOf(err))

	// Nothing was attached: a failed Connect leaves the Service as it was.
	assert.NoError(t, svc.CheckHealth(t.Context()))
}

// TestConnect_WrongPassword pins that a database that rejects the credentials
// fails Connect too, and is not retried on another connection attempt.
func TestConnect_WrongPassword(t *testing.T) {
	cfg := testDatabase(t)
	cfg.Password = "wrong"

	svc, _ := servicetest.New(t)

	_, err := Connect(t.Context(), svc, cfg)
	require.Error(t, err)
	assert.Equal(t, errorstack.CodeServiceUnavailable, codeOf(err))
}

// TestConnect_AttachFailureClosesThePool is the regression test for a pool, and
// its metrics, leaked when the Service refuses the connection — here, for a name
// already in use. The refused pool's connection must be closed: a pool left open
// would keep it, and its health check, for the life of the process.
func TestConnect_AttachFailureClosesThePool(t *testing.T) {
	cfg := testDatabase(t)
	svc, _ := servicetest.New(t)

	_, err := Connect(t.Context(), svc, cfg)
	require.NoError(t, err)

	duplicate := cfg
	duplicate.ApplicationName = "helix-duplicate"

	_, err = Connect(t.Context(), svc, duplicate)
	require.Error(t, err)
	assert.Equal(t, errorstack.CodeValidationFailed, codeOf(err))

	assert.Eventually(t, func() bool {
		var count int
		err := admin(func(ctx context.Context, conn *pgx.Conn) error {
			return conn.QueryRow(ctx,
				"SELECT count(*) FROM pg_stat_activity WHERE application_name = $1", "helix-duplicate",
			).Scan(&count)
		})

		return err == nil && count == 0
	}, 10*time.Second, 50*time.Millisecond, "the refused pool's connection is still open")
}

// TestConnect_TLSAgainstPlaintextServer is the regression test for the plaintext
// fallback: a server that does not offer TLS must fail Connect when TLS is
// enabled. The driver's default sslmode used to carry on in plaintext.
func TestConnect_TLSAgainstPlaintextServer(t *testing.T) {
	cfg := testDatabase(t)
	cfg.TLS = integration.ConfigTLS{Enabled: true, InsecureSkipVerify: true}

	svc, _ := servicetest.New(t)

	_, err := Connect(t.Context(), svc, cfg)
	require.Error(t, err)
	assert.Equal(t, errorstack.CodeServiceUnavailable, codeOf(err))
}

// TestConnect_TLS runs against a server that offers TLS, and pins that a
// connection with TLS enabled is encrypted and verified — with the server name
// taken from Address — and that one with TLS disabled is plaintext.
//
// Before ServerName defaulted to the host, the verifying handshake could not
// succeed at all, and the plaintext fallback took over without a word: the first
// case connected unencrypted, and the second connected anyway.
func TestConnect_TLS(t *testing.T) {
	dir := t.TempDir()
	caPEM := writeServerCertificates(t, dir)

	container, address, err := startServer(t.Context(),
		pgcontainer.WithSSLCert(
			filepath.Join(dir, "ca.pem"), filepath.Join(dir, "server.pem"), filepath.Join(dir, "server.key"),
		),
		testcontainers.WithCmdArgs(
			"-c", "ssl=on",
			"-c", "ssl_cert_file=/tmp/testcontainers-go/postgres/server.cert",
			"-c", "ssl_key_file=/tmp/testcontainers-go/postgres/server.key",
		),
	)
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err)

	// localhost rather than whatever the container reports, so that the server
	// name defaulted from Address is one the certificate is valid for.
	_, port, err := net.SplitHostPort(address)
	require.NoError(t, err)

	cfg := Config{
		Address:  net.JoinHostPort("localhost", port),
		Database: "helix",
		User:     "helix",
		Password: "helix",
	}

	svc, _ := servicetest.New(t)

	encrypted := func(db PostgreSQL) bool {
		var ssl bool
		require.NoError(t, db.QueryRow(t.Context(), "SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()").Scan(&ssl))

		return ssl
	}

	t.Run("enabled TLS is verified against the host", func(t *testing.T) {
		verified := cfg
		verified.Name = "verified"
		verified.TLS = integration.ConfigTLS{Enabled: true, RootCAPEMs: [][]byte{caPEM}}

		db, err := Connect(t.Context(), svc, verified)
		require.NoError(t, err)
		assert.True(t, encrypted(db))
	})

	t.Run("a certificate that does not verify fails rather than falling back", func(t *testing.T) {
		untrusted := cfg
		untrusted.Name = "untrusted"
		untrusted.TLS = integration.ConfigTLS{Enabled: true, RootCAPEMs: [][]byte{newCA(t).pem}}

		_, err := Connect(t.Context(), svc, untrusted)
		require.Error(t, err)
		assert.Equal(t, errorstack.CodeServiceUnavailable, codeOf(err))
	})

	t.Run("disabled TLS is plaintext", func(t *testing.T) {
		plaintext := cfg
		plaintext.Name = "plaintext"

		db, err := Connect(t.Context(), svc, plaintext)
		require.NoError(t, err)
		assert.False(t, encrypted(db))
	})
}

// testCA is a certificate authority made up for a test.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

// newCA makes up a certificate authority.
func newCA(tb testing.TB) testCA {
	tb.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(tb, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "helix test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(tb, err)

	cert, err := x509.ParseCertificate(der)
	require.NoError(tb, err)

	return testCA{
		cert: cert,
		key:  key,
		pem:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}
}

// writeServerCertificates writes a certificate authority to dir as ca.pem, and a
// server certificate it signed for localhost as server.pem and server.key, and
// returns the authority's PEM.
func writeServerCertificates(tb testing.TB, dir string) []byte {
	tb.Helper()

	ca := newCA(tb)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(tb, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	require.NoError(tb, err)

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(tb, err)

	write := func(name string, block *pem.Block) {
		require.NoError(tb, os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(block), 0o600))
	}

	require.NoError(tb, os.WriteFile(filepath.Join(dir, "ca.pem"), ca.pem, 0o600))
	write("server.pem", &pem.Block{Type: "CERTIFICATE", Bytes: der})
	write("server.key", &pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	return ca.pem
}
