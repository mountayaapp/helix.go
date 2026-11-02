package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDependency_CheckHealth(t *testing.T) {
	cfg := testDatabase(t)
	svc, rec := servicetest.New(t)

	db, err := Connect(t.Context(), svc, cfg)
	require.NoError(t, err)

	dep := dependency{conn: db.(*connection)}

	t.Run("a reachable database is healthy", func(t *testing.T) {
		assert.NoError(t, dep.CheckHealth(t.Context()))
	})

	// The regression test for readiness noise: the probe runs on every poll, and
	// its acquire used to add a root span to the trace each time.
	t.Run("a health check is not traced", func(t *testing.T) {
		before := len(rec.Spans())

		require.NoError(t, dep.CheckHealth(t.Context()))
		require.NoError(t, svc.CheckHealth(t.Context()))

		assert.Len(t, rec.Spans(), before)
	})

	t.Run("a closed pool is unhealthy", func(t *testing.T) {
		require.NoError(t, dep.Close(t.Context()))

		err := dep.CheckHealth(t.Context())
		require.Error(t, err)
		assert.Equal(t, errorstack.CodeServiceUnavailable, codeOf(err))
	})
}

// TestDependency_CloseHonoursContext is the regression test for a Close that
// blocked until every connection was returned to the pool: a Rows never closed —
// a leak in the caller, but a common one — held the Service's shutdown forever.
// Close now gives up when ctx ends, and the pool finishes closing once the
// connection is released.
func TestDependency_CloseHonoursContext(t *testing.T) {
	cfg := testDatabase(t)
	svc, _ := servicetest.New(t)

	db, err := Connect(t.Context(), svc, cfg)
	require.NoError(t, err)

	conn := db.(*connection)

	rows, err := db.Query(t.Context(), "SELECT 1")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	closed := make(chan error, 1)
	go func() {
		closed <- dependency{conn: conn}.Close(ctx)
	}()

	select {
	case err := <-closed:
		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return when its context ended")
	}

	// Releasing the connection lets the pool finish closing in the background.
	rows.Close()

	assert.Eventually(t, func() bool {
		return conn.client.Stat().TotalConns() == 0
	}, 10*time.Second, 10*time.Millisecond)
}

// TestConnect_TimeZone pins both halves of Config.TimeZone against a real server:
// the session's TimeZone, which decides what the server makes of a timestamp
// without time zone, and the location every scanned timestamptz is expressed in.
func TestConnect_TimeZone(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	require.NoError(t, err)

	instant := time.Date(2026, time.January, 15, 11, 0, 0, 0, time.UTC)

	t.Run("a time zone sets the session's and the scans'", func(t *testing.T) {
		cfg := testDatabase(t)
		cfg.TimeZone = "Europe/Paris"
		svc, _ := servicetest.New(t)

		db, err := Connect(t.Context(), svc, cfg)
		require.NoError(t, err)

		var session string
		require.NoError(t, db.QueryRow(t.Context(), "SHOW timezone").Scan(&session))
		assert.Equal(t, "Europe/Paris", session)

		var scanned time.Time
		require.NoError(t, db.QueryRow(t.Context(), "SELECT $1::timestamptz", instant).Scan(&scanned))
		assert.Equal(t, paris, scanned.Location())
		assert.True(t, scanned.Equal(instant))

		// A timestamp without time zone is read in the session's zone: noon in Paris
		// is 11:00 UTC in January.
		var wallClock time.Time
		require.NoError(t, db.QueryRow(t.Context(), "SELECT '2026-01-15 12:00:00'::timestamp::timestamptz").Scan(&wallClock))
		assert.True(t, wallClock.Equal(instant))
	})

	t.Run("no time zone scans in the process's local zone", func(t *testing.T) {
		cfg := testDatabase(t)
		svc, _ := servicetest.New(t)

		db, err := Connect(t.Context(), svc, cfg)
		require.NoError(t, err)

		var scanned time.Time
		require.NoError(t, db.QueryRow(t.Context(), "SELECT $1::timestamptz", instant).Scan(&scanned))
		assert.Equal(t, time.Local, scanned.Location())
		assert.True(t, scanned.Equal(instant))
	})
}
