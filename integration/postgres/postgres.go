package postgres

import (
	"context"
	"sync"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
)

// PostgreSQL exposes an opinionated way to interact with PostgreSQL, by bringing
// automatic distributed tracing, operation metrics and error recording to a pool
// of connections.
//
// Every method but Listen takes a connection from the pool for the duration of
// the work, and every method is safe for concurrent use. Errors are the driver's
// own, unwrapped, so errors.As with *pgconn.PgError reaches the server's error
// code.
type PostgreSQL interface {

	// BeginTx takes a connection from the pool and starts a transaction on it,
	// with the isolation level, access mode and deferrable mode of txOptions.
	//
	// Unlike database/sql, ctx only affects the BEGIN statement: there is no
	// rollback when it is cancelled. The transaction holds its connection until
	// Commit or Rollback, so one of them must be called; a deferred Rollback is
	// safe after a Commit.
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (Tx, error)

	// Exec executes a statement that returns no rows, and returns its command
	// tag. query is either SQL or the name of a prepared statement. Arguments are
	// referenced positionally from the query as $1, $2, etc.
	Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error)

	// Query sends a query to the server and returns the Rows to read its result
	// from. Only errors encountered sending the query and initializing Rows are
	// returned; an error the server raises while producing rows is reported by
	// Rows.Err after the rows are closed, so it must be checked there.
	//
	// The Rows hold their connection until they are closed, so they must always
	// be closed. Reading is safe even when an error is returned: the error is then
	// also available from Rows.Err.
	//
	// A query may return some rows before failing. Collecting the rows before
	// acting on them — pgx.CollectRows does — avoids processing a result the
	// server ended up rejecting.
	//
	// A pgx.QueryRewriter passed as the first argument rewrites the query and its
	// arguments; pgx.NamedArgs is one, for named arguments.
	Query(ctx context.Context, query string, args ...any) (pgx.Rows, error)

	// QueryRow is a convenience wrapper over Query for a query that returns at
	// most one row. Any error is deferred until Scan is called on the returned
	// Row, which returns pgx.ErrNoRows when there was no row. The connection is
	// released by Scan.
	QueryRow(ctx context.Context, query string, args ...any) pgx.Row

	// SendBatch sends every statement queued in batch to the server in one round
	// trip, on one connection, and returns the results to read in queue order.
	// The statements run in an implicit transaction unless the batch has
	// transaction control statements of its own.
	//
	// The returned BatchResults hold their connection until they are closed, so
	// they must always be closed; Close also returns the first error any
	// statement raised.
	SendBatch(ctx context.Context, batch *pgx.Batch) pgx.BatchResults

	// CopyFrom bulk-loads the rows rowSrc yields into the columns of tableName
	// with the COPY protocol, and returns how many rows were copied. It is the
	// fastest way to insert many rows, and it is all or nothing: a row the server
	// rejects fails the whole copy. pgx.CopyFromRows and pgx.CopyFromSlice adapt
	// in-memory data to a pgx.CopyFromSource.
	CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error)

	// Listen opens a dedicated connection, outside of the pool, and listens on
	// every one of channels with it. Notifications are then read with the
	// returned Listener's WaitForNotification, as they arrive.
	//
	// Each channel name is matched exactly, case included — it is the name
	// pg_notify(channel, payload) addresses. At least one channel is required.
	//
	// The Listener must be closed when no longer needed: it holds a server
	// connection until then. Closing it closes nothing else. Listen returns
	// ErrClosed once the Service has closed the connection.
	Listen(ctx context.Context, channels ...string) (Listener, error)
}

// connection implements the PostgreSQL interface. It is not what the Service
// holds: see dependency, which adapts it to integration.Dependency.
//
// It holds no Config: everything a span needs from one is stamped by the
// Instrument the driver's tracer carries, and the methods below build no spans of
// their own.
type connection struct {

	// client is the pool of connections made with the PostgreSQL database.
	client *pgxpool.Pool

	// mu guards the fields below: Listeners open and close, and the Service
	// closes the connection, while it is in use.
	mu sync.Mutex

	// closed is set once the Service has closed the connection. Nothing is
	// registered after that, so that nothing outlives the pool.
	closed bool

	// metrics observes the pool. It is held so that closing can unregister the
	// callback before the pool it reads from goes away.
	metrics *poolMetrics

	// listeners are the Listeners opened and not yet closed, which closing the
	// connection closes too: their connections are not the pool's, so closing the
	// pool would leave them open.
	listeners map[*listener]struct{}
}

// Connect tries to connect to the PostgreSQL database given the Config, and
// attaches the connection to the Service, which owns its lifecycle from then on.
//
// Returns a validation error if the Config is not valid, before any connection
// is attempted. Returns a SERVICE_UNAVAILABLE error if the database cannot be
// reached and authenticated against — the pool opens one connection and pings
// the database before Connect returns — and the attach error if the Service
// refuses the connection, such as for a Name already in use. On any error,
// everything Connect opened is closed again.
func Connect(ctx context.Context, svc *service.Service, cfg Config) (PostgreSQL, error) {

	// No need to continue if Config is not valid.
	err := cfg.sanitize()
	if err != nil {
		return nil, err
	}

	tlsConfig, entries := cfg.TLS.ToStandardTLS()
	if len(entries) > 0 {
		return nil, errorstack.NewValidation(entries...)
	}

	opts, err := poolConfig(&cfg, tlsConfig)
	if err != nil {
		return nil, errorstack.NewValidation(errorstack.Entry{
			Message: integration.NormalizeErrorMessage(err),
			Path:    []any{"config", "address"},
		})
	}

	// Hand the driver the one thing that creates a span for this integration. pgx
	// discovers what it implements by type assertion on this single value, which is
	// how one struct ends up tracing queries, batches, copies, prepares, connects
	// and pool acquires — three of which never pass through a method below, and so
	// could not be traced from here.
	inst, err := integration.NewInstrument(integration.InstrumentConfig{
		Identifier: identifier,
		Humanized:  humanized,
		Name:       cfg.name(),
		Attributes: []attribute.KeyValue{
			integration.Key(identifier, "database").String(cfg.Database),
		},
		TracerProvider: service.TracerProvider(svc),
		MeterProvider:  service.MeterProvider(svc),
		Propagator:     service.Propagator(svc),
	})
	if err != nil {
		return nil, err
	}

	opts.ConnConfig.Tracer = &tracer{inst: inst, onWrite: cfg.OnWrite}

	// Wrap the notification callback so end-users never reach the underlying pgx
	// connection the driver hands it.
	if cfg.OnNotification != nil {
		opts.ConnConfig.OnNotification = func(_ *pgconn.PgConn, notif *pgconn.Notification) {
			cfg.OnNotification(notif)
		}
	}

	client, err := pgxpool.NewWithConfig(ctx, opts)
	if err != nil {
		return nil, unavailable(err)
	}

	// The pool connects lazily, so without this a database that cannot be reached
	// — or a password it rejects — would surface on the first request rather than
	// here. The ping is a probe, and like the readiness check it is not traced.
	if err := client.Ping(untraced(ctx)); err != nil {
		client.Close()
		return nil, unavailable(err)
	}

	conn := &connection{client: client}

	// Hand the connection to the Service, which owns its lifecycle from here.
	if err := service.Attach(svc, cfg.name(), dependency{conn: conn}); err != nil {
		client.Close()
		return nil, err
	}

	conn.observe(inst)

	return conn, nil
}

// unavailable returns the error Connect reports when the driver could not reach
// the database.
func unavailable(err error) error {
	return errorstack.Wrap(err, "Failed to connect to PostgreSQL",
		errorstack.WithCode(errorstack.CodeServiceUnavailable),
		errorstack.WithExtension("reason", integration.NormalizeErrorMessage(err)),
	)
}

// observe registers the pool's metrics, unless the connection was closed first.
//
// It runs last in Connect, after the Service took the connection: a registration
// made before a step that can still fail is one more thing to undo, and one made
// for a connection the Service refused would go on reporting under its name.
func (conn *connection) observe(inst *integration.Instrument) {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	if conn.closed {
		return
	}

	conn.metrics = newPoolMetrics(inst, conn.client)
}

// shutdown marks the connection closed, unregisters the pool's metrics, and
// returns the Listeners still open, for the caller to close.
func (conn *connection) shutdown() []*listener {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	conn.closed = true
	conn.metrics.unregister()

	listeners := make([]*listener, 0, len(conn.listeners))
	for l := range conn.listeners {
		listeners = append(listeners, l)
	}

	return listeners
}

// track records l as open, and reports false when the connection was closed in
// the meantime, in which case l is the caller's to close.
func (conn *connection) track(l *listener) bool {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	if conn.closed {
		return false
	}

	if conn.listeners == nil {
		conn.listeners = make(map[*listener]struct{})
	}

	conn.listeners[l] = struct{}{}

	return true
}

// untrack forgets l, once it is closed.
func (conn *connection) untrack(l *listener) {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	delete(conn.listeners, l)
}

// isClosed reports whether the Service has closed the connection.
func (conn *connection) isClosed() bool {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	return conn.closed
}

// BeginTx starts a transaction. See PostgreSQL.
func (conn *connection) BeginTx(ctx context.Context, txOptions pgx.TxOptions) (Tx, error) {
	client, err := conn.client.BeginTx(withOperation(ctx, "Transaction / Begin"), txOptions)
	if err != nil {
		return nil, err
	}

	return &transaction{client: client}, nil
}

// Exec executes a statement. See PostgreSQL.
func (conn *connection) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return conn.client.Exec(withOperation(ctx, "Exec"), query, args...)
}

// Query sends a query and returns its Rows. See PostgreSQL.
func (conn *connection) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	return conn.client.Query(withOperation(ctx, "Query"), query, args...)
}

// QueryRow sends a query and returns its one Row. See PostgreSQL.
func (conn *connection) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return conn.client.QueryRow(withOperation(ctx, "QueryRow"), query, args...)
}

// SendBatch sends a batch of statements. See PostgreSQL.
func (conn *connection) SendBatch(ctx context.Context, batch *pgx.Batch) pgx.BatchResults {
	return conn.client.SendBatch(withOperation(ctx, "Batch"), batch)
}

// CopyFrom bulk-loads rows with the COPY protocol. See PostgreSQL.
func (conn *connection) CopyFrom(
	ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource,
) (int64, error) {
	return conn.client.CopyFrom(withOperation(ctx, "Copy From"), tableName, columnNames, rowSrc)
}

// Listen opens a Listener on a dedicated connection. See PostgreSQL.
func (conn *connection) Listen(ctx context.Context, channels ...string) (Listener, error) {
	if err := validateChannels(channels); err != nil {
		return nil, err
	}

	if conn.isClosed() {
		return nil, ErrClosed
	}

	// The pool's configuration is a copy, so the dedicated connection gets the
	// same server, credentials, TLS and tracer without touching the pool. It does
	// not get the OnNotification callback: pgx only buffers notifications for
	// WaitForNotification on a connection that has none.
	connConfig := conn.client.Config().ConnConfig
	connConfig.OnNotification = nil

	ctx = withOperation(ctx, "Listen")

	client, err := pgx.ConnectConfig(ctx, connConfig)
	if err != nil {
		return nil, err
	}

	if _, err := client.Exec(ctx, listenSQL(channels)); err != nil {
		_ = client.Close(ctx)
		return nil, err
	}

	l := newListener(conn, client)
	if !conn.track(l) {
		_ = client.Close(ctx)
		return nil, ErrClosed
	}

	return l, nil
}
