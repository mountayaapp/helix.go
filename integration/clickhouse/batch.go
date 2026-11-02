package clickhouse

import (
	"context"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Batch is a batch insert in progress: rows are appended client-side, then sent
// to ClickHouse in a single INSERT.
//
// A batch holds a connection, and the open "ClickHouse: Batch" span, from
// NewBatchInsert until it is sent or aborted, so every batch must end with Send
// or Abort; one that is simply dropped leaks both. Abort does nothing on a batch
// already finished, so deferring it right after NewBatchInsert is a correct
// guard on every path:
//
//	batch, err := db.NewBatchInsert(ctx, "events")
//	if err != nil {
//	  return err
//	}
//	defer batch.Abort(ctx)
//
// A batch is single-use. Once Send has run — whether it succeeded or failed — or
// Abort has, AppendStruct and Send return an error wrapping the driver's
// clickhouse.ErrBatchAlreadySent rather than doing nothing, so that a row is
// never dropped without the caller being told. After a failed Send, start a new
// batch.
//
// A batch does not depend on the context it was begun with: it can be begun in
// one request and sent after that request ended. The context given to each call
// bounds that call.
//
// A batch is not safe for concurrent use.
type Batch interface {

	// AppendStruct appends value to the batch. value is a struct, or a pointer to
	// one, whose fields carry `ch` struct tags naming the table's columns;
	// embedded structs are recursed into. Nothing is sent until Send.
	//
	// An error is recorded on the batch's span. A row the driver could not
	// encode leaves the batch unusable: Send then returns the same error.
	AppendStruct(ctx context.Context, value any) error

	// Send sends the appended rows to ClickHouse and ends the batch, whatever the
	// outcome. Cancelling ctx interrupts the send, which then fails with ctx's
	// error; a ctx already done fails it without sending anything.
	Send(ctx context.Context) error

	// Abort discards the batch without sending it, releasing its connection and
	// ending its span. It does nothing, and returns nil, on a batch already sent
	// or aborted.
	Abort(ctx context.Context) error
}

// batchState is where a batch is in its life. Every state but batchOpen is final.
type batchState int

const (
	batchOpen batchState = iota
	batchSent
	batchFailed
	batchAborted
)

// batch implements the Batch interface and allows to wrap the ClickHouse batch
// functions for automatic tracing and error recording.
type batch struct {
	inst    *integration.Instrument
	client  driver.Batch
	profile *profile
	table   attribute.KeyValue

	// span is the batch's own span, open from NewBatchInsert until Send or Abort
	// ends it. parent is the same span as OpenTelemetry knows it: each call's
	// span is opened under it, in the context the caller passed to that call, so
	// that every span describing one batch sits in one place in the trace while
	// still carrying the caller's own context values.
	span   *trace.Span
	parent oteltrace.Span

	// cancel cancels the context the driver holds for the batch. That context is
	// detached from the one the batch was begun with; NewBatchInsert and Send
	// each forward their own ctx's cancellation to it for as long as their round
	// trip runs, and the batch ending releases it.
	cancel context.CancelCauseFunc

	// state tells a batch that is still open from one that is finished, and why.
	// A batch is not safe for concurrent use — neither is the driver's — so a
	// plain field is enough.
	state batchState
}

// AppendStruct implements Batch.
func (b *batch) AppendStruct(ctx context.Context, value any) error {
	if err := b.finished(); err != nil {
		return err
	}

	err := b.client.AppendStruct(value)
	b.span.RecordError("failed to append struct to batch", err)

	return err
}

// Send implements Batch.
func (b *batch) Send(ctx context.Context) error {
	if err := b.finished(); err != nil {
		return err
	}

	ctx, op := b.inst.StartOperation(oteltrace.ContextWithSpan(ctx, b.parent), trace.SpanKindClient, "Batch / Send")
	op.SetAttributes(b.table)

	err := ctx.Err()
	if err == nil {
		stop := context.AfterFunc(ctx, func() {
			b.cancel(context.Cause(ctx))
		})

		err = b.client.Send()

		// The driver reports a send it was interrupted in as context.Canceled,
		// its own context's error; ctx's is the one the caller can recognise.
		if !stop() && err != nil {
			err = ctx.Err()
		}
	}

	// Send releases the connection on its own, whatever its outcome — except when
	// it never ran. Abort releases it then, and is a no-op otherwise.
	if err != nil {
		_ = b.client.Abort()
	}

	b.profile.stamp(op.Span)
	op.RecordError("failed to send batch", err)
	op.End(ctx)

	b.state = batchSent
	if err != nil {
		b.state = batchFailed
		b.span.RecordError("failed to send batch", err)
	}

	b.end()

	return err
}

// Abort implements Batch.
//
// It exists because Send was the only way to end a batch: a caller that hit an
// error partway through appending and returned had no way to give the connection
// back, and left behind a span that was never ended and therefore never exported.
func (b *batch) Abort(ctx context.Context) error {
	if b.state != batchOpen {
		return nil
	}

	ctx, op := b.inst.StartOperation(oteltrace.ContextWithSpan(ctx, b.parent), trace.SpanKindClient, "Batch / Abort")
	op.SetAttributes(b.table)

	err := b.client.Abort()
	op.RecordError("failed to abort batch", err)
	op.End(ctx)

	b.state = batchAborted
	b.end()

	return err
}

// end ends the batch's span and releases the driver's context, once the batch
// is finished.
func (b *batch) end() {
	b.cancel(nil)
	b.span.End()
}

// finished returns the error a call on a finished batch returns, and nil while
// the batch is open.
//
// It wraps the driver's own ErrBatchAlreadySent, which is what the driver
// returns for an append on a batch it considers sent — as it does an aborted
// one — so that errors.Is recognises it whichever of the two produced it.
func (b *batch) finished() error {
	switch b.state {
	case batchSent:
		return errorstack.Wrap(clickhouse.ErrBatchAlreadySent, "Batch has already been sent")
	case batchFailed:
		return errorstack.Wrap(clickhouse.ErrBatchAlreadySent, "Batch has already been sent and failed; start a new batch")
	case batchAborted:
		return errorstack.Wrap(clickhouse.ErrBatchAlreadySent, "Batch has already been aborted")
	default:
		return nil
	}
}
