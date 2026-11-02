package postgres

import (
	"context"
	"strings"

	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// operationKeyIdentifier is the unique internal type for carrying the facade's
// name for an operation down into the driver.
type operationKeyIdentifier struct{}

// operationKey is the context key the facade stores an operation name under.
var operationKey operationKeyIdentifier

// prepareKeyIdentifier is the unique internal type for carrying the facade's name
// for an explicit statement preparation.
type prepareKeyIdentifier struct{}

// prepareKey is the context key the facade stores a prepare's name under. It is
// separate from operationKey on purpose — see withPrepare.
var prepareKey prepareKeyIdentifier

// inFlightKeyIdentifier is the unique internal type for carrying an open
// Operation from a tracer's Start hook to its End hook.
type inFlightKeyIdentifier struct{}

// inFlightKey is the context key an in-flight Operation is stored under.
var inFlightKey inFlightKeyIdentifier

// untracedKeyIdentifier is the unique internal type for marking a context whose
// work must not be traced.
type untracedKeyIdentifier struct{}

// untracedKey is the context key a probe's context is marked with.
var untracedKey untracedKeyIdentifier

// batchWritesKeyIdentifier is the unique internal type for carrying what a batch
// wrote from a tracer's batch Start hook to its End hook.
type batchWritesKeyIdentifier struct{}

// batchWritesKey is the context key a batch's batchWrites is stored under.
var batchWritesKey batchWritesKeyIdentifier

// withOperation labels ctx with the name the facade gives the work it is about to
// hand to pgx.
//
// The driver cannot tell an Exec from a Query from a QueryRow — all three arrive
// at TraceQueryStart as the same SQL — and it certainly cannot tell that the
// "begin" it is about to run is a nested savepoint. The facade knows, so it says
// so, and the tracer names the span accordingly.
func withOperation(ctx context.Context, operation string) context.Context {
	return context.WithValue(ctx, operationKey, operation)
}

// operationFrom returns the operation name the facade labelled ctx with, falling
// back to fallback when the call reached the driver by some other route — a
// caller's own pgx usage, or an internal statement pgx runs on its own behalf.
func operationFrom(ctx context.Context, fallback string) string {
	if operation, ok := ctx.Value(operationKey).(string); ok {
		return operation
	}

	return fallback
}

// withPrepare labels ctx with the name of an explicit statement preparation.
//
// It is a separate label from withOperation because pgx prepares statements on
// its own account: under the default exec mode a query whose SQL is not in the
// connection's statement cache is prepared first, through the same hook. Reading
// the query's label there would name that round trip "Query", which is a second
// span claiming to be the query. It is a prepare, and says so.
func withPrepare(ctx context.Context, operation string) context.Context {
	return context.WithValue(ctx, prepareKey, operation)
}

// prepareFrom returns the name of an explicit preparation, falling back to
// "Prepare" for the ones pgx does on its own account.
func prepareFrom(ctx context.Context) string {
	if operation, ok := ctx.Value(prepareKey).(string); ok {
		return operation
	}

	return "Prepare"
}

// untraced marks ctx so that nothing the driver does with it is traced or
// measured.
//
// It is for probes. A readiness check runs on every poll of the probe, and a
// pool's Ping goes through Acquire — and through Connect when the pool is empty —
// so each poll would otherwise add root spans and operation measurements
// describing the probe rather than the service's work.
func untraced(ctx context.Context) context.Context {
	return context.WithValue(ctx, untracedKey, true)
}

// isUntraced reports whether ctx was marked by untraced.
func isUntraced(ctx context.Context) bool {
	return ctx.Value(untracedKey) != nil
}

// tracer is the single place this integration creates a span. pgx finds what it
// implements by type assertion on one value, so this satisfies six of its tracing
// interfaces at once: queries, batches, copies, prepares, connects, and the
// pool's acquires.
//
// That is what replaced eleven hand-written span sites in the facade, and it is
// also why connecting, acquiring from the pool, and the prepares pgx does on its
// own account are traced at all — none of them passes through a facade method,
// so none of them could have been.
//
// Every span it opens is an integration.Operation, so each one is also measured
// into the postgres.operation.duration histogram under the same name.
//
// pgxpool.ReleaseTracer is deliberately not implemented: TraceRelease is handed
// no context, so there is no span to parent a release to and nothing meaningful
// to emit.
//
// It is also where Config.OnWrite is called from: the End hooks are the one place
// that sees every statement's outcome, its command tag, and the connection's
// transaction status once the statement is over.
type tracer struct {
	inst *integration.Instrument

	// onWrite is Config.OnWrite, nil when it is not set.
	onWrite func(ctx context.Context)
}

// Ensure tracer satisfies every pgx tracing interface it claims. pgx discovers
// them by type assertion, so a signature drifting out of shape would otherwise
// fail silently as missing telemetry rather than as a build error.
var (
	_ pgx.QueryTracer       = (*tracer)(nil)
	_ pgx.BatchTracer       = (*tracer)(nil)
	_ pgx.CopyFromTracer    = (*tracer)(nil)
	_ pgx.PrepareTracer     = (*tracer)(nil)
	_ pgx.ConnectTracer     = (*tracer)(nil)
	_ pgxpool.AcquireTracer = (*tracer)(nil)
)

// start opens an operation and returns both the context carrying it, for a later
// end, and the operation itself, so that a caller with attributes to set does not
// have to read back what it just stored.
//
// The operation is nil when ctx was marked by untraced, and ctx is then returned
// as is, so the matching end finds nothing to close.
func (t *tracer) start(ctx context.Context, operation string) (context.Context, *integration.Operation) {
	if isUntraced(ctx) {
		return ctx, nil
	}

	ctx, op := t.inst.StartOperation(ctx, trace.SpanKindClient, operation)

	return context.WithValue(ctx, inFlightKey, op), op
}

// inFlight returns the operation start stored in ctx, if any.
func inFlight(ctx context.Context) (*integration.Operation, bool) {
	op, ok := ctx.Value(inFlightKey).(*integration.Operation)

	return op, ok
}

// recording reports whether op exists and records what is set on it. Attributes
// are built only when it does: a span that is not sampled discards them, and some
// — a sanitized table name, a column list — allocate.
func recording(op *integration.Operation) bool {
	return op != nil && op.IsRecording()
}

// end records err on the operation ctx carries, if any, and ends it.
func (t *tracer) end(ctx context.Context, msg string, err error) {
	op, ok := inFlight(ctx)
	if !ok {
		return
	}

	op.RecordError(msg, err)
	op.End(ctx)
}

// written calls Config.OnWrite with ctx when a statement that succeeded with
// tag, leaving conn in the transaction status it now reports, made the database
// keep something it wrote. See durable.
func (t *tracer) written(ctx context.Context, conn *pgx.Conn, tag pgconn.CommandTag, err error) {
	if t.onWrite == nil || err != nil {
		return
	}

	if durable(tag, txStatus(conn)) {
		t.onWrite(ctx)
	}
}

// txStatus returns the transaction status the server reported on conn at the
// end of its last statement: 'I' outside of a transaction, 'T' inside one, 'E'
// inside one that failed. It is 0, which no statement leaves, without a
// connection to ask.
func txStatus(conn *pgx.Conn) byte {
	if conn == nil {
		return 0
	}

	return conn.PgConn().TxStatus()
}

// durable reports whether a statement that succeeded with tag, leaving its
// connection in txStatus, made the database keep something: it committed a
// transaction, or it wrote rows outside of one, which the server commits as the
// statement ends.
//
// A COMMIT counts whatever the transaction ran. Its statements' tags cannot
// settle it: a SELECT calling a function that writes, or one under a
// data-modifying WITH, reports itself as a SELECT. A COMMIT the server turned
// into a rollback, for a transaction a failed statement had aborted, is tagged
// ROLLBACK, and does not count.
func durable(tag pgconn.CommandTag, txStatus byte) bool {
	if tag.String() == "COMMIT" {
		return true
	}

	return txStatus == 'I' && wroteRows(tag)
}

// wroteRows reports whether tag is that of a statement that inserted, updated,
// deleted, merged or copied at least one row.
func wroteRows(tag pgconn.CommandTag) bool {
	verb, _, _ := strings.Cut(tag.String(), " ")

	switch verb {
	case "INSERT", "UPDATE", "DELETE", "MERGE", "COPY":
		return tag.RowsAffected() > 0
	default:
		return false
	}
}

// batchWrites is what the statements of a batch reported as their results were
// read, for TraceBatchEnd to tell whether the batch as a whole was kept.
//
// A batch runs in one implicit transaction unless it holds transaction control
// statements of its own, so a statement's rows are only kept once the whole batch
// has succeeded, outside of any transaction. A COMMIT among its statements is
// kept as soon as it succeeds, whatever follows it, and a ROLLBACK undoes every
// row written since the last one.
type batchWrites struct {

	// committed is set once a COMMIT among the statements succeeded.
	committed bool

	// wrote is set once a statement wrote rows, and cleared by a ROLLBACK.
	wrote bool

	// failed is set once a statement failed.
	failed bool

	// reported is set once OnWrite was called for the batch, which pgx may end
	// more than once.
	reported bool
}

// record notes what one statement of the batch reported.
func (w *batchWrites) record(data pgx.TraceBatchQueryData) {
	switch {
	case data.Err != nil:
		w.failed = true
	case data.CommandTag.String() == "COMMIT":
		w.committed = true
	case data.CommandTag.String() == "ROLLBACK":
		w.wrote = false
	case wroteRows(data.CommandTag):
		w.wrote = true
	}
}

// durable reports whether the batch, which ended with err and left its
// connection in txStatus, made the database keep something.
func (w *batchWrites) durable(err error, txStatus byte) bool {
	if w.committed {
		return true
	}

	return err == nil && !w.failed && w.wrote && txStatus == 'I'
}

// TraceQueryStart opens the span for a query, an exec, or a row read.
//
// It is also where a transaction's own statements are traced: pgx implements
// Begin, Commit, Rollback and savepoints by running SQL, so they arrive here like
// any other statement, named by whichever facade method issued them.
//
// postgres.query is the statement as the caller wrote it, placeholders included;
// the arguments bound to it are never recorded. Its length is not bounded here:
// the OpenTelemetry SDK's attribute value limit
// (OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT) is what caps it.
func (t *tracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	ctx, op := t.start(ctx, operationFrom(ctx, "Query"))
	if recording(op) {
		op.SetAttributes(attrKeyQuery.String(data.SQL))
	}

	return ctx
}

// TraceQueryEnd closes the query's span, recording how many rows the statement
// returned or affected, as the server's command tag reports it, then tells
// Config.OnWrite when the statement made the database keep something.
//
// For Query and QueryRow this happens when the rows are closed, not when the call
// returns: pgx reads a result set lazily, and the span covers the whole read. A
// caller that never closes its Rows — or never Scans the Row it was handed — is
// already leaking the pooled connection, and now leaks the span with it.
func (t *tracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	if op, ok := inFlight(ctx); ok && data.Err == nil && op.IsRecording() {
		op.SetAttributes(attrKeyRows.Int64(data.CommandTag.RowsAffected()))
	}

	t.end(ctx, "failed to execute query", data.Err)
	t.written(ctx, conn, data.CommandTag, data.Err)
}

// TraceBatchStart opens the span covering a whole batch, and starts noting what
// its statements write when Config.OnWrite is set.
func (t *tracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchStartData) context.Context {
	ctx, op := t.start(ctx, operationFrom(ctx, "Batch"))
	if recording(op) {
		op.SetAttributes(attrKeyBatchSize.Int(data.Batch.Len()))
	}

	if t.onWrite != nil {
		ctx = context.WithValue(ctx, batchWritesKey, &batchWrites{})
	}

	return ctx
}

// TraceBatchQuery is called for each statement of a batch as its result is read.
// A statement that failed is recorded on the batch's span, which marks the batch
// as failed; one that succeeded leaves no trace of its own. There is no child
// span per statement either: a batch is one round trip, and a span per queued
// statement would describe a shape the network never had.
func (t *tracer) TraceBatchQuery(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchQueryData) {
	if op, ok := inFlight(ctx); ok {
		op.RecordError("failed to execute batched query", data.Err)
	}

	if writes, ok := ctx.Value(batchWritesKey).(*batchWrites); ok {
		writes.record(data)
	}
}

// TraceBatchEnd closes the batch's span, then tells Config.OnWrite, once, when
// the batch made the database keep something.
func (t *tracer) TraceBatchEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchEndData) {
	t.end(ctx, "failed to send batch", data.Err)

	writes, ok := ctx.Value(batchWritesKey).(*batchWrites)
	if !ok || writes.reported || !writes.durable(data.Err, txStatus(conn)) {
		return
	}

	writes.reported = true
	t.onWrite(ctx)
}

// TraceCopyFromStart opens the span for a COPY FROM.
func (t *tracer) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromStartData) context.Context {
	ctx, op := t.start(ctx, operationFrom(ctx, "Copy From"))
	if recording(op) {
		op.SetAttributes(
			attrKeyTable.String(data.TableName.Sanitize()),
			attrKeyColumns.StringSlice(data.ColumnNames),
		)
	}

	return ctx
}

// TraceCopyFromEnd closes the COPY FROM's span, recording how many rows landed,
// then tells Config.OnWrite when they were kept.
func (t *tracer) TraceCopyFromEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceCopyFromEndData) {
	if op, ok := inFlight(ctx); ok && op.IsRecording() {
		op.SetAttributes(attrKeyRows.Int64(data.CommandTag.RowsAffected()))
	}

	t.end(ctx, "failed to copy rows", data.Err)
	t.written(ctx, conn, data.CommandTag, data.Err)
}

// TracePrepareStart opens the span for preparing a statement, whether the caller
// asked for one or pgx is filling its statement cache before a query.
func (t *tracer) TracePrepareStart(ctx context.Context, _ *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	ctx, op := t.start(ctx, prepareFrom(ctx))
	if recording(op) {
		op.SetAttributes(
			attrKeyStatement.String(data.Name),
			attrKeyQuery.String(data.SQL),
		)
	}

	return ctx
}

// TracePrepareEnd closes the prepare's span, recording whether the statement was
// already on the connection — pgx caches them, so a hit is the common case and a
// miss is what costs a round trip.
func (t *tracer) TracePrepareEnd(ctx context.Context, _ *pgx.Conn, data pgx.TracePrepareEndData) {
	if op, ok := inFlight(ctx); ok && op.IsRecording() {
		op.SetAttributes(attrKeyAlreadyPrepared.Bool(data.AlreadyPrepared))
	}

	t.end(ctx, "failed to prepare statement", data.Err)
}

// TraceConnectStart opens the span for opening a connection to the server. The
// pool opens them lazily, so these appear on whichever request happened to find
// the pool short of one.
func (t *tracer) TraceConnectStart(ctx context.Context, _ pgx.TraceConnectStartData) context.Context {
	ctx, _ = t.start(ctx, "Connect")

	return ctx
}

// TraceConnectEnd closes the connect's span.
func (t *tracer) TraceConnectEnd(ctx context.Context, data pgx.TraceConnectEndData) {
	t.end(ctx, "failed to connect", data.Err)
}

// TraceAcquireStart opens the span for taking a connection out of the pool.
//
// It is short and uninteresting until it is not: a pool with no free connection
// blocks here, and without this span that wait is invisible — it shows up as a
// query that was slow for no stated reason.
func (t *tracer) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	ctx, _ = t.start(ctx, "Pool / Acquire")

	return ctx
}

// TraceAcquireEnd closes the acquire's span.
func (t *tracer) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	t.end(ctx, "failed to acquire a connection from the pool", data.Err)
}
