package clickhouse

import (
	"testing"

	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// TestTelemetryGolden pins what this integration emits, so that changing how a
// span is produced cannot silently change what is produced. It records span
// names, emitting scope, kind and attribute keys - never values, timings or IDs,
// which differ on every run.
//
// A diff here is not automatically a failure: it is a change to the library's
// observable output, and it has to be an intentional one. Update the golden in
// the same commit that changes the instrumentation, never separately.
func TestTelemetryGolden(t *testing.T) {
	cfg := serverConfig(t, "golden")
	cfg.MaxOpenConns = 4

	svc, rec := servicetest.New(t)

	db, err := Connect(t.Context(), svc, cfg)
	require.NoError(t, err)

	// The table is created through the integration itself: Exec is the entry
	// point for a statement that returns no rows.
	require.NoError(t, db.Exec(t.Context(),
		"CREATE TABLE things (id UInt32, name String) ENGINE = MergeTree ORDER BY id"))

	sent, err := db.NewBatchInsert(t.Context(), "things")
	require.NoError(t, err)
	require.NoError(t, sent.AppendStruct(t.Context(), &serverRow{ID: 1, Name: "one"}))
	require.NoError(t, sent.Send(t.Context()))

	aborted, err := db.NewBatchInsert(t.Context(), "things")
	require.NoError(t, err)
	require.NoError(t, aborted.Abort(t.Context()))

	require.NoError(t, db.AsyncInsertStruct(t.Context(), "things", serverRow{ID: 2, Name: "two"}))

	var selected []serverRow
	require.NoError(t, db.Select(t.Context(), &selected, "SELECT id, name FROM things WHERE id = ?", 1))

	rows, err := db.Query(t.Context(), "SELECT id, name FROM things WHERE id = ?", 1)
	require.NoError(t, err)
	for rows.Next() {
		var r serverRow
		require.NoError(t, rows.ScanStruct(&r))
	}
	require.NoError(t, rows.Close())

	var name string
	require.NoError(t, db.QueryRow(t.Context(), "SELECT name FROM things WHERE id = ?", 1).Scan(&name))

	var got []string
	for _, s := range servicetest.Summarize(rec.Spans()) {
		got = append(got, s.String())
	}

	// The selected.* and inserted.* attributes are the server's own counters,
	// asked for with WithProfileEvents and summed as they arrive: a span says how
	// much data the statement actually touched, which is the one thing about a
	// query that a client cannot know by itself. Async Insert reports none because
	// it does not wait for the server to flush, which is the whole point of it.
	//
	// Every statement also ships its span identifiers to the server with WithSpan,
	// so a slow query can be followed out of the trace and into ClickHouse's own
	// system.opentelemetry_span_log. That creates no span here, and so is invisible
	// in this golden - it is propagation, not instrumentation.
	//
	// The statements the caller wrote carry clickhouse.query; the ones this
	// package builds from a table name carry clickhouse.table instead.
	//
	// A batch is three spans. Batch / Begin is the round trip that starts the
	// INSERT and ends with it, so its duration measures ClickHouse rather than the
	// caller. Batch stands for the whole batch and ends last, after Send or Abort:
	// it is internal, and it is not an operation, because its duration includes
	// however long the caller took to append.
	//
	// Query ends when its rows have been read, which is why it carries the
	// counters of the whole result rather than of its first block.
	want := []string{
		"github.com/mountayaapp/helix.go | client | ClickHouse: Exec | clickhouse.database,clickhouse.name,clickhouse.query",
		"github.com/mountayaapp/helix.go | client | ClickHouse: Batch / Begin | clickhouse.database,clickhouse.name,clickhouse.table",
		"github.com/mountayaapp/helix.go | client | ClickHouse: Batch / Send | clickhouse.database,clickhouse.inserted.bytes,clickhouse.inserted.rows,clickhouse.name,clickhouse.selected.bytes,clickhouse.selected.rows,clickhouse.table",
		"github.com/mountayaapp/helix.go | internal | ClickHouse: Batch | clickhouse.database,clickhouse.name,clickhouse.table",
		"github.com/mountayaapp/helix.go | client | ClickHouse: Batch / Begin | clickhouse.database,clickhouse.name,clickhouse.table",
		"github.com/mountayaapp/helix.go | client | ClickHouse: Batch / Abort | clickhouse.database,clickhouse.name,clickhouse.table",
		"github.com/mountayaapp/helix.go | internal | ClickHouse: Batch | clickhouse.database,clickhouse.name,clickhouse.table",
		"github.com/mountayaapp/helix.go | client | ClickHouse: Async Insert | clickhouse.database,clickhouse.name,clickhouse.table",
		"github.com/mountayaapp/helix.go | client | ClickHouse: Select | clickhouse.database,clickhouse.name,clickhouse.query,clickhouse.selected.bytes,clickhouse.selected.rows",
		"github.com/mountayaapp/helix.go | client | ClickHouse: Query | clickhouse.database,clickhouse.name,clickhouse.query,clickhouse.selected.bytes,clickhouse.selected.rows",
		"github.com/mountayaapp/helix.go | client | ClickHouse: QueryRow | clickhouse.database,clickhouse.name,clickhouse.query,clickhouse.selected.bytes,clickhouse.selected.rows",
	}

	assert.Equal(t, want, got)

	servicetest.AssertConvention(t, rec.Spans())

	// The instruments are pinned by name rather than merely asserted non-empty: a
	// rename is exactly the kind of change that should surface here rather than in
	// somebody's dashboard.
	metrics := rec.Metrics()

	var names []string
	for _, m := range metrics {
		names = append(names, m.Name)
	}

	assert.ElementsMatch(t, []string{
		"clickhouse.operation.duration",
		"clickhouse.pool.connections",
		"clickhouse.pool.connections.max",
	}, names)

	// Every operation is measured under its span's operation name; the Batch span
	// is not, since it measures the caller.
	duration, ok := metricNamed(metrics, "clickhouse.operation.duration")
	require.True(t, ok)

	histogram, ok := duration.Data.(metricdata.Histogram[float64])
	require.True(t, ok)

	var operations []string
	for _, point := range histogram.DataPoints {
		operation, _ := point.Attributes.Value(integration.Key(identifier, "operation"))
		outcome, _ := point.Attributes.Value(integration.Key(identifier, "outcome"))
		connection, _ := point.Attributes.Value(integration.Key(identifier, "name"))

		assert.Equal(t, "ok", outcome.AsString())
		assert.Equal(t, "golden", connection.AsString())
		operations = append(operations, operation.AsString())
	}

	assert.ElementsMatch(t, []string{
		"Exec",
		"Batch / Begin",
		"Batch / Send",
		"Batch / Abort",
		"Async Insert",
		"Select",
		"Query",
		"QueryRow",
	}, operations)

	// The pool is reported by state, idle and acquired together never exceeding
	// the maximum, which is the one configured. Which state a connection is in
	// right now is not pinned: the driver gives a connection back from its own
	// goroutine, a moment after the rows reading from it are closed.
	connections := gaugeValues(t, metrics, "clickhouse.pool.connections", "golden", attrKeyPoolState)
	require.Len(t, connections, 2)
	assert.Contains(t, connections, "idle")
	assert.Contains(t, connections, "acquired")
	assert.Positive(t, connections["idle"]+connections["acquired"])
	assert.LessOrEqual(t, connections["idle"]+connections["acquired"], int64(4))

	assert.Equal(t, map[string]int64{"": 4},
		gaugeValues(t, metrics, "clickhouse.pool.connections.max", "golden", attrKeyPoolState))
}
