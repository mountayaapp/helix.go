package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mountaya/helix.go/integration"
	"github.com/mountaya/helix.go/service/servicetest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestPrepareFrom(t *testing.T) {
	t.Run("an implicit prepare is named a prepare", func(t *testing.T) {
		assert.Equal(t, "Prepare", prepareFrom(t.Context()))
	})

	// The label a facade method set for its own span must not leak into the
	// statement-cache prepare pgx runs just before it, or that round trip would
	// show up as a second span claiming to be the query.
	t.Run("a query's label does not name the prepare it triggers", func(t *testing.T) {
		assert.Equal(t, "Prepare", prepareFrom(withOperation(t.Context(), "Query")))
	})

	t.Run("an explicit prepare keeps the facade's name", func(t *testing.T) {
		ctx := withPrepare(t.Context(), "Transaction / Prepare")

		assert.Equal(t, "Transaction / Prepare", prepareFrom(ctx))
	})
}

func TestOperationFrom(t *testing.T) {
	t.Run("returns the fallback when the facade labelled nothing", func(t *testing.T) {
		assert.Equal(t, "Query", operationFrom(t.Context(), "Query"))
	})

	t.Run("returns the label the facade set", func(t *testing.T) {
		ctx := withOperation(t.Context(), "Transaction / Exec")

		assert.Equal(t, "Transaction / Exec", operationFrom(ctx, "Query"))
	})

	t.Run("the innermost label wins", func(t *testing.T) {
		ctx := withOperation(withOperation(t.Context(), "Exec"), "Transaction / Exec")

		assert.Equal(t, "Transaction / Exec", operationFrom(ctx, "Query"))
	})
}

// recordingTracer is a tracer whose spans and measurements are recorded in
// memory, so the hooks can be driven directly.
type recordingTracer struct {
	*tracer
	spans  *tracetest.SpanRecorder
	reader *sdkmetric.ManualReader
}

// newRecordingTracer builds a recordingTracer.
func newRecordingTracer(tb testing.TB) *recordingTracer {
	tb.Helper()

	spans := tracetest.NewSpanRecorder()
	reader := sdkmetric.NewManualReader()

	inst, err := integration.NewInstrument(integration.InstrumentConfig{
		Identifier:     identifier,
		Humanized:      humanized,
		Name:           "primary",
		Attributes:     []attribute.KeyValue{integration.Key(identifier, "database").String("helix")},
		TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)),
		MeterProvider:  sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
	})
	require.NoError(tb, err)

	return &recordingTracer{tracer: &tracer{inst: inst}, spans: spans, reader: reader}
}

// operations returns the postgres.operation.duration data points recorded so
// far, keyed by "<operation> <outcome>", with the number of measurements in each.
func (r *recordingTracer) operations(tb testing.TB) map[string]uint64 {
	tb.Helper()

	var rm metricdata.ResourceMetrics
	require.NoError(tb, r.reader.Collect(context.Background(), &rm))

	out := map[string]uint64{}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "postgres.operation.duration" {
				continue
			}

			histogram, ok := m.Data.(metricdata.Histogram[float64])
			require.True(tb, ok, "postgres.operation.duration must be a float64 histogram")

			for _, point := range histogram.DataPoints {
				operation, _ := point.Attributes.Value("postgres.operation")
				outcome, _ := point.Attributes.Value("postgres.outcome")
				out[operation.AsString()+" "+outcome.AsString()] += point.Count
			}
		}
	}

	return out
}

// TestTracer_MeasuresOperations is the regression test for an operation histogram
// that was registered and never recorded into: every span the tracer opens is an
// Operation, and ending it is what records the measurement.
func TestTracer_MeasuresOperations(t *testing.T) {
	tr := newRecordingTracer(t)

	ctx := tr.TraceAcquireStart(withOperation(t.Context(), "Exec"), nil, pgxpool.TraceAcquireStartData{})
	tr.TraceAcquireEnd(ctx, nil, pgxpool.TraceAcquireEndData{})

	ctx = tr.TraceQueryStart(withOperation(t.Context(), "Exec"), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 1")})

	ctx = tr.TraceQueryStart(withOperation(t.Context(), "Exec"), nil, pgx.TraceQueryStartData{SQL: "SELECT nope"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("column does not exist")})

	assert.Equal(t, map[string]uint64{
		"Pool / Acquire ok": 1,
		"Exec ok":           1,
		"Exec error":        1,
	}, tr.operations(t))
}

func TestTracer_Query(t *testing.T) {
	t.Run("a statement carries its SQL and the rows it returned", func(t *testing.T) {
		tr := newRecordingTracer(t)

		ctx := tr.TraceQueryStart(withOperation(t.Context(), "Query"), nil, pgx.TraceQueryStartData{SQL: "SELECT id FROM things"})
		tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 3")})

		ended := tr.spans.Ended()
		require.Len(t, ended, 1)
		assert.Equal(t, "PostgreSQL: Query", ended[0].Name())
		assert.Equal(t, []attribute.KeyValue{
			attribute.String("postgres.name", "primary"),
			attribute.String("postgres.database", "helix"),
			attribute.String("postgres.query", "SELECT id FROM things"),
			attribute.Int64("postgres.rows", 3),
		}, ended[0].Attributes())
	})

	t.Run("a failed statement is an error and reports no rows", func(t *testing.T) {
		tr := newRecordingTracer(t)

		ctx := tr.TraceQueryStart(t.Context(), nil, pgx.TraceQueryStartData{SQL: "SELECT nope"})
		tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("column does not exist")})

		ended := tr.spans.Ended()
		require.Len(t, ended, 1)
		assert.Equal(t, codes.Error, ended[0].Status().Code)
		for _, attr := range ended[0].Attributes() {
			assert.NotEqual(t, attrKeyRows, attr.Key, "a failed statement has no row count to report")
		}
	})
}

func TestTracer_BatchAndCopy(t *testing.T) {
	t.Run("a batch is one span carrying its size", func(t *testing.T) {
		tr := newRecordingTracer(t)

		batch := &pgx.Batch{}
		batch.Queue("INSERT INTO things (id) VALUES ($1)", 1)
		batch.Queue("INSERT INTO things (id) VALUES ($1)", 2)

		ctx := tr.TraceBatchStart(withOperation(t.Context(), "Batch"), nil, pgx.TraceBatchStartData{Batch: batch})
		tr.TraceBatchQuery(ctx, nil, pgx.TraceBatchQueryData{SQL: "INSERT INTO things (id) VALUES ($1)"})
		tr.TraceBatchQuery(ctx, nil, pgx.TraceBatchQueryData{SQL: "INSERT INTO things (id) VALUES ($1)"})
		tr.TraceBatchEnd(ctx, nil, pgx.TraceBatchEndData{})

		ended := tr.spans.Ended()
		require.Len(t, ended, 1, "a batch is one round trip and must be one span")
		assert.Equal(t, "PostgreSQL: Batch", ended[0].Name())
		assert.Equal(t, []attribute.KeyValue{
			attribute.String("postgres.name", "primary"),
			attribute.String("postgres.database", "helix"),
			attribute.Int("postgres.batch.size", 2),
		}, ended[0].Attributes())
		assert.Empty(t, ended[0].Events(), "a statement that succeeded leaves no trace of its own")
	})

	t.Run("a failed statement is recorded on the batch's span", func(t *testing.T) {
		tr := newRecordingTracer(t)

		batch := &pgx.Batch{}
		batch.Queue("INSERT INTO things (id) VALUES ($1)", 1)

		ctx := tr.TraceBatchStart(t.Context(), nil, pgx.TraceBatchStartData{Batch: batch})
		tr.TraceBatchQuery(ctx, nil, pgx.TraceBatchQueryData{Err: errors.New("duplicate key")})
		tr.TraceBatchEnd(ctx, nil, pgx.TraceBatchEndData{})

		ended := tr.spans.Ended()
		require.Len(t, ended, 1)
		assert.Equal(t, codes.Error, ended[0].Status().Code)
		assert.Equal(t, map[string]uint64{"Batch error": 1}, tr.operations(t))
	})

	t.Run("a copy carries its table, columns and row count", func(t *testing.T) {
		tr := newRecordingTracer(t)

		ctx := tr.TraceCopyFromStart(withOperation(t.Context(), "Copy From"), nil, pgx.TraceCopyFromStartData{
			TableName:   pgx.Identifier{"public", "things"},
			ColumnNames: []string{"id", "name"},
		})
		tr.TraceCopyFromEnd(ctx, nil, pgx.TraceCopyFromEndData{
			CommandTag: pgconn.NewCommandTag("COPY 3"),
		})

		ended := tr.spans.Ended()
		require.Len(t, ended, 1)
		assert.Equal(t, "PostgreSQL: Copy From", ended[0].Name())
		assert.Equal(t, []attribute.KeyValue{
			attribute.String("postgres.name", "primary"),
			attribute.String("postgres.database", "helix"),
			attribute.String("postgres.table", `"public"."things"`),
			attribute.StringSlice("postgres.columns", []string{"id", "name"}),
			attribute.Int64("postgres.rows", 3),
		}, ended[0].Attributes())
	})
}

// TestTracer_Untraced pins that a context marked as a probe's produces neither a
// span nor a measurement, from any hook, including the nested ones a pool's
// Ping goes through when it has to open a connection.
func TestTracer_Untraced(t *testing.T) {
	tr := newRecordingTracer(t)
	probe := untraced(t.Context())

	ctx := tr.TraceAcquireStart(probe, nil, pgxpool.TraceAcquireStartData{})
	connectCtx := tr.TraceConnectStart(ctx, pgx.TraceConnectStartData{})
	tr.TraceConnectEnd(connectCtx, pgx.TraceConnectEndData{})
	tr.TraceAcquireEnd(ctx, nil, pgxpool.TraceAcquireEndData{})

	ctx = tr.TraceQueryStart(probe, nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("unreachable")})

	ctx = tr.TracePrepareStart(probe, nil, pgx.TracePrepareStartData{SQL: "SELECT 1"})
	tr.TracePrepareEnd(ctx, nil, pgx.TracePrepareEndData{})

	ctx = tr.TraceBatchStart(probe, nil, pgx.TraceBatchStartData{Batch: &pgx.Batch{}})
	tr.TraceBatchQuery(ctx, nil, pgx.TraceBatchQueryData{Err: errors.New("unreachable")})
	tr.TraceBatchEnd(ctx, nil, pgx.TraceBatchEndData{})

	ctx = tr.TraceCopyFromStart(probe, nil, pgx.TraceCopyFromStartData{TableName: pgx.Identifier{"things"}})
	tr.TraceCopyFromEnd(ctx, nil, pgx.TraceCopyFromEndData{})

	assert.Empty(t, tr.spans.Started())
	assert.Empty(t, tr.operations(t))
}

// TestTracer_NotRecording pins that a span the sampler dropped is still measured:
// the histogram describes every operation, not the sampled ones.
func TestTracer_NotRecording(t *testing.T) {
	reader := sdkmetric.NewManualReader()

	inst, err := integration.NewInstrument(integration.InstrumentConfig{
		Identifier:     identifier,
		Humanized:      humanized,
		TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample())),
		MeterProvider:  sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
	})
	require.NoError(t, err)

	tr := &recordingTracer{tracer: &tracer{inst: inst}, reader: reader}

	ctx := tr.TraceCopyFromStart(t.Context(), nil, pgx.TraceCopyFromStartData{
		TableName:   pgx.Identifier{"things"},
		ColumnNames: []string{"id"},
	})
	tr.TraceCopyFromEnd(ctx, nil, pgx.TraceCopyFromEndData{CommandTag: pgconn.NewCommandTag("COPY 1")})

	assert.Equal(t, map[string]uint64{"Copy From ok": 1}, tr.operations(t))
}

// TestTracer_EndWithoutStart pins that a hook handed a context no Start ever
// touched does nothing rather than panicking. pgx calls the End hooks from defer,
// including on paths that failed before the Start hook ran.
func TestTracer_EndWithoutStart(t *testing.T) {
	tr := newRecordingTracer(t)

	tr.TraceQueryEnd(t.Context(), nil, pgx.TraceQueryEndData{})
	tr.TraceBatchQuery(t.Context(), nil, pgx.TraceBatchQueryData{Err: errors.New("unreachable")})
	tr.TraceBatchEnd(t.Context(), nil, pgx.TraceBatchEndData{})
	tr.TraceCopyFromEnd(t.Context(), nil, pgx.TraceCopyFromEndData{})
	tr.TracePrepareEnd(t.Context(), nil, pgx.TracePrepareEndData{})
	tr.TraceConnectEnd(t.Context(), pgx.TraceConnectEndData{})
	tr.TraceAcquireEnd(t.Context(), nil, pgxpool.TraceAcquireEndData{})

	assert.Empty(t, tr.spans.Ended())
	assert.Empty(t, tr.operations(t))
}

func TestDurable(t *testing.T) {
	testcases := []struct {
		tag      string
		txStatus byte
		want     bool
	}{
		{tag: "COMMIT", txStatus: 'I', want: true},
		{tag: "COMMIT", txStatus: 0, want: true},
		{tag: "ROLLBACK", txStatus: 'I', want: false},
		{tag: "RELEASE", txStatus: 'T', want: false},
		{tag: "BEGIN", txStatus: 'T', want: false},
		{tag: "INSERT 0 1", txStatus: 'I', want: true},
		{tag: "INSERT 0 0", txStatus: 'I', want: false},
		{tag: "INSERT 0 1", txStatus: 'T', want: false},
		{tag: "INSERT 0 1", txStatus: 'E', want: false},
		{tag: "INSERT 0 1", txStatus: 0, want: false},
		{tag: "UPDATE 3", txStatus: 'I', want: true},
		{tag: "UPDATE 0", txStatus: 'I', want: false},
		{tag: "DELETE 1", txStatus: 'I', want: true},
		{tag: "MERGE 2", txStatus: 'I', want: true},
		{tag: "COPY 5", txStatus: 'I', want: true},
		{tag: "SELECT 1", txStatus: 'I', want: false},
		{tag: "CREATE TABLE", txStatus: 'I', want: false},
		{tag: "", txStatus: 'I', want: false},
	}

	for _, tc := range testcases {
		t.Run(fmt.Sprintf("%s in %q", tc.tag, tc.txStatus), func(t *testing.T) {
			assert.Equal(t, tc.want, durable(pgconn.NewCommandTag(tc.tag), tc.txStatus))
		})
	}
}

// requestKeyIdentifier is the type of the key a test labels the context of a
// statement with, to check that OnWrite is handed that context.
type requestKeyIdentifier struct{}

// requestKey is the context key a test's label is stored under.
var requestKey requestKeyIdentifier

// writeRecorder records the label of every context OnWrite is called with.
type writeRecorder struct {
	labels []string
}

// onWrite is the Config.OnWrite of the recorder.
func (w *writeRecorder) onWrite(ctx context.Context) {
	label, _ := ctx.Value(requestKey).(string)
	w.labels = append(w.labels, label)
}

// TestTracer_OnWrite drives the End hooks directly, with no connection to ask for
// a transaction status: only a COMMIT is then known to have been kept.
func TestTracer_OnWrite(t *testing.T) {
	newTracer := func(t *testing.T) (*recordingTracer, *writeRecorder) {
		t.Helper()

		tr := newRecordingTracer(t)
		writes := &writeRecorder{}
		tr.onWrite = writes.onWrite

		return tr, writes
	}

	labelled := func(t *testing.T) context.Context {
		return context.WithValue(t.Context(), requestKey, t.Name())
	}

	commit := func(tr *recordingTracer, ctx context.Context, data pgx.TraceQueryEndData) {
		ctx = tr.TraceQueryStart(withOperation(ctx, "Transaction / Commit"), nil, pgx.TraceQueryStartData{SQL: "commit"})
		tr.TraceQueryEnd(ctx, nil, data)
	}

	t.Run("a commit is reported once, with the statement's context", func(t *testing.T) {
		tr, writes := newTracer(t)

		commit(tr, labelled(t), pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("COMMIT")})

		assert.Equal(t, []string{t.Name()}, writes.labels)
	})

	t.Run("a commit the server turned into a rollback is not reported", func(t *testing.T) {
		tr, writes := newTracer(t)

		commit(tr, labelled(t), pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("ROLLBACK")})

		assert.Empty(t, writes.labels)
	})

	t.Run("a commit that failed is not reported", func(t *testing.T) {
		tr, writes := newTracer(t)

		commit(tr, labelled(t), pgx.TraceQueryEndData{
			CommandTag: pgconn.NewCommandTag("COMMIT"),
			Err:        errors.New("deferred constraint violated"),
		})

		assert.Empty(t, writes.labels)
	})

	t.Run("a write the connection cannot place is not reported", func(t *testing.T) {
		tr, writes := newTracer(t)

		ctx := tr.TraceQueryStart(labelled(t), nil, pgx.TraceQueryStartData{SQL: "INSERT INTO things (id) VALUES (1)"})
		tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("INSERT 0 1")})

		ctx = tr.TraceCopyFromStart(labelled(t), nil, pgx.TraceCopyFromStartData{TableName: pgx.Identifier{"things"}})
		tr.TraceCopyFromEnd(ctx, nil, pgx.TraceCopyFromEndData{CommandTag: pgconn.NewCommandTag("COPY 3")})

		assert.Empty(t, writes.labels)
	})

	t.Run("a batch committing a transaction is reported once, however often it ends", func(t *testing.T) {
		tr, writes := newTracer(t)

		ctx := tr.TraceBatchStart(labelled(t), nil, pgx.TraceBatchStartData{Batch: &pgx.Batch{}})
		tr.TraceBatchQuery(ctx, nil, pgx.TraceBatchQueryData{CommandTag: pgconn.NewCommandTag("BEGIN")})
		tr.TraceBatchQuery(ctx, nil, pgx.TraceBatchQueryData{CommandTag: pgconn.NewCommandTag("INSERT 0 1")})
		tr.TraceBatchQuery(ctx, nil, pgx.TraceBatchQueryData{CommandTag: pgconn.NewCommandTag("COMMIT")})
		tr.TraceBatchQuery(ctx, nil, pgx.TraceBatchQueryData{Err: errors.New("duplicate key")})
		tr.TraceBatchEnd(ctx, nil, pgx.TraceBatchEndData{Err: errors.New("duplicate key")})
		tr.TraceBatchEnd(ctx, nil, pgx.TraceBatchEndData{Err: errors.New("duplicate key")})

		assert.Equal(t, []string{t.Name()}, writes.labels)
	})

	t.Run("a batch that committed nothing is not reported", func(t *testing.T) {
		tr, writes := newTracer(t)

		ctx := tr.TraceBatchStart(labelled(t), nil, pgx.TraceBatchStartData{Batch: &pgx.Batch{}})
		tr.TraceBatchQuery(ctx, nil, pgx.TraceBatchQueryData{CommandTag: pgconn.NewCommandTag("INSERT 0 1")})
		tr.TraceBatchEnd(ctx, nil, pgx.TraceBatchEndData{})

		assert.Empty(t, writes.labels)
	})

	t.Run("nothing is noted without OnWrite", func(t *testing.T) {
		tr := newRecordingTracer(t)

		ctx := tr.TraceBatchStart(t.Context(), nil, pgx.TraceBatchStartData{Batch: &pgx.Batch{}})
		_, noted := ctx.Value(batchWritesKey).(*batchWrites)
		assert.False(t, noted)

		tr.TraceBatchQuery(ctx, nil, pgx.TraceBatchQueryData{CommandTag: pgconn.NewCommandTag("COMMIT")})
		tr.TraceBatchEnd(ctx, nil, pgx.TraceBatchEndData{})
		commit(tr, t.Context(), pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("COMMIT")})
	})
}

func TestBatchWrites(t *testing.T) {
	statement := func(tag string) pgx.TraceBatchQueryData {
		return pgx.TraceBatchQueryData{CommandTag: pgconn.NewCommandTag(tag)}
	}

	failed := pgx.TraceBatchQueryData{Err: errors.New("duplicate key")}

	testcases := []struct {
		name       string
		statements []pgx.TraceBatchQueryData
		err        error
		txStatus   byte
		want       bool
	}{
		{name: "rows written outside of a transaction", statements: []pgx.TraceBatchQueryData{statement("INSERT 0 1"), statement("UPDATE 2")}, txStatus: 'I', want: true},
		{name: "nothing written", statements: []pgx.TraceBatchQueryData{statement("SELECT 1"), statement("UPDATE 0")}, txStatus: 'I', want: false},
		{name: "rows written inside a transaction", statements: []pgx.TraceBatchQueryData{statement("INSERT 0 1")}, txStatus: 'T', want: false},
		{name: "a statement failed", statements: []pgx.TraceBatchQueryData{statement("INSERT 0 1"), failed}, txStatus: 'I', want: false},
		{name: "the batch failed", statements: []pgx.TraceBatchQueryData{statement("INSERT 0 1")}, err: errors.New("connection reset"), txStatus: 'I', want: false},
		{name: "rows rolled back", statements: []pgx.TraceBatchQueryData{statement("BEGIN"), statement("INSERT 0 1"), statement("ROLLBACK")}, txStatus: 'I', want: false},
		{name: "rows written after a rollback", statements: []pgx.TraceBatchQueryData{statement("ROLLBACK"), statement("INSERT 0 1")}, txStatus: 'I', want: true},
		{name: "a commit, whatever follows", statements: []pgx.TraceBatchQueryData{statement("BEGIN"), statement("INSERT 0 1"), statement("COMMIT"), failed}, err: errors.New("duplicate key"), txStatus: 'I', want: true},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			writes := &batchWrites{}
			for _, data := range tc.statements {
				writes.record(data)
			}

			assert.Equal(t, tc.want, writes.durable(tc.err, tc.txStatus))
		})
	}
}

// TestConnect_OnWrite runs OnWrite against a server: what is reported is what the
// server kept, and the transaction status it reports is what places a write
// inside or outside of a transaction.
func TestConnect_OnWrite(t *testing.T) {
	cfg := testDatabase(t)

	writes := &writeRecorder{}
	cfg.OnWrite = writes.onWrite

	svc, _ := servicetest.New(t)

	db, err := Connect(t.Context(), svc, cfg)
	require.NoError(t, err)

	_, err = db.Exec(t.Context(), `
		CREATE TABLE things (id int PRIMARY KEY, name text);
		CREATE TABLE pairs (id int, CONSTRAINT pairs_id_key UNIQUE (id) DEFERRABLE INITIALLY DEFERRED);
	`)
	require.NoError(t, err)
	require.Empty(t, writes.labels, "creating a table writes no rows")

	// observe runs fn with a context labelled with the subtest's name, and returns
	// the labels OnWrite was called with meanwhile.
	observe := func(t *testing.T, fn func(ctx context.Context)) []string {
		t.Helper()

		writes.labels = nil
		fn(context.WithValue(t.Context(), requestKey, t.Name()))

		return writes.labels
	}

	// exists reports whether a row of things has id.
	exists := func(t *testing.T, id int) bool {
		t.Helper()

		var found bool
		require.NoError(t, db.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM things WHERE id = $1)", id).Scan(&found))

		return found
	}

	t.Run("a write outside of a transaction is reported once, with its context", func(t *testing.T) {
		got := observe(t, func(ctx context.Context) {
			_, err := db.Exec(ctx, "INSERT INTO things (id, name) VALUES ($1, $2)", 1, "one")
			require.NoError(t, err)
		})

		assert.Equal(t, []string{t.Name()}, got)
	})

	t.Run("a write returning rows is reported once its row is read", func(t *testing.T) {
		got := observe(t, func(ctx context.Context) {
			var id int
			require.NoError(t, db.QueryRow(ctx, "INSERT INTO things (id, name) VALUES ($1, $2) RETURNING id", 2, "two").Scan(&id))
		})

		assert.Equal(t, []string{t.Name()}, got)
	})

	t.Run("reads, writes of no row and failed writes are not reported", func(t *testing.T) {
		got := observe(t, func(ctx context.Context) {
			var name string
			require.NoError(t, db.QueryRow(ctx, "SELECT name FROM things WHERE id = $1", 1).Scan(&name))

			tag, err := db.Exec(ctx, "UPDATE things SET name = $1 WHERE id = $2", "none", 404)
			require.NoError(t, err)
			require.Zero(t, tag.RowsAffected())

			_, err = db.Exec(ctx, "INSERT INTO things (id, name) VALUES ($1, $2)", 1, "duplicate")
			require.Error(t, err)
		})

		assert.Empty(t, got)
	})

	t.Run("a committed transaction is reported once, at its commit", func(t *testing.T) {
		got := observe(t, func(ctx context.Context) {
			tx, err := db.BeginTx(ctx, pgx.TxOptions{})
			require.NoError(t, err)

			_, err = tx.Exec(ctx, "INSERT INTO things (id, name) VALUES ($1, $2)", 10, "ten")
			require.NoError(t, err)

			_, err = tx.Exec(ctx, "UPDATE things SET name = $1 WHERE id = $2", "TEN", 10)
			require.NoError(t, err)
			require.Empty(t, writes.labels, "nothing is kept before the commit")

			require.NoError(t, tx.Commit(ctx))
		})

		assert.Equal(t, []string{t.Name()}, got)
		assert.True(t, exists(t, 10))
	})

	t.Run("a rolled back transaction is never reported", func(t *testing.T) {
		got := observe(t, func(ctx context.Context) {
			tx, err := db.BeginTx(ctx, pgx.TxOptions{})
			require.NoError(t, err)

			_, err = tx.Exec(ctx, "INSERT INTO things (id, name) VALUES ($1, $2)", 11, "eleven")
			require.NoError(t, err)

			require.NoError(t, tx.Rollback(ctx))
		})

		assert.Empty(t, got)
		assert.False(t, exists(t, 11))
	})

	t.Run("a nested commit is only reported with its outer transaction's", func(t *testing.T) {
		got := observe(t, func(ctx context.Context) {
			tx, err := db.BeginTx(ctx, pgx.TxOptions{})
			require.NoError(t, err)

			nested, err := tx.Begin(ctx)
			require.NoError(t, err)

			_, err = nested.Exec(ctx, "INSERT INTO things (id, name) VALUES ($1, $2)", 12, "twelve")
			require.NoError(t, err)
			require.NoError(t, nested.Commit(ctx))
			require.Empty(t, writes.labels, "releasing a savepoint keeps nothing")

			require.NoError(t, tx.Rollback(ctx))
		})

		assert.Empty(t, got)
		assert.False(t, exists(t, 12))
	})

	t.Run("a commit the server turns into a rollback is not reported", func(t *testing.T) {
		got := observe(t, func(ctx context.Context) {
			tx, err := db.BeginTx(ctx, pgx.TxOptions{})
			require.NoError(t, err)

			_, err = tx.Exec(ctx, "INSERT INTO things (id, name) VALUES ($1, $2)", 13, "thirteen")
			require.NoError(t, err)

			_, err = tx.Exec(ctx, "INSERT INTO things (id, name) VALUES ($1, $2)", 1, "duplicate")
			require.Error(t, err)

			require.ErrorIs(t, tx.Commit(ctx), pgx.ErrTxCommitRollback)
		})

		assert.Empty(t, got)
		assert.False(t, exists(t, 13))
	})

	t.Run("a commit that fails is not reported", func(t *testing.T) {
		got := observe(t, func(ctx context.Context) {
			tx, err := db.BeginTx(ctx, pgx.TxOptions{})
			require.NoError(t, err)

			_, err = tx.Exec(ctx, "INSERT INTO pairs (id) VALUES (1), (1)")
			require.NoError(t, err, "the unique constraint is only checked at commit")

			require.Error(t, tx.Commit(ctx))
		})

		assert.Empty(t, got)
	})

	t.Run("a read-only transaction that commits is reported", func(t *testing.T) {
		got := observe(t, func(ctx context.Context) {
			tx, err := db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
			require.NoError(t, err)

			var count int
			require.NoError(t, tx.QueryRow(ctx, "SELECT count(*) FROM things").Scan(&count))
			require.NoError(t, tx.Commit(ctx))
		})

		assert.Equal(t, []string{t.Name()}, got)
	})

	t.Run("a batch outside of a transaction is reported once", func(t *testing.T) {
		got := observe(t, func(ctx context.Context) {
			batch := &pgx.Batch{}
			batch.Queue("INSERT INTO things (id, name) VALUES ($1, $2)", 20, "twenty")
			batch.Queue("INSERT INTO things (id, name) VALUES ($1, $2)", 21, "twenty-one")
			batch.Queue("SELECT count(*) FROM things")

			require.NoError(t, db.SendBatch(ctx, batch).Close())
		})

		assert.Equal(t, []string{t.Name()}, got)
		assert.True(t, exists(t, 21))
	})

	t.Run("a batch whose statement fails is not reported", func(t *testing.T) {
		got := observe(t, func(ctx context.Context) {
			batch := &pgx.Batch{}
			batch.Queue("INSERT INTO things (id, name) VALUES ($1, $2)", 22, "twenty-two")
			batch.Queue("INSERT INTO things (id, name) VALUES ($1, $2)", 1, "duplicate")

			require.Error(t, db.SendBatch(ctx, batch).Close())
		})

		assert.Empty(t, got)
		assert.False(t, exists(t, 22), "the batch's implicit transaction was rolled back")
	})

	t.Run("a batch that rolls back its writes is not reported", func(t *testing.T) {
		got := observe(t, func(ctx context.Context) {
			batch := &pgx.Batch{}
			batch.Queue("BEGIN")
			batch.Queue("INSERT INTO things (id, name) VALUES ($1, $2)", 23, "twenty-three")
			batch.Queue("ROLLBACK")

			require.NoError(t, db.SendBatch(ctx, batch).Close())
		})

		assert.Empty(t, got)
		assert.False(t, exists(t, 23))
	})

	t.Run("a batch inside a transaction is reported at its commit", func(t *testing.T) {
		got := observe(t, func(ctx context.Context) {
			tx, err := db.BeginTx(ctx, pgx.TxOptions{})
			require.NoError(t, err)

			batch := &pgx.Batch{}
			batch.Queue("INSERT INTO things (id, name) VALUES ($1, $2)", 24, "twenty-four")
			require.NoError(t, tx.SendBatch(ctx, batch).Close())
			require.Empty(t, writes.labels, "nothing is kept before the commit")

			require.NoError(t, tx.Commit(ctx))
		})

		assert.Equal(t, []string{t.Name()}, got)
	})

	t.Run("a copy outside of a transaction is reported once", func(t *testing.T) {
		got := observe(t, func(ctx context.Context) {
			copied, err := db.CopyFrom(ctx, pgx.Identifier{"things"}, []string{"id", "name"}, pgx.CopyFromRows([][]any{
				{30, "thirty"},
				{31, "thirty-one"},
			}))
			require.NoError(t, err)
			require.Equal(t, int64(2), copied)
		})

		assert.Equal(t, []string{t.Name()}, got)
	})

	t.Run("a copy inside a transaction that rolls back is never reported", func(t *testing.T) {
		got := observe(t, func(ctx context.Context) {
			tx, err := db.BeginTx(ctx, pgx.TxOptions{})
			require.NoError(t, err)

			_, err = tx.CopyFrom(ctx, pgx.Identifier{"things"}, []string{"id", "name"}, pgx.CopyFromRows([][]any{
				{32, "thirty-two"},
			}))
			require.NoError(t, err)

			require.NoError(t, tx.Rollback(ctx))
		})

		assert.Empty(t, got)
		assert.False(t, exists(t, 32))
	})
}
