package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/service"
	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListenSQL(t *testing.T) {
	assert.Equal(t, `LISTEN "events"`, listenSQL([]string{"events"}))
	assert.Equal(t, `LISTEN "Events"; LISTEN "jobs"`, listenSQL([]string{"Events", "jobs"}))
	assert.Equal(t, `LISTEN "a"";DROP TABLE things;--"`, listenSQL([]string{`a";DROP TABLE things;--`}),
		"a channel name must not be able to carry SQL of its own")
}

func TestValidateChannels(t *testing.T) {
	assert.NoError(t, validateChannels([]string{"events"}))

	err := validateChannels(nil)
	assert.Equal(t, errorstack.NewValidation(errorstack.Entry{Message: "Must be set", Path: []any{"channels"}}), err)

	err = validateChannels([]string{"events", ""})
	assert.Equal(t, errorstack.NewValidation(errorstack.Entry{Message: "Must not be empty", Path: []any{"channels", 1}}), err)
}

// connectListening connects to a database of its own and returns the connection
// along with its Service.
func connectListening(t *testing.T, cfg Config) (PostgreSQL, *service.Service) {
	t.Helper()

	svc, _ := servicetest.New(t)

	db, err := Connect(t.Context(), svc, cfg)
	require.NoError(t, err)

	return db, svc
}

// notify sends payload on channel through the pool.
func notify(t *testing.T, db PostgreSQL, channel, payload string) {
	t.Helper()

	_, err := db.Exec(t.Context(), "SELECT pg_notify($1, $2)", channel, payload)
	require.NoError(t, err)
}

// nextNotification waits for the next notification on l, failing the test after
// a while rather than hanging it.
func nextNotification(t *testing.T, l Listener) (*pgconn.Notification, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	return l.WaitForNotification(ctx)
}

func TestListen(t *testing.T) {
	db, _ := connectListening(t, testDatabase(t))

	l, err := db.Listen(t.Context(), "Events", "jobs")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close(context.Background()) })

	t.Run("notifications arrive in order, on every channel, with their case", func(t *testing.T) {
		notify(t, db, "Events", "first")
		notify(t, db, "jobs", "second")
		notify(t, db, "events", "not listened to")
		notify(t, db, "Events", "third")

		for _, want := range []struct{ channel, payload string }{
			{"Events", "first"},
			{"jobs", "second"},
			{"Events", "third"},
		} {
			notif, err := nextNotification(t, l)
			require.NoError(t, err)
			assert.Equal(t, want.channel, notif.Channel)
			assert.Equal(t, want.payload, notif.Payload)
		}
	})

	t.Run("a cancelled wait leaves the Listener usable", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()

		_, err := l.WaitForNotification(ctx)
		require.ErrorIs(t, err, context.DeadlineExceeded)

		notify(t, db, "jobs", "after the timeout")

		notif, err := nextNotification(t, l)
		require.NoError(t, err)
		assert.Equal(t, "after the timeout", notif.Payload)
	})
}

// TestListen_WithOnNotification is the regression guard for the callback the
// pool's connections are configured with: pgx does not buffer notifications for
// WaitForNotification on a connection that has one, so the Listener's dedicated
// connection must be made without it, or every wait returns nothing.
func TestListen_WithOnNotification(t *testing.T) {
	cfg := testDatabase(t)
	cfg.OnNotification = func(*pgconn.Notification) {}

	db, _ := connectListening(t, cfg)

	l, err := db.Listen(t.Context(), "events")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close(context.Background()) })

	notify(t, db, "events", "payload")

	notif, err := nextNotification(t, l)
	require.NoError(t, err)
	require.NotNil(t, notif)
	assert.Equal(t, "payload", notif.Payload)
}

func TestListen_Validation(t *testing.T) {
	db, _ := connectListening(t, testDatabase(t))

	_, err := db.Listen(t.Context())
	assert.Equal(t, errorstack.CodeValidationFailed, codeOf(err))

	_, err = db.Listen(t.Context(), "")
	assert.Equal(t, errorstack.CodeValidationFailed, codeOf(err))
}

func TestListener_Close(t *testing.T) {
	db, _ := connectListening(t, testDatabase(t))

	l, err := db.Listen(t.Context(), "events")
	require.NoError(t, err)

	// A wait in progress is interrupted rather than left waiting for a
	// notification that will not come.
	waited := make(chan error, 1)
	go func() {
		_, err := l.WaitForNotification(context.Background())
		waited <- err
	}()

	time.Sleep(50 * time.Millisecond)
	require.NoError(t, l.Close(t.Context()))

	select {
	case err := <-waited:
		assert.ErrorIs(t, err, ErrClosed)
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not interrupt the wait in progress")
	}

	_, err = l.WaitForNotification(t.Context())
	assert.ErrorIs(t, err, ErrClosed)
	assert.NoError(t, l.Close(t.Context()), "closing twice is a no-op")

	// Closing a Listener closes its own connection, and nothing else.
	_, err = db.Exec(t.Context(), "SELECT 1")
	assert.NoError(t, err)
}

// TestListener_ServiceStop pins that the Service stopping closes every Listener
// still open. Their connections are not the pool's, so closing the pool alone
// would leave them, and the goroutines waiting on them, behind.
func TestListener_ServiceStop(t *testing.T) {
	db, svc := connectListening(t, testDatabase(t))

	l, err := db.Listen(t.Context(), "events")
	require.NoError(t, err)

	waited := make(chan error, 1)
	go func() {
		_, err := l.WaitForNotification(context.Background())
		waited <- err
	}()

	time.Sleep(50 * time.Millisecond)
	require.NoError(t, svc.Stop(t.Context()))

	select {
	case err := <-waited:
		assert.True(t, errors.Is(err, ErrClosed), "got %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("stopping the Service did not close the Listener")
	}

	_, err = db.Listen(t.Context(), "events")
	assert.ErrorIs(t, err, ErrClosed)
}
