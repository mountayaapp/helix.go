package postgres

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/mountaya/helix.go/errorstack"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrClosed is returned by PostgreSQL.Listen once the Service has closed the
// connection, and by Listener.WaitForNotification once the Listener is closed —
// including when a wait in progress is interrupted by Close. A caller looping on
// WaitForNotification can test for it with errors.Is to tell "stop" from a
// connection that failed and is worth listening on again.
var ErrClosed = errors.New("postgres: closed")

// Listener receives the notifications the LISTEN/NOTIFY system delivers on a set
// of channels. It is returned by PostgreSQL.Listen.
//
// A Listener holds a connection of its own, opened for it and outside of the
// pool: a pooled connection only reads from the server while it runs a
// statement, so a notification sent to one would wait for whichever request
// happened to use it next. The connection is not counted in the pool's size or
// its metrics, and it is open until Close — or until the Service stops, which
// closes every Listener still open.
//
// Notifications are not queued by the server for a channel nobody listens on.
// Those sent while a Listener is not connected — before Listen returns, after its
// connection failed — are lost; a caller that cannot afford that re-reads the
// state it is notified about after listening again.
//
// Waiting is not traced: a wait can last for hours, and a span covering it would
// describe idleness. Opening the connection and the LISTEN statement are, as
// "PostgreSQL: Connect" and "PostgreSQL: Listen".
type Listener interface {

	// WaitForNotification blocks until a notification arrives on one of the
	// channels the Listener listens on, and returns it. Notifications that arrive
	// while nobody is waiting are held until the next wait, and returned in order.
	//
	// It returns ctx's error when ctx ends first, and the connection is then still
	// usable: waiting again resumes where it left off. It returns ErrClosed when
	// the Listener is closed, including by a Close interrupting this very wait.
	// Any other error means the connection failed: the Listener is then unusable,
	// and the caller closes it and listens again.
	//
	// It is safe to call from several goroutines; the waits are served one at a
	// time.
	WaitForNotification(ctx context.Context) (*pgconn.Notification, error)

	// Close closes the Listener's dedicated connection, which ends its LISTEN. It
	// interrupts a WaitForNotification in progress, which returns ErrClosed, and
	// it is safe to call more than once and from any goroutine.
	//
	// It closes nothing else: the pool, and the PostgreSQL value Listen was called
	// on, stay usable.
	Close(ctx context.Context) error
}

// listener implements Listener on a dedicated pgx connection.
type listener struct {

	// owner is the connection that opened the listener, which tracks it so that
	// the Service stopping closes it.
	owner *connection

	// client is the dedicated connection. A pgx.Conn is not safe for concurrent
	// use, so every use of it holds mu.
	client *pgx.Conn

	// closing is cancelled by Close before it takes mu. It is what lets Close
	// interrupt a wait holding mu, rather than queueing behind a notification that
	// may never come.
	closing context.Context
	cancel  context.CancelFunc

	mu     sync.Mutex
	closed bool
}

// newListener returns a Listener over client, tracked by owner.
func newListener(owner *connection, client *pgx.Conn) *listener {
	closing, cancel := context.WithCancel(context.Background())

	return &listener{
		owner:   owner,
		client:  client,
		closing: closing,
		cancel:  cancel,
	}
}

// WaitForNotification blocks until a notification arrives, ctx ends, or the
// Listener is closed. See Listener.
func (l *listener) WaitForNotification(ctx context.Context) (*pgconn.Notification, error) {

	// The wait ends when the caller's ctx does or when Close is called, whichever
	// comes first. pgx stops a wait on a context by setting a deadline on the
	// socket, which leaves the connection usable afterwards.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stop := context.AfterFunc(l.closing, cancel)
	defer stop()

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil, ErrClosed
	}

	notif, err := l.client.WaitForNotification(ctx)
	if err != nil && l.closing.Err() != nil {
		return nil, ErrClosed
	}

	return notif, err
}

// Close closes the dedicated connection. See Listener.
func (l *listener) Close(ctx context.Context) error {
	l.cancel()

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}

	l.closed = true
	l.owner.untrack(l)

	return l.client.Close(ctx)
}

// listenSQL returns the statement that listens on every channel.
//
// Each channel is quoted as an identifier, so the name is matched exactly, case
// included, and cannot carry SQL of its own. That is the name pg_notify(channel,
// payload) addresses; an unquoted NOTIFY folds its channel to lower case first.
func listenSQL(channels []string) string {
	var b strings.Builder
	for i, channel := range channels {
		if i > 0 {
			b.WriteString("; ")
		}

		b.WriteString("LISTEN ")
		b.WriteString(pgx.Identifier{channel}.Sanitize())
	}

	return b.String()
}

// validateChannels returns a validation error when channels is empty or holds an
// empty name, and nil otherwise.
func validateChannels(channels []string) error {
	if len(channels) == 0 {
		return errorstack.NewValidation(errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"channels"},
		})
	}

	var entries []errorstack.Entry
	for i, channel := range channels {
		if channel == "" {
			entries = append(entries, errorstack.Entry{
				Message: "Must not be empty",
				Path:    []any{"channels", i},
			})
		}
	}

	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	return nil
}
