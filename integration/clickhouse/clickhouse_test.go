package clickhouse

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service"
	"github.com/mountayaapp/helix.go/service/servicetest"

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

	pingErr error
	stats   driver.Stats
	closed  int
}

func (c *fakeConn) Ping(context.Context) error { return c.pingErr }
func (c *fakeConn) Stats() driver.Stats        { return c.stats }

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

	conn, err := attach(t.Context(), svc, "primary", testInstrument(t, svc, "primary"), client)
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

	conn, err := attach(t.Context(), svc, "primary", testInstrument(t, svc, "primary"), client)
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

	conn, err := attach(t.Context(), svc, "primary", testInstrument(t, svc, "primary"), client)
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
	m.register(testInstrument(t, svc, "primary"), &fakeConn{})

	assert.Empty(t, gaugeValues(t, rec.Metrics(), "clickhouse.pool.connections", "primary", attrKeyPoolState))
}
