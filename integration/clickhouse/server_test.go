package clickhouse

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	chcontainer "github.com/testcontainers/testcontainers-go/modules/clickhouse"
	"go.opentelemetry.io/otel/codes"
)

// serverRow is the shape the server-backed tests insert, both as a batch and
// asynchronously.
type serverRow struct {
	ID   uint32 `ch:"id"`
	Name string `ch:"name"`
}

// mistypedRow carries a value the driver cannot encode into the UInt32 id column.
type mistypedRow struct {
	ID   string `ch:"id"`
	Name string `ch:"name"`
}

// serverConfig brings up a ClickHouse server for the duration of the test and
// returns a Config pointing at it, under the given connection name.
//
// A real server rather than a stub: what this package emits depends on which
// statements actually reach the server and whether they fail, and a stub would
// let a test pass while describing something the integration never does.
//
// Call it *before* servicetest.New, and do not touch the container afterwards.
// testcontainers-go instruments its own Docker API calls with otelhttp against
// the global tracer provider, which servicetest installs; starting the container
// while the Recorder is live puts several dozen Docker API spans in the middle of
// this integration's telemetry.
func serverConfig(tb testing.TB, name string) Config {
	tb.Helper()

	container, err := chcontainer.Run(tb.Context(), "clickhouse/clickhouse-server:25.3-alpine")
	testcontainers.CleanupContainer(tb, container)
	require.NoError(tb, err)

	address, err := container.ConnectionHost(tb.Context())
	require.NoError(tb, err)

	return Config{
		Name:     name,
		Address:  address,
		Database: container.DbName,
		User:     container.User,
		Password: container.Password,
	}
}

// countRows returns how many rows table holds.
func countRows(tb testing.TB, db ClickHouse, table string) uint64 {
	tb.Helper()

	var count uint64
	require.NoError(tb, db.QueryRow(tb.Context(), "SELECT count() FROM "+table).Scan(&count))

	return count
}

// TestServer runs the behaviour that depends on a real server — what reaches it
// and what it answers — against one server, each subtest on its own Service and
// its own table.
func TestServer(t *testing.T) {
	cfg := serverConfig(t, "server")

	// connect returns a connection to the server on a fresh Service, and creates
	// table for the subtest to write to.
	connect := func(t *testing.T, table string) (ClickHouse, *servicetest.Recorder) {
		t.Helper()

		svc, rec := servicetest.New(t)

		db, err := Connect(t.Context(), svc, cfg)
		require.NoError(t, err)
		require.NoError(t, svc.CheckHealth(t.Context()))

		require.NoError(t, db.Exec(t.Context(),
			"CREATE OR REPLACE TABLE "+table+" (id UInt32, name String) ENGINE = MergeTree ORDER BY id"))

		return db, rec
	}

	t.Run("batch is sent once", func(t *testing.T) {
		db, _ := connect(t, "sent_once")

		b, err := db.NewBatchInsert(t.Context(), "sent_once")
		require.NoError(t, err)
		defer b.Abort(t.Context())

		require.NoError(t, b.AppendStruct(t.Context(), &serverRow{ID: 1, Name: "one"}))
		require.NoError(t, b.AppendStruct(t.Context(), &serverRow{ID: 2, Name: "two"}))
		require.NoError(t, b.Send(t.Context()))

		assertFinished(t, b.Send(t.Context()), "Batch has already been sent")
		require.NoError(t, b.Abort(t.Context()))
		assert.Equal(t, uint64(2), countRows(t, db, "sent_once"))
	})

	t.Run("send after abort inserts nothing and says so", func(t *testing.T) {
		db, _ := connect(t, "aborted")

		b, err := db.NewBatchInsert(t.Context(), "aborted")
		require.NoError(t, err)

		require.NoError(t, b.AppendStruct(t.Context(), &serverRow{ID: 1, Name: "one"}))
		require.NoError(t, b.Abort(t.Context()))

		assertFinished(t, b.Send(t.Context()), "Batch has already been aborted")
		assert.Zero(t, countRows(t, db, "aborted"))
	})

	t.Run("failing append fails the send, on the batch span", func(t *testing.T) {
		db, rec := connect(t, "mistyped")

		b, err := db.NewBatchInsert(t.Context(), "mistyped")
		require.NoError(t, err)
		defer b.Abort(t.Context())

		require.Error(t, b.AppendStruct(t.Context(), &mistypedRow{ID: "one", Name: "one"}))
		require.Error(t, b.Send(t.Context()))
		assertFinished(t, b.Send(t.Context()), "Batch has already been sent and failed; start a new batch")
		assert.Zero(t, countRows(t, db, "mistyped"))

		spans := rec.Spans()
		assert.Equal(t, codes.Unset, spanNamed(t, spans, "ClickHouse: Batch / Begin").Status().Code)
		assert.Equal(t, codes.Error, spanNamed(t, spans, "ClickHouse: Batch / Send").Status().Code)
		assert.Equal(t, codes.Error, spanNamed(t, spans, "ClickHouse: Batch").Status().Code)
	})

	// The regression test for the driver holding on to the context the batch was
	// begun with: a batch begun in a request whose context then ended failed on
	// Send.
	t.Run("batch outlives the context it was begun with", func(t *testing.T) {
		db, _ := connect(t, "outlives")

		beginCtx, cancel := context.WithCancel(t.Context())
		b, err := db.NewBatchInsert(beginCtx, "outlives")
		require.NoError(t, err)
		cancel()

		require.NoError(t, b.AppendStruct(t.Context(), &serverRow{ID: 1, Name: "one"}))
		require.NoError(t, b.Send(t.Context()))
		assert.Equal(t, uint64(1), countRows(t, db, "outlives"))
	})

	t.Run("begin honours its context", func(t *testing.T) {
		db, _ := connect(t, "begin_cancelled")

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		b, err := db.NewBatchInsert(ctx, "begin_cancelled")
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, b)

		// The connection the begin held was given back: the pool still serves.
		assert.Zero(t, countRows(t, db, "begin_cancelled"))
	})

	t.Run("injected table names are refused before reaching the server", func(t *testing.T) {
		db, _ := connect(t, "victim")

		for _, table := range []string{
			"victim; DROP TABLE victim",
			"victim (id) SELECT 1 --",
			"`victim`",
		} {
			b, err := db.NewBatchInsert(t.Context(), table)
			assert.Nil(t, b)
			assert.Equal(t, errorstack.CodeValidationFailed, errorstack.EntriesOf(err)[0].Extensions["code"])

			err = db.AsyncInsertStruct(t.Context(), table, serverRow{ID: 1, Name: "one"})
			assert.Equal(t, errorstack.CodeValidationFailed, errorstack.EntriesOf(err)[0].Extensions["code"])
		}

		assert.Zero(t, countRows(t, db, "victim"))
	})

	t.Run("qualified table names", func(t *testing.T) {
		db, _ := connect(t, "qualified")
		table := cfg.Database + ".qualified"

		b, err := db.NewBatchInsert(t.Context(), table)
		require.NoError(t, err)
		require.NoError(t, b.AppendStruct(t.Context(), &serverRow{ID: 1, Name: "one"}))
		require.NoError(t, b.Send(t.Context()))

		require.NoError(t, db.AsyncInsertStruct(t.Context(), table, serverRow{ID: 2, Name: "two"}))

		// The async insert returns before the server flushes its buffer.
		assert.Eventually(t, func() bool {
			return countRows(t, db, "qualified") == 2
		}, 10*time.Second, 50*time.Millisecond)
	})

	t.Run("query and query row", func(t *testing.T) {
		db, rec := connect(t, "reads")
		require.NoError(t, db.Exec(t.Context(), "INSERT INTO reads VALUES (1, 'one'), (2, 'two')"))

		rows, err := db.Query(t.Context(), "SELECT id, name FROM reads ORDER BY id")
		require.NoError(t, err)

		var got []serverRow
		for rows.Next() {
			var r serverRow
			require.NoError(t, rows.ScanStruct(&r))
			got = append(got, r)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		assert.Equal(t, []serverRow{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}}, got)

		var name string
		require.NoError(t, db.QueryRow(t.Context(), "SELECT name FROM reads WHERE id = ?", 2).Scan(&name))
		assert.Equal(t, "two", name)

		// No row is an answer rather than a failure: the span says the read
		// succeeded.
		err = db.QueryRow(t.Context(), "SELECT name FROM reads WHERE id = ?", 3).Scan(&name)
		require.ErrorIs(t, err, sql.ErrNoRows)

		var queryRows []string
		for _, span := range rec.Spans() {
			if span.Name() == "ClickHouse: QueryRow" {
				assert.Equal(t, codes.Unset, span.Status().Code)
				queryRows = append(queryRows, span.Name())
			}
		}
		assert.Len(t, queryRows, 2)

		// A statement the server rejects fails its span, and is measured as such.
		require.Error(t, db.Exec(t.Context(), "SELECT * FROM missing_table"))
		assert.Equal(t, codes.Error, rec.Spans()[len(rec.Spans())-1].Status().Code)
	})

	// The regression test for queryContext overwriting a profile-events callback
	// the caller had set through clickhouse.Context.
	t.Run("caller's profile-events callback is kept", func(t *testing.T) {
		db, rec := connect(t, "profiled")
		require.NoError(t, db.Exec(t.Context(), "INSERT INTO profiled VALUES (1, 'one')"))

		var called atomic.Bool
		ctx := clickhouse.Context(t.Context(), clickhouse.WithProfileEvents(func([]clickhouse.ProfileEvent) {
			called.Store(true)
		}))

		var rows []serverRow
		require.NoError(t, db.Select(ctx, &rows, "SELECT id, name FROM profiled"))
		assert.True(t, called.Load())

		// The callback is the caller's, so the counters went to them rather than
		// to the span.
		span := spanNamed(t, rec.Spans(), "ClickHouse: Select")
		for _, attr := range span.Attributes() {
			assert.NotContains(t, string(attr.Key), "selected")
		}
	})
}
