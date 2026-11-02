package postgres

import (
	"testing"

	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// TestTelemetryGolden pins what this integration emits, so that changing how a
// span is produced cannot silently change what is produced. It records span
// names, emitting scope, kind and attribute keys - never values, timings or IDs,
// which differ on every run.
//
// A real database rather than a stub: what this package emits depends on which
// statements actually reach the server and whether they fail, and a stub would
// let the golden pass while describing something the integration never does.
//
// A diff here is not automatically a failure: it is a change to the library's
// observable output, and it has to be an intentional one. Update the golden in
// the same commit that changes the instrumentation, never separately.
func TestTelemetryGolden(t *testing.T) {
	cfg := testDatabase(t)
	cfg.Name = "golden"

	svc, rec := servicetest.New(t)

	db, err := Connect(t.Context(), svc, cfg)
	require.NoError(t, err)

	_, err = db.Exec(t.Context(), "CREATE TABLE things (id int PRIMARY KEY)")
	require.NoError(t, err)

	rows, err := db.Query(t.Context(), "SELECT id FROM things")
	require.NoError(t, err)
	rows.Close()

	var id int
	require.ErrorIs(t, db.QueryRow(t.Context(), "SELECT id FROM things").Scan(&id), pgx.ErrNoRows)

	_, err = db.Exec(t.Context(), "INSERT INTO missing (id) VALUES (1)")
	require.Error(t, err)

	batch := &pgx.Batch{}
	batch.Queue("INSERT INTO things (id) VALUES ($1)", 1)
	batch.Queue("INSERT INTO things (id) VALUES ($1)", 2)
	require.NoError(t, db.SendBatch(t.Context(), batch).Close())

	copied, err := db.CopyFrom(t.Context(), pgx.Identifier{"things"}, []string{"id"}, pgx.CopyFromRows([][]any{{3}, {4}}))
	require.NoError(t, err)
	assert.Equal(t, int64(2), copied)

	tx, err := db.BeginTx(t.Context(), pgx.TxOptions{})
	require.NoError(t, err)

	_, err = tx.Exec(t.Context(), "INSERT INTO things (id) VALUES ($1)", 5)
	require.NoError(t, err)

	_, err = tx.Prepare(t.Context(), "byID", "SELECT id FROM things WHERE id = $1")
	require.NoError(t, err)

	txRows, err := tx.Query(t.Context(), "SELECT id FROM things")
	require.NoError(t, err)
	txRows.Close()

	require.NoError(t, tx.QueryRow(t.Context(), "byID", 5).Scan(&id))

	txBatch := &pgx.Batch{}
	txBatch.Queue("DELETE FROM things WHERE id = $1", 1)
	require.NoError(t, tx.SendBatch(t.Context(), txBatch).Close())

	_, err = tx.CopyFrom(t.Context(), pgx.Identifier{"things"}, []string{"id"}, pgx.CopyFromRows([][]any{{6}}))
	require.NoError(t, err)

	sub, err := tx.Begin(t.Context())
	require.NoError(t, err)
	require.NoError(t, sub.Rollback(t.Context()))
	require.NoError(t, tx.Commit(t.Context()))

	listener, err := db.Listen(t.Context(), "things")
	require.NoError(t, err)

	_, err = db.Exec(t.Context(), "NOTIFY things")
	require.NoError(t, err)

	_, err = listener.WaitForNotification(t.Context())
	require.NoError(t, err)
	require.NoError(t, listener.Close(t.Context()))

	// Probes leave no trace: neither Connect's ping nor the readiness check.
	require.NoError(t, svc.CheckHealth(t.Context()))

	var got []string
	for _, s := range servicetest.Summarize(rec.Spans()) {
		got = append(got, s.String())
	}

	// Connect and Pool / Acquire spans come from underneath the facade: opening a
	// connection and taking one out of the pool happen beneath every call, and a
	// pool with nothing free blocks in Acquire. There is no Connect for the pool
	// here only because Connect's own ping opened the pool's one connection, and
	// probes are not traced; the Connect span is the Listener's own connection.
	//
	// The bare Prepare spans are pgx filling its per-connection caches before a
	// parameterised query or a copy - a real round trip on a miss. They are named
	// Prepare rather than after the call that triggered them, and they are absent
	// where the cache already held the statement.
	//
	// Every statement that succeeded carries postgres.rows, as the server's
	// command tag reports it; the failing Exec does not. Every span carries
	// postgres.name, so two pools are told apart in a trace the way they already
	// are in health output.
	want := []string{
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Pool / Acquire | postgres.database,postgres.name",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Exec | postgres.database,postgres.name,postgres.query,postgres.rows",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Pool / Acquire | postgres.database,postgres.name",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Prepare | postgres.already_prepared,postgres.database,postgres.name,postgres.query,postgres.statement",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Query | postgres.database,postgres.name,postgres.query,postgres.rows",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Pool / Acquire | postgres.database,postgres.name",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: QueryRow | postgres.database,postgres.name,postgres.query,postgres.rows",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Pool / Acquire | postgres.database,postgres.name",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Exec | postgres.database,postgres.name,postgres.query",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Pool / Acquire | postgres.database,postgres.name",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Batch | postgres.batch.size,postgres.database,postgres.name",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Pool / Acquire | postgres.database,postgres.name",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Prepare | postgres.already_prepared,postgres.database,postgres.name,postgres.query,postgres.statement",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Copy From | postgres.columns,postgres.database,postgres.name,postgres.rows,postgres.table",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Pool / Acquire | postgres.database,postgres.name",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Transaction / Begin | postgres.database,postgres.name,postgres.query,postgres.rows",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Transaction / Exec | postgres.database,postgres.name,postgres.query,postgres.rows",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Transaction / Prepare | postgres.already_prepared,postgres.database,postgres.name,postgres.query,postgres.statement",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Transaction / Query | postgres.database,postgres.name,postgres.query,postgres.rows",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Transaction / QueryRow | postgres.database,postgres.name,postgres.query,postgres.rows",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Transaction / Batch | postgres.batch.size,postgres.database,postgres.name",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Transaction / Copy From | postgres.columns,postgres.database,postgres.name,postgres.rows,postgres.table",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Transaction / Begin | postgres.database,postgres.name,postgres.query,postgres.rows",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Transaction / Rollback | postgres.database,postgres.name,postgres.query,postgres.rows",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Transaction / Commit | postgres.database,postgres.name,postgres.query,postgres.rows",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Connect | postgres.database,postgres.name",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Listen | postgres.database,postgres.name,postgres.query,postgres.rows",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Pool / Acquire | postgres.database,postgres.name",
		"github.com/mountayaapp/helix.go | client | PostgreSQL: Exec | postgres.database,postgres.name,postgres.query,postgres.rows",
	}

	assert.Equal(t, want, got)

	servicetest.AssertConvention(t, rec.Spans())

	// The one statement that failed is the one span marked as an error.
	var failed []string
	for _, span := range rec.Spans() {
		if span.Status().Code == codes.Error {
			failed = append(failed, span.Name())
		}
	}
	assert.Equal(t, []string{"PostgreSQL: Exec"}, failed)

	assertGoldenMetrics(t, rec.Metrics())
}

// assertGoldenMetrics pins the metrics TestTelemetryGolden's workload records:
// an operation measurement for every span, under the span's own operation name
// and its outcome, and the pool's instruments.
func assertGoldenMetrics(t *testing.T, metrics []metricdata.Metrics) {
	t.Helper()

	histogram, ok := metricNamed(t, metrics, "postgres.operation.duration").Data.(metricdata.Histogram[float64])
	require.True(t, ok)

	operations := map[string]uint64{}
	for _, point := range histogram.DataPoints {
		name, _ := point.Attributes.Value("postgres.name")
		assert.Equal(t, "golden", name.AsString())

		operation, _ := point.Attributes.Value("postgres.operation")
		outcome, _ := point.Attributes.Value("postgres.outcome")
		operations[operation.AsString()+" "+outcome.AsString()] += point.Count
	}

	want := map[string]uint64{
		"Pool / Acquire ok":          8,
		"Connect ok":                 1,
		"Exec ok":                    2,
		"Exec error":                 1,
		"Prepare ok":                 2,
		"Query ok":                   1,
		"QueryRow ok":                1,
		"Batch ok":                   1,
		"Copy From ok":               1,
		"Transaction / Begin ok":     2,
		"Transaction / Exec ok":      1,
		"Transaction / Prepare ok":   1,
		"Transaction / Query ok":     1,
		"Transaction / QueryRow ok":  1,
		"Transaction / Batch ok":     1,
		"Transaction / Copy From ok": 1,
		"Transaction / Rollback ok":  1,
		"Transaction / Commit ok":    1,
		"Listen ok":                  1,
	}
	assert.Equal(t, want, operations)

	for _, name := range []string{
		"postgres.pool.connections",
		"postgres.pool.connections.max",
		"postgres.pool.acquire.waits",
		"postgres.pool.acquire.wait.duration",
	} {
		metricNamed(t, metrics, name)
	}
}
