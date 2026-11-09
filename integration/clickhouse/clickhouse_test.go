package clickhouse

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service"
	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// fakeConn stands in for the driver's connection where a test needs to see what
// Connect does with it. It embeds the interface, so any method it does not
// override panics: a test that reaches one is testing something else.
type fakeConn struct {
	driver.Conn

	mu      sync.Mutex
	pingErr error
	pings   int
	stats   driver.Stats
	closed  int
}

func (c *fakeConn) Ping(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.pings++

	return c.pingErr
}

func (c *fakeConn) Stats() driver.Stats { return c.stats }

// failPings sets what every later ping answers.
func (c *fakeConn) failPings(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.pingErr = err
}

// pinged returns how many pings were sent.
func (c *fakeConn) pinged() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.pings
}

func (c *fakeConn) Close() error {
	c.closed++
	return nil
}

// stubDependency is a dependency that does nothing, attached to take a name.
type stubDependency struct{}

func (stubDependency) Close(context.Context) error       { return nil }
func (stubDependency) CheckHealth(context.Context) error { return nil }

// testInstrument returns the Instrument Connect would build for a connection
// called name, on svc's providers.
func testInstrument(tb testing.TB, svc *service.Service, name string) *integration.Instrument {
	tb.Helper()

	inst, err := integration.NewInstrument(integration.InstrumentConfig{
		Identifier: identifier,
		Humanized:  humanized,
		Name:       name,
		Attributes: []attribute.KeyValue{
			integration.Key(identifier, "database").String("default"),
		},
		TracerProvider: service.TracerProvider(svc),
		MeterProvider:  service.MeterProvider(svc),
		Propagator:     service.Propagator(svc),
	})
	require.NoError(tb, err)

	return inst
}

// metricNamed returns the metric called name, and whether there is one.
func metricNamed(metrics []metricdata.Metrics, name string) (metricdata.Metrics, bool) {
	for _, m := range metrics {
		if m.Name == name {
			return m, true
		}
	}

	return metricdata.Metrics{}, false
}

// gaugeValues returns the data points of the int64 gauge called name, keyed by
// the value of the attribute key on each, or by "" for a point without it. Every
// point must carry the connection's name, which is how two pools are told apart.
func gaugeValues(tb testing.TB, metrics []metricdata.Metrics, name, connection string, key attribute.Key) map[string]int64 {
	tb.Helper()

	m, ok := metricNamed(metrics, name)
	if !ok {
		return nil
	}

	gauge, ok := m.Data.(metricdata.Gauge[int64])
	require.True(tb, ok, "metric %q is a %T, not an int64 gauge", name, m.Data)

	out := make(map[string]int64, len(gauge.DataPoints))
	for _, point := range gauge.DataPoints {
		got, _ := point.Attributes.Value(integration.Key(identifier, "name"))
		assert.Equal(tb, connection, got.AsString())

		value, _ := point.Attributes.Value(key)
		out[value.AsString()] = point.Value
	}

	return out
}

func TestConnect_UnreachableServer(t *testing.T) {
	svc, rec := servicetest.New(t)

	db, err := Connect(t.Context(), svc, Config{
		Address:     "127.0.0.1:1",
		DialTimeout: 5 * time.Second,
	})
	require.Error(t, err)
	assert.Nil(t, db)

	// Unreachable is a runtime condition, not a configuration mistake.
	entries := errorstack.EntriesOf(err)
	require.Len(t, entries, 1)
	assert.Equal(t, errorstack.CodeServiceUnavailable, entries[0].Extensions["code"])
	assert.NotEmpty(t, entries[0].Extensions["reason"])

	// Nothing was attached under the name, and no pool is reported.
	require.NoError(t, service.Attach(svc, identifier, stubDependency{}))
	_, ok := metricNamed(rec.Metrics(), "clickhouse.pool.connections")
	assert.False(t, ok)
}

// TestConnect_InvalidConfigFailsBeforeDialing pins the order: a Config that
// cannot be used is reported as such, without anything being dialed. The address
// is unreachable, so reaching the dial would have reported SERVICE_UNAVAILABLE.
func TestConnect_InvalidConfigFailsBeforeDialing(t *testing.T) {
	testcases := []struct {
		name string
		cfg  Config
		path []any
	}{
		{
			name: "sanitize",
			cfg:  Config{Address: "127.0.0.1:1", Compression: "gzip"},
			path: []any{"config", "compression"},
		},
		{
			name: "TLS conversion",
			cfg: Config{
				Address: "127.0.0.1:1",
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
				},
			},
			path: []any{"config", "tls"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := servicetest.New(t)

			db, err := Connect(t.Context(), svc, tc.cfg)
			assert.Nil(t, db)

			entries := errorstack.EntriesOf(err)
			require.Len(t, entries, 1)
			assert.Equal(t, errorstack.CodeValidationFailed, entries[0].Extensions["code"])
			assert.Equal(t, tc.path, entries[0].Path)
		})
	}
}

func TestAttach_PingFailureClosesTheConnection(t *testing.T) {
	svc, rec := servicetest.New(t)
	client := &fakeConn{pingErr: errors.New("dial tcp: connection refused")}

	conn, err := attach(t.Context(), svc, "primary", testInstrument(t, svc, "primary"), client, false)
	require.Error(t, err)
	assert.Nil(t, conn)
	assert.Equal(t, 1, client.closed)

	entries := errorstack.EntriesOf(err)
	require.Len(t, entries, 1)
	assert.Equal(t, "Failed to connect", entries[0].Message)
	assert.Equal(t, errorstack.CodeServiceUnavailable, entries[0].Extensions["code"])
	assert.Equal(t, "Dial tcp: connection refused", entries[0].Extensions["reason"])
	assert.ErrorIs(t, err, client.pingErr)

	require.NoError(t, service.Attach(svc, "primary", stubDependency{}))
	_, ok := metricNamed(rec.Metrics(), "clickhouse.pool.connections")
	assert.False(t, ok)
}

// TestAttach_DuplicateNameLeavesNoMetrics is the regression test for the pool
// metrics being registered before Attach: a connection refused for a duplicate
// name went on reporting its pool under the name of the one attached first.
func TestAttach_DuplicateNameLeavesNoMetrics(t *testing.T) {
	svc, rec := servicetest.New(t)
	require.NoError(t, service.Attach(svc, "primary", stubDependency{}))

	client := &fakeConn{stats: driver.Stats{Open: 1, Idle: 1, MaxOpenConns: 3}}

	conn, err := attach(t.Context(), svc, "primary", testInstrument(t, svc, "primary"), client, false)
	require.Error(t, err)
	assert.Nil(t, conn)
	assert.Equal(t, 1, client.closed)

	metrics := rec.Metrics()
	_, ok := metricNamed(metrics, "clickhouse.pool.connections")
	assert.False(t, ok)
	_, ok = metricNamed(metrics, "clickhouse.pool.connections.max")
	assert.False(t, ok)
}

func TestAttach_ReportsThePoolUntilClosed(t *testing.T) {
	svc, rec := servicetest.New(t)
	client := &fakeConn{stats: driver.Stats{Open: 2, Idle: 3, MaxOpenConns: 10, MaxIdleConns: 5}}

	conn, err := attach(t.Context(), svc, "primary", testInstrument(t, svc, "primary"), client, false)
	require.NoError(t, err)

	metrics := rec.Metrics()
	assert.Equal(t, map[string]int64{"idle": 3, "acquired": 2},
		gaugeValues(t, metrics, "clickhouse.pool.connections", "primary", attrKeyPoolState))
	assert.Equal(t, map[string]int64{"": 10},
		gaugeValues(t, metrics, "clickhouse.pool.connections.max", "primary", attrKeyPoolState))

	dep := dependency{conn: conn}
	require.NoError(t, dep.CheckHealth(t.Context()))
	require.NoError(t, dep.Close(t.Context()))
	assert.Equal(t, 1, client.closed)

	// Unregistered before the connection was closed: the gauges report nothing
	// for a pool that is gone.
	metrics = rec.Metrics()
	assert.Empty(t, gaugeValues(t, metrics, "clickhouse.pool.connections", "primary", attrKeyPoolState))
	assert.Empty(t, gaugeValues(t, metrics, "clickhouse.pool.connections.max", "primary", attrKeyPoolState))
}

func TestDependency_CheckHealth(t *testing.T) {
	client := &fakeConn{pingErr: errors.New("connection reset by peer")}
	dep := dependency{conn: &connection{client: client, pool: &poolMetrics{}}}

	err := dep.CheckHealth(t.Context())

	entries := errorstack.EntriesOf(err)
	require.Len(t, entries, 1)
	assert.Equal(t, errorstack.CodeServiceUnavailable, entries[0].Extensions["code"])
	assert.Equal(t, "Connection reset by peer", entries[0].Extensions["reason"])
}

// TestPoolMetrics_RegisterAfterUnregister covers Close running on the Service's
// goroutine before Connect has finished on the caller's: the registration that
// arrives late must not start a callback nothing will stop.
func TestPoolMetrics_RegisterAfterUnregister(t *testing.T) {
	svc, rec := servicetest.New(t)

	m := &poolMetrics{}
	m.unregister()
	m.register(testInstrument(t, svc, "primary"), &fakeConn{}, &atomic.Bool{})

	assert.Empty(t, gaugeValues(t, rec.Metrics(), "clickhouse.pool.connections", "primary", attrKeyPoolState))
}

// TestConnect_OptionalUnreachableServer pins Config.Optional end to end: a server
// that cannot be reached neither fails Connect nor makes the Service unready, and
// shows on the reachable gauge instead.
func TestConnect_OptionalUnreachableServer(t *testing.T) {
	svc, rec := servicetest.New(t)

	db, err := Connect(t.Context(), svc, Config{
		Address:     "127.0.0.1:1",
		DialTimeout: time.Second,
		Optional:    true,
	})
	require.NoError(t, err)
	require.NotNil(t, db)

	// Readiness reports on the Service's health, which the connection no longer
	// gates.
	require.NoError(t, svc.CheckHealth(t.Context()))
	assert.Equal(t, map[string]int64{"": 0},
		gaugeValues(t, rec.Metrics(), "clickhouse.reachable", identifier, attrKeyPoolState))

	// A statement fails, as one to a server that is down does.
	require.Error(t, db.Exec(t.Context(), "SELECT 1"))
}

// TestAttach_OptionalFollowsTheServer pins an optional connection over a server
// that comes and goes: attached while it cannot be reached, healthy throughout
// without a ping, and followed by its prober on the reachable gauge, which no
// health check updates.
func TestAttach_OptionalFollowsTheServer(t *testing.T) {
	probeEvery(t, 5*time.Millisecond)

	svc, rec := servicetest.New(t)
	client := &fakeConn{pingErr: errors.New("dial tcp: connection refused")}

	conn, err := attach(t.Context(), svc, "primary", testInstrument(t, svc, "primary"), client, true)
	require.NoError(t, err)
	require.NotNil(t, conn)
	assert.Zero(t, client.closed)
	assert.Equal(t, map[string]int64{"": 0},
		gaugeValues(t, rec.Metrics(), "clickhouse.reachable", "primary", attrKeyPoolState))

	dep := dependency{conn: conn}
	t.Cleanup(func() { _ = dep.Close(context.Background()) })

	pings := client.pinged()
	require.NoError(t, dep.CheckHealth(t.Context()))
	assert.Equal(t, pings, client.pinged(), "the health check of an optional connection sent a ping")

	client.failPings(nil)
	assert.Eventually(t, func() bool {
		return gaugeValues(t, rec.Metrics(), "clickhouse.reachable", "primary", attrKeyPoolState)[""] == 1
	}, 2*time.Second, 5*time.Millisecond, "the gauge never followed the server back")

	client.failPings(errors.New("connection reset by peer"))
	assert.Eventually(t, func() bool {
		return gaugeValues(t, rec.Metrics(), "clickhouse.reachable", "primary", attrKeyPoolState)[""] == 0
	}, 2*time.Second, 5*time.Millisecond, "the gauge never followed the server away")

	// Closed, the connection pings no more.
	require.NoError(t, dep.Close(t.Context()))
	pings = client.pinged()
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, pings, client.pinged(), "the prober pinged a closed connection")
}

// probeEvery has every connection the test attaches ping its server every
// interval, until the test ends.
func probeEvery(t *testing.T, interval time.Duration) {
	t.Helper()

	before := reachabilityInterval
	reachabilityInterval = interval
	t.Cleanup(func() { reachabilityInterval = before })
}

// TestAttach_OptionalStillRefusesARefusal pins the limit of Config.Optional: a
// server that answered the ping with an exception refuses the configuration, and
// Connect fails as it does without the option.
func TestAttach_OptionalStillRefusesARefusal(t *testing.T) {
	svc, _ := servicetest.New(t)
	client := &fakeConn{pingErr: &clickhouse.Exception{Code: 516, Name: "DB::Exception", Message: "default: Authentication failed"}}

	conn, err := attach(t.Context(), svc, "primary", testInstrument(t, svc, "primary"), client, true)
	require.Error(t, err)
	assert.Nil(t, conn)
	assert.Equal(t, 1, client.closed)
}

// TestDependency_CheckHealthIsReportedOnTheGauge pins the gauge for a connection
// that is not optional: the health check that fails reports it unreachable, as
// well as unhealthy.
func TestDependency_CheckHealthIsReportedOnTheGauge(t *testing.T) {
	svc, rec := servicetest.New(t)
	client := &fakeConn{}

	conn, err := attach(t.Context(), svc, "primary", testInstrument(t, svc, "primary"), client, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"": 1},
		gaugeValues(t, rec.Metrics(), "clickhouse.reachable", "primary", attrKeyPoolState))

	client.pingErr = errors.New("connection reset by peer")
	require.Error(t, dependency{conn: conn}.CheckHealth(t.Context()))
	assert.Equal(t, map[string]int64{"": 0},
		gaugeValues(t, rec.Metrics(), "clickhouse.reachable", "primary", attrKeyPoolState))
}

// silentServer returns the address of a server that accepts every connection and
// never answers: a hung server, or a middlebox swallowing the traffic.
func silentServer(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	return listener.Addr().String()
}

// TestCheckHealth_OptionalAnswersAtOnceAgainstASilentServer pins the readiness of a
// service whose optional connection's server hangs: its health check answers at
// once, without the ping that would hold a readiness probe for as long as a dial
// may last, and the server still shows unreachable.
func TestCheckHealth_OptionalAnswersAtOnceAgainstASilentServer(t *testing.T) {
	svc, rec := servicetest.New(t)

	db, err := Connect(t.Context(), svc, Config{Address: silentServer(t), DialTimeout: 300 * time.Millisecond, Optional: true})
	require.NoError(t, err)
	require.NotNil(t, db)

	probe, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	started := time.Now()
	require.NoError(t, svc.CheckHealth(probe))
	assert.Less(t, time.Since(started), 100*time.Millisecond, "the readiness check waited on the silent server")

	assert.Equal(t, map[string]int64{"": 0},
		gaugeValues(t, rec.Metrics(), "clickhouse.reachable", identifier, attrKeyPoolState))
}

// TestConnect_TLSFailureIsARefusal pins what an optional connection does not wait
// out: a TLS handshake that fails, against a server that does not speak TLS or one
// whose certificate the configuration does not trust, fails Connect as it does
// without the option, and Connect hands back no connection at all.
func TestConnect_TLSFailureIsARefusal(t *testing.T) {
	plain, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = plain.Close() })
	go func() {
		for {
			conn, err := plain.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("HTTP/1.0 400 Bad Request\r\n\r\n"))
			_ = conn.Close()
		}
	}()

	untrusted := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(untrusted.Close)

	// A server that requires a client certificate the configuration does not
	// present: over TLS 1.3 the handshake completes on this end, and the server's
	// alert arrives on the first read, as a *net.OpError, not a tls.AlertError.
	clientCertificate := httptest.NewUnstartedServer(http.NotFoundHandler())
	clientCertificate.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert, MinVersion: tls.VersionTLS13}
	clientCertificate.StartTLS()
	t.Cleanup(clientCertificate.Close)

	for name, tc := range map[string]struct {
		address string
		tls     integration.ConfigTLS
	}{
		"a server that does not speak TLS":            {address: plain.Addr().String(), tls: integration.ConfigTLS{Enabled: true}},
		"a certificate nothing here trusts":           {address: untrusted.Listener.Addr().String(), tls: integration.ConfigTLS{Enabled: true}},
		"a server that requires a client certificate": {address: clientCertificate.Listener.Addr().String(), tls: integration.ConfigTLS{Enabled: true, InsecureSkipVerify: true}},
	} {
		t.Run(name, func(t *testing.T) {
			svc, _ := servicetest.New(t)

			db, err := Connect(t.Context(), svc, Config{
				Address:     tc.address,
				DialTimeout: 2 * time.Second,
				Optional:    true,
				TLS:         tc.tls,
			})
			require.Error(t, err)
			assert.True(t, db == nil, "Connect handed back a connection holding nothing")
		})
	}
}

// TestTolerated_TLSAlertOverTCP pins how an alert received over TCP is told from an
// outage: crypto/tls reports it as a *net.OpError wrapping the alert, which is a
// refusal; any other operation error, a dial or a read that timed out, is not.
func TestTolerated_TLSAlertOverTCP(t *testing.T) {
	alert := &net.OpError{Op: "remote error", Net: "tcp", Err: errors.New("tls: certificate required")}
	assert.False(t, tolerated(fmt.Errorf("handshake: %w", alert), true))
	assert.False(t, tolerated(&net.OpError{Op: "local error", Err: errors.New("tls: bad record MAC")}, true))

	assert.True(t, tolerated(&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")}, true))
	assert.True(t, tolerated(&net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}, true))
	assert.True(t, tolerated(&net.OpError{Op: "remote error", Err: errors.New("connection reset by peer")}, true))
}

// TestConnect_DNSFailureIsTolerated pins the outage an optional connection does
// wait out: a name that does not resolve, since its record can come back with the
// server it names.
func TestConnect_DNSFailureIsTolerated(t *testing.T) {
	svc, _ := servicetest.New(t)

	db, err := Connect(t.Context(), svc, Config{Address: "no-such-host.invalid:9000", DialTimeout: 2 * time.Second, Optional: true})
	require.NoError(t, err)
	assert.NotNil(t, db)
}

// TestConnect_FailureHandsBackNothing pins that a Connect that fails hands back a
// nil ClickHouse, not an interface holding a nil connection, which compares
// unequal to nil.
func TestConnect_FailureHandsBackNothing(t *testing.T) {
	svc, _ := servicetest.New(t)

	db, err := Connect(t.Context(), svc, Config{Address: "127.0.0.1:1", DialTimeout: time.Second})
	require.Error(t, err)
	assert.True(t, db == nil, "Connect handed back %#v", db)
}

// TestBatchColumns pins what the batch options name, as NewBatchInsert reads them:
// the columns given, or a struct's tags in their order, none by default, and a
// validation error for a column named twice or a value that is no struct.
func TestBatchColumns(t *testing.T) {
	columns, err := BatchColumns()
	require.NoError(t, err)
	assert.Empty(t, columns)

	columns, err = BatchColumns(WithColumns("id", "name"))
	require.NoError(t, err)
	assert.Equal(t, []string{"id", "name"}, columns)

	columns, err = BatchColumns(WithColumnsOf(&serverRow{}))
	require.NoError(t, err)
	assert.Equal(t, []string{"id", "name"}, columns)

	_, err = BatchColumns(WithColumns("id", "id"))
	assert.Equal(t, errorstack.CodeValidationFailed, errorstack.EntriesOf(err)[0].Extensions["code"])

	_, err = BatchColumns(WithColumnsOf("not a struct"))
	assert.Equal(t, errorstack.CodeValidationFailed, errorstack.EntriesOf(err)[0].Extensions["code"])

	assert.Equal(t, "INSERT INTO `t` (`id`, `na``me`)", (&batchOptions{columns: []string{"id", "na`me"}}).insertStatement("`t`"))
}
