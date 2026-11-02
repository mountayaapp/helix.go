package postgres

import (
	"context"
	"testing"

	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// metricNamed returns the metric called name among metrics, failing the test when
// there is none.
func metricNamed(tb testing.TB, metrics []metricdata.Metrics, name string) metricdata.Metrics {
	tb.Helper()

	for _, m := range metrics {
		if m.Name == name {
			return m
		}
	}

	tb.Fatalf("metric %q was not recorded", name)

	return metricdata.Metrics{}
}

// TestPoolMetrics pins the instruments the pool is observed through and the
// attributes each is keyed on. It needs no server: the pool connects lazily, and
// an idle, empty pool still reports its shape.
func TestPoolMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()

	inst, err := integration.NewInstrument(integration.InstrumentConfig{
		Identifier:    identifier,
		Humanized:     humanized,
		Name:          "primary",
		MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
	})
	require.NoError(t, err)

	cfg := Config{Address: closedAddress(t), Database: "helix", User: "helix", Password: "helix", MaxConns: 7}
	opts, err := poolConfig(&cfg, nil)
	require.NoError(t, err)

	pool, err := pgxpool.NewWithConfig(t.Context(), opts)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	metrics := newPoolMetrics(inst, pool)

	collect := func() []metricdata.Metrics {
		var rm metricdata.ResourceMetrics
		require.NoError(t, reader.Collect(context.Background(), &rm))

		var out []metricdata.Metrics
		for _, scope := range rm.ScopeMetrics {
			out = append(out, scope.Metrics...)
		}

		return out
	}

	got := collect()

	connections, ok := metricNamed(t, got, "postgres.pool.connections").Data.(metricdata.Gauge[int64])
	require.True(t, ok)

	states := map[string]int64{}
	for _, point := range connections.DataPoints {
		name, _ := point.Attributes.Value("postgres.name")
		assert.Equal(t, "primary", name.AsString())

		state, _ := point.Attributes.Value("postgres.pool.state")
		states[state.AsString()] = point.Value
	}
	assert.Equal(t, map[string]int64{"idle": 0, "acquired": 0, "constructing": 0}, states)

	limit, ok := metricNamed(t, got, "postgres.pool.connections.max").Data.(metricdata.Gauge[int64])
	require.True(t, ok)
	require.Len(t, limit.DataPoints, 1)
	assert.Equal(t, int64(7), limit.DataPoints[0].Value)

	_, ok = metricNamed(t, got, "postgres.pool.acquire.waits").Data.(metricdata.Sum[int64])
	assert.True(t, ok)

	_, ok = metricNamed(t, got, "postgres.pool.acquire.wait.duration").Data.(metricdata.Sum[float64])
	assert.True(t, ok)

	// Once unregistered, the pool is no longer read.
	metrics.unregister()
	for _, m := range collect() {
		assert.NotContains(t, m.Name, "postgres.pool.")
	}
}

// TestPoolMetrics_WaitDuration is the regression test for the wait gauge, which
// reported the total time of every acquire — including the ones served at once
// from an idle connection — as time spent waiting for one.
func TestPoolMetrics_WaitDuration(t *testing.T) {
	cfg := testDatabase(t)
	svc, rec := servicetest.New(t)

	db, err := Connect(t.Context(), svc, cfg)
	require.NoError(t, err)

	// Acquires served from the idle connection Connect left behind: they take
	// time, and wait for nothing.
	for range 10 {
		_, err := db.Exec(t.Context(), "SELECT 1")
		require.NoError(t, err)
	}

	sum, ok := metricNamed(t, rec.Metrics(), "postgres.pool.acquire.wait.duration").Data.(metricdata.Sum[float64])
	require.True(t, ok)
	require.Len(t, sum.DataPoints, 1)

	stat := db.(*connection).client.Stat()
	assert.Equal(t, stat.EmptyAcquireWaitTime().Seconds(), sum.DataPoints[0].Value)
	assert.NotEqual(t, stat.AcquireDuration().Seconds(), sum.DataPoints[0].Value)
}
