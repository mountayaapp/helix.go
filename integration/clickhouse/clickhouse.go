package clickhouse

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"sync/atomic"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"
	"github.com/mountaya/helix.go/service"
	"github.com/mountaya/helix.go/telemetry/log"
	"github.com/mountaya/helix.go/telemetry/trace"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// ClickHouse exposes an opinionated way to interact with ClickHouse, by bringing
// automatic distributed tracing, metrics and error recording.
//
// Every call opens a span named "ClickHouse: <Operation>" and measures it in the
// clickhouse.operation.duration histogram — save the "ClickHouse: Batch" span
// standing for a whole batch, whose duration includes the caller's own time
// appending and so is not measured. Statements ship their span's identifiers to
// the server, and ask the server for the rows and bytes they touched. Both yield
// to a caller who set their own on ctx through the driver's clickhouse.Context:
// the caller's WithSpan is shipped instead, and a caller's WithProfileEvents
// callback is kept, in which case the span carries no clickhouse.selected.* or
// clickhouse.inserted.* attributes.
//
// Pass query arguments as variadic args, so the driver binds them as parameters
// rather than interpolating them into the SQL.
type ClickHouse interface {

	// Exec runs a statement that returns no rows: DDL, ALTER, an
	// INSERT ... SELECT.
	Exec(ctx context.Context, query string, args ...any) error

	// Query runs a read and returns its rows to iterate over. Check Err on the
	// rows once Next reports false.
	//
	// The rows hold a connection, and the span stays open, until the rows are
	// closed or Next reports false, so that the span covers the whole result
	// being read. Close them on every path, typically with a deferred Close.
	Query(ctx context.Context, query string, args ...any) (driver.Rows, error)

	// QueryRow runs a read expected to return at most one row. Any error is
	// deferred to Scan or ScanStruct, which return sql.ErrNoRows when the read
	// returned no row.
	//
	// The row holds a connection, and the span stays open, until Scan or
	// ScanStruct is called. No row is not an error on the span: it is an answer.
	QueryRow(ctx context.Context, query string, args ...any) driver.Row

	// Select runs a read and scans the whole result set into dest, which must be
	// a pointer to a slice of structs whose exported fields carry `ch` struct
	// tags matching the selected columns.
	//
	// Intended for analytical reads such as aggregations over large datasets. For
	// writes, use NewBatchInsert or AsyncInsertStruct.
	Select(ctx context.Context, dest any, query string, args ...any) error

	// NewBatchInsert begins a batch insert into table, which must be a table
	// name made of letters, digits and underscores, optionally qualified by its
	// database ("analytics.events"). Any other table name is refused before
	// anything is sent, since it is spliced into the statement.
	//
	// With no option, the batch inserts every column of the table, in its order.
	// WithColumns and WithColumnsOf name the columns it inserts instead, the
	// others taking their DEFAULT.
	//
	// The Batch outlives ctx: it can be begun in one request and sent after that
	// request has ended. ctx bounds the begin itself, once a connection is
	// established: dialing one is bounded by Config.DialTimeout only. See Batch.
	NewBatchInsert(ctx context.Context, table string, opts ...BatchOption) (Batch, error)

	// AsyncInsertStruct inserts a single struct row into the given table using
	// ClickHouse's native async_insert mechanism. The server buffers incoming
	// rows and flushes them in the background, so this call does not pay the
	// prepare/append/send roundtrip cost of a batch insert. The call returns as
	// soon as the server acknowledges receipt; it does not wait for the flush.
	//
	// Intended for high-frequency, low-latency per-request writes such as request
	// counters. For bulk loads, prefer NewBatchInsert. Server-side buffering is
	// governed by `async_insert_max_data_size`, `async_insert_busy_timeout_ms`,
	// and `async_insert_max_query_number`.
	//
	// The table is held to the same shape as for NewBatchInsert. value is a
	// struct, or a pointer to one, whose every exported field carries a `ch`
	// struct tag naming its column, or `ch:"-"` to be skipped. Embedded structs
	// are refused: unlike a batch insert, an async insert does not recurse into
	// them.
	AsyncInsertStruct(ctx context.Context, table string, value any) error
}

// connection represents the clickhouse integration. It implements the ClickHouse
// interface; the dependency type adapts it to integration.Dependency.
type connection struct {

	// inst names this connection to the telemetry pipeline. Every span below is
	// opened through it, which is what keeps the naming the same here as in the
	// eight other integrations.
	inst *integration.Instrument

	// client is the connection made with the ClickHouse database.
	client driver.Conn

	// pool observes the connection pool. It is held so that Close can unregister
	// the callback before the connection it reads from goes away.
	pool *poolMetrics

	// optional is Config.Optional: a failed ping does not fail Connect, and the
	// connection is reported healthy without a ping.
	optional bool

	// reachable is whether the last ping succeeded: Connect's, the prober's, or
	// a health check's. It is what the clickhouse.reachable gauge reports.
	reachable atomic.Bool

	// prober pings the server on its own schedule, so reachable follows it.
	prober *prober
}

// Connect connects to the ClickHouse database given the Config, and attaches the
// connection to the Service, which owns its lifecycle from then on.
//
// The server is pinged before Connect returns, so that an unreachable server or a
// rejected login fails here rather than on the first statement. ctx bounds the
// ping once a connection is established; dialing it and the handshake are bounded
// by Config.DialTimeout alone, which the driver applies whatever ctx says.
// Returns a validation error if Config is not valid, before anything is dialed,
// and a SERVICE_UNAVAILABLE error if the server could not be reached, unless
// Config.Optional is set and nothing refused the connection (tolerated): it is
// then attached anyway, and a warning is logged.
func Connect(ctx context.Context, svc *service.Service, cfg Config) (ClickHouse, error) {

	// No need to continue if Config is not valid.
	if err := cfg.sanitize(); err != nil {
		return nil, err
	}

	opts, err := cfg.options()
	if err != nil {
		return nil, err
	}

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

	// Open builds the pool and dials nothing: the ping below is the first round
	// trip.
	client, err := clickhouse.Open(opts)
	if err != nil {
		return nil, errorstack.Wrap(err, "Failed to open connection pool",
			errorstack.WithCode(errorstack.CodeServiceUnavailable),
			errorstack.WithExtension("reason", integration.NormalizeErrorMessage(err)),
		)
	}

	// A nil *connection in the interface would compare unequal to nil.
	conn, err := attach(ctx, svc, cfg.name(), inst, client, cfg.Optional)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

// attach pings the server through client, hands the connection to the Service
// and starts observing its pool — in that order, closing client on any failure,
// so that a Connect that fails leaves neither a connection nor a metric behind.
//
// An optional connection whose server could not be reached is attached anyway,
// reported unreachable, and a warning is logged; one refused is not (tolerated).
// Once attached, the connection's prober keeps reachable current.
//
// The pool is observed only once attached. Observed before, a connection that
// Attach refuses for a duplicate name would report its pool under the name of the
// connection already attached, and go on reporting it.
func attach(ctx context.Context, svc *service.Service, name string, inst *integration.Instrument, client driver.Conn, optional bool) (*connection, error) {
	pingErr := client.Ping(ctx)
	if pingErr != nil && !tolerated(pingErr, optional) {
		_ = client.Close()

		return nil, errorstack.Wrap(pingErr, "Failed to connect",
			errorstack.WithCode(errorstack.CodeServiceUnavailable),
			errorstack.WithExtension("reason", integration.NormalizeErrorMessage(pingErr)),
		)
	}

	conn := &connection{
		inst:     inst,
		client:   client,
		pool:     &poolMetrics{},
		optional: optional,
	}
	conn.reachable.Store(pingErr == nil)
	conn.prober = newProber(conn)

	// Hand the connection to the Service, which owns its lifecycle from here.
	if err := service.Attach(svc, name, dependency{conn: conn}); err != nil {
		_ = client.Close()
		return nil, err
	}

	conn.pool.register(inst, client, &conn.reachable)
	conn.prober.start()

	if pingErr != nil {
		log.Warn(service.Context(ctx, svc), "ClickHouse is unreachable; the optional connection is attached and dials it again on use",
			log.String("name", name),
			log.Any("reason", pingErr),
		)
	}

	return conn, nil
}

// tolerated reports whether a failed ping leaves the connection attached: only
// when it is optional, and only when nothing refused the connection, which no
// reconnect fixes:
//
//   - a server that answered with an exception (a rejected login, a database
//     that does not exist);
//   - a TLS handshake that failed: a certificate the configuration does not
//     trust or that names another host, a server that does not speak TLS where
//     the configuration expects it, or an alert either end raised, such as a
//     server that requires a client certificate the configuration does not
//     present (tlsAlert).
//
// A server that cannot be reached, or does not answer, is an outage to wait out,
// and so is a name that does not resolve, since a DNS record can come back with
// the server it names.
func tolerated(pingErr error, optional bool) bool {
	if !optional {
		return false
	}

	var (
		exception    *clickhouse.Exception
		verification *tls.CertificateVerificationError
		alert        tls.AlertError
		record       tls.RecordHeaderError
		authority    x509.UnknownAuthorityError
		hostname     x509.HostnameError
		invalid      x509.CertificateInvalidError
	)

	switch {
	case errors.As(pingErr, &exception),
		errors.As(pingErr, &verification),
		errors.As(pingErr, &alert),
		errors.As(pingErr, &record),
		errors.As(pingErr, &authority),
		errors.As(pingErr, &hostname),
		errors.As(pingErr, &invalid),
		tlsAlert(pingErr):
		return false
	}

	return true
}

// tlsAlert reports whether err is a TLS alert over TCP: one the server sent, which
// crypto/tls reports as a *net.OpError whose Op is "remote error", or one this end
// raised, "local error", each wrapping the alert, a "tls: " error of an
// unexported type. tls.AlertError is what QUIC reports instead.
func tlsAlert(err error) bool {
	op, ok := errors.AsType[*net.OpError](err)
	if !ok || (op.Op != "remote error" && op.Op != "local error") || op.Err == nil {
		return false
	}

	return strings.HasPrefix(op.Err.Error(), "tls: ")
}

// Exec implements ClickHouse.
func (conn *connection) Exec(ctx context.Context, query string, args ...any) error {
	ctx, op := conn.startStatement(ctx, "Exec", query)
	defer op.End(ctx)

	prof := &profile{}
	err := conn.client.Exec(queryContext(ctx, prof), query, args...)
	prof.stamp(op.Span)
	op.RecordError("failed to execute statement", err)

	return err
}

// Query implements ClickHouse.
func (conn *connection) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	ctx, op := conn.startStatement(ctx, "Query", query)

	prof := &profile{}
	result, err := conn.client.Query(queryContext(ctx, prof), query, args...)
	if err != nil {
		prof.stamp(op.Span)
		op.RecordError("failed to query", err)
		op.End(ctx)

		return nil, err
	}

	return &rows{Rows: result, ctx: ctx, op: op, profile: prof}, nil
}

// QueryRow implements ClickHouse.
func (conn *connection) QueryRow(ctx context.Context, query string, args ...any) driver.Row {
	ctx, op := conn.startStatement(ctx, "QueryRow", query)

	prof := &profile{}
	r := &row{
		Row:     conn.client.QueryRow(queryContext(ctx, prof), query, args...),
		ctx:     ctx,
		op:      op,
		profile: prof,
	}

	// A read that failed outright has nothing left for Scan to wait on.
	if err := r.Err(); err != nil {
		r.end(err)
	}

	return r
}

// Select implements ClickHouse.
func (conn *connection) Select(ctx context.Context, dest any, query string, args ...any) error {
	ctx, op := conn.startStatement(ctx, "Select", query)
	defer op.End(ctx)

	prof := &profile{}
	err := conn.client.Select(queryContext(ctx, prof), dest, query, args...)
	prof.stamp(op.Span)
	op.RecordError("failed to select", err)

	return err
}

// NewBatchInsert implements ClickHouse.
//
// It opens two spans. "Batch" stands for the whole batch and stays open until
// Send or Abort, so that what happens to the batch is recorded on it; it is a
// span rather than an Operation, because its duration includes however long the
// caller took to append and measures nothing about ClickHouse. "Batch / Begin" is
// the round trip that starts the INSERT, and ends with it.
func (conn *connection) NewBatchInsert(ctx context.Context, table string, opts ...BatchOption) (Batch, error) {
	batchCtx, span := conn.inst.Start(ctx, trace.SpanKindInternal, "Batch")
	span.SetAttributes(attrKeyTable.String(table))

	beginCtx, op := conn.inst.StartOperation(batchCtx, trace.SpanKindClient, "Batch / Begin")
	op.SetAttributes(attrKeyTable.String(table))

	// No Batch is handed back on failure. Returning one whose driver handle is
	// nil means a caller following the deferred-cleanup idiom panics instead of
	// seeing the error it was given.
	fail := func(err error) (Batch, error) {
		op.RecordError("failed to begin batch", err)
		op.End(beginCtx)
		span.RecordError("failed to begin batch", err)
		span.End()

		return nil, err
	}

	quoted, err := quoteTable(table)
	if err != nil {
		return fail(err)
	}

	options := &batchOptions{}
	for _, opt := range opts {
		opt(options)
	}

	if options.err != nil {
		return fail(options.err)
	}

	// The driver keeps the context PrepareBatch is given for the life of the
	// batch, and works under it again on Send. Handed ctx, a batch begun in a
	// request and sent after that request ended would fail on Send, so the driver
	// gets a context that keeps ctx's values — the Batch span among them, which
	// the server's own span for the INSERT is parented under — but not its
	// cancellation. Each call that does I/O forwards its own ctx's cancellation
	// for as long as it runs: this one, and Send.
	prof := &profile{}
	driverCtx, cancel := context.WithCancelCause(queryContext(context.WithoutCancel(batchCtx), prof))

	stop := context.AfterFunc(ctx, func() {
		cancel(context.Cause(ctx))
	})

	client, err := conn.client.PrepareBatch(driverCtx, options.insertStatement(quoted))

	// ctx ended while the batch was being begun, or just as it was: the driver's
	// context is cancelled either way, and a batch begun under it would only fail
	// on Send. The driver reports that as context.Canceled; ctx's own error is
	// what the caller can recognise.
	if !stop() {
		if client != nil {
			_ = client.Abort()
		}

		err = ctx.Err()
	}

	if err != nil {
		cancel(nil)
		return fail(err)
	}

	op.End(beginCtx)

	return &batch{
		inst:    conn.inst,
		client:  client,
		profile: prof,
		table:   attrKeyTable.String(table),
		span:    span,
		parent:  oteltrace.SpanFromContext(batchCtx),
		cancel:  cancel,
	}, nil
}

// AsyncInsertStruct implements ClickHouse.
func (conn *connection) AsyncInsertStruct(ctx context.Context, table string, value any) error {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "Async Insert")
	defer op.End(ctx)

	op.SetAttributes(attrKeyTable.String(table))

	query, args, err := prepareAsyncInsert(table, value)
	if err != nil {
		op.RecordError("failed to prepare async insert", err)
		return err
	}

	// WithAsync(false): the server buffers the row and we do not block waiting
	// for the flush. This is the recommended async insert API as of
	// clickhouse-go v2.
	prof := &profile{}
	err = conn.client.Exec(queryContext(ctx, prof, clickhouse.WithAsync(false)), query, args...)
	prof.stamp(op.Span)
	op.RecordError("failed to async insert struct", err)

	return err
}

// startStatement opens the Operation for a statement the caller wrote, stamped
// with its text. Only the text: arguments are bound by the driver, and are where
// the values — and anything sensitive among them — live.
func (conn *connection) startStatement(ctx context.Context, operation, query string) (context.Context, *integration.Operation) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, operation)
	if op.IsRecording() {
		op.SetAttributes(attrKeyQuery.String(query))
	}

	return ctx, op
}
