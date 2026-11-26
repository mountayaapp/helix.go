package clickhouse

import (
	"context"
	"errors"
	"testing"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/service/servicetest"
	"github.com/mountaya/helix.go/telemetry/trace"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// fakeBatch stands in for the driver's batch, counting what reaches it. It embeds
// the interface, so any method it does not override panics.
type fakeBatch struct {
	driver.Batch

	appendErr error
	sendErr   error

	appended []any
	sends    int
	aborts   int
}

func (b *fakeBatch) AppendStruct(v any) error {
	if b.appendErr != nil {
		return b.appendErr
	}

	b.appended = append(b.appended, v)
	return nil
}

func (b *fakeBatch) Send() error {
	b.sends++
	return b.sendErr
}

func (b *fakeBatch) Abort() error {
	b.aborts++
	return nil
}

// newTestBatch returns a batch over client, as NewBatchInsert would after a
// successful begin, with its spans recorded by the returned Recorder.
func newTestBatch(t *testing.T, client driver.Batch) (*batch, *servicetest.Recorder) {
	t.Helper()

	svc, rec := servicetest.New(t)
	inst := testInstrument(t, svc, "primary")

	ctx, span := inst.Start(t.Context(), trace.SpanKindInternal, "Batch")
	_, cancel := context.WithCancelCause(context.WithoutCancel(ctx))

	return &batch{
		inst:    inst,
		client:  client,
		profile: &profile{},
		table:   attrKeyTable.String("events"),
		span:    span,
		parent:  oteltrace.SpanFromContext(ctx),
		cancel:  cancel,
	}, rec
}

// spanNamed returns the ended span called name.
func spanNamed(tb testing.TB, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	tb.Helper()

	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}

	tb.Fatalf("no span named %q among %d", name, len(spans))
	return nil
}

// spanNames returns the names of the ended spans, in the order they ended.
func spanNames(spans []sdktrace.ReadOnlySpan) []string {
	names := make([]string, 0, len(spans))
	for _, span := range spans {
		names = append(names, span.Name())
	}

	return names
}

// assertFinished asserts that err is what a call on a finished batch returns.
func assertFinished(tb testing.TB, err error, message string) {
	tb.Helper()

	require.Error(tb, err)
	assert.ErrorIs(tb, err, clickhouse.ErrBatchAlreadySent)

	entries := errorstack.EntriesOf(err)
	require.Len(tb, entries, 1)
	assert.Equal(tb, message, entries[0].Message)
}

func TestBatch_Send(t *testing.T) {
	client := &fakeBatch{}
	b, rec := newTestBatch(t, client)

	require.NoError(t, b.AppendStruct(t.Context(), struct{}{}))
	require.NoError(t, b.Send(t.Context()))
	assert.Equal(t, 1, client.sends)
	assert.Zero(t, client.aborts)

	spans := rec.Spans()
	assert.Equal(t, []string{"ClickHouse: Batch / Send", "ClickHouse: Batch"}, spanNames(spans))

	parent := spanNamed(t, spans, "ClickHouse: Batch")
	send := spanNamed(t, spans, "ClickHouse: Batch / Send")
	assert.Equal(t, parent.SpanContext().SpanID(), send.Parent().SpanID())
	assert.Equal(t, codes.Unset, parent.Status().Code)
	assert.Equal(t, codes.Unset, send.Status().Code)
}

// TestBatch_SendAfterAbort is the regression test for a Send on an aborted batch
// returning nil: the caller was told its rows were sent when none had been.
func TestBatch_SendAfterAbort(t *testing.T) {
	client := &fakeBatch{}
	b, rec := newTestBatch(t, client)

	require.NoError(t, b.AppendStruct(t.Context(), struct{}{}))
	require.NoError(t, b.Abort(t.Context()))

	assertFinished(t, b.Send(t.Context()), "Batch has already been aborted")
	assertFinished(t, b.AppendStruct(t.Context(), struct{}{}), "Batch has already been aborted")
	assert.Zero(t, client.sends)
	assert.Equal(t, 1, client.aborts)
	assert.Len(t, client.appended, 1)

	assert.Equal(t, []string{"ClickHouse: Batch / Abort", "ClickHouse: Batch"}, spanNames(rec.Spans()))
}

// TestBatch_SendAfterFailedSend is the regression test for a second Send after a
// failed one returning nil, which read as the retry having succeeded.
func TestBatch_SendAfterFailedSend(t *testing.T) {
	client := &fakeBatch{sendErr: errors.New("code: 241, message: memory limit exceeded")}
	b, rec := newTestBatch(t, client)

	err := b.Send(t.Context())
	require.ErrorIs(t, err, client.sendErr)

	assertFinished(t, b.Send(t.Context()), "Batch has already been sent and failed; start a new batch")
	assert.Equal(t, 1, client.sends)

	// The connection is released on a failed send, and the failure is on both
	// the send and the batch.
	assert.Equal(t, 1, client.aborts)

	spans := rec.Spans()
	assert.Equal(t, codes.Error, spanNamed(t, spans, "ClickHouse: Batch / Send").Status().Code)
	assert.Equal(t, codes.Error, spanNamed(t, spans, "ClickHouse: Batch").Status().Code)

	// A deferred Abort after the failed Send does nothing.
	require.NoError(t, b.Abort(t.Context()))
	assert.Equal(t, 1, client.aborts)
	assert.Len(t, rec.Spans(), 2)
}

func TestBatch_SendTwice(t *testing.T) {
	client := &fakeBatch{}
	b, _ := newTestBatch(t, client)

	require.NoError(t, b.Send(t.Context()))
	assertFinished(t, b.Send(t.Context()), "Batch has already been sent")
	assertFinished(t, b.AppendStruct(t.Context(), struct{}{}), "Batch has already been sent")
	assert.Equal(t, 1, client.sends)
}

// TestBatch_AbortAfterSend pins that the deferred guard is free: no span, no call
// reaching the driver.
func TestBatch_AbortAfterSend(t *testing.T) {
	client := &fakeBatch{}
	b, rec := newTestBatch(t, client)

	require.NoError(t, b.Send(t.Context()))
	require.NoError(t, b.Abort(t.Context()))
	require.NoError(t, b.Abort(t.Context()))

	assert.Zero(t, client.aborts)
	assert.Equal(t, []string{"ClickHouse: Batch / Send", "ClickHouse: Batch"}, spanNames(rec.Spans()))
}

// TestBatch_AppendFailure pins where an append failure is recorded: on the span
// standing for the batch, and not on the begin, which succeeded.
func TestBatch_AppendFailure(t *testing.T) {
	client := &fakeBatch{appendErr: errors.New("clickhouse [AppendRow]: converting string to UInt32 is unsupported")}
	b, rec := newTestBatch(t, client)

	err := b.AppendStruct(t.Context(), struct{}{})
	require.ErrorIs(t, err, client.appendErr)

	// The batch is still open: the caller decides whether to go on or abort.
	require.NoError(t, b.Abort(t.Context()))
	assert.Equal(t, 1, client.aborts)

	spans := rec.Spans()
	batch := spanNamed(t, spans, "ClickHouse: Batch")
	assert.Equal(t, codes.Error, batch.Status().Code)
	assert.Equal(t, "failed to append struct to batch", batch.Status().Description)
	assert.Equal(t, codes.Unset, spanNamed(t, spans, "ClickHouse: Batch / Abort").Status().Code)
}

// TestBatch_SendWithDoneContext pins that a ctx already done fails the send
// without sending, and still releases the connection.
func TestBatch_SendWithDoneContext(t *testing.T) {
	client := &fakeBatch{}
	b, _ := newTestBatch(t, client)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, b.Send(ctx), context.Canceled)
	assert.Zero(t, client.sends)
	assert.Equal(t, 1, client.aborts)

	assertFinished(t, b.Send(t.Context()), "Batch has already been sent and failed; start a new batch")
}

// TestBatch_SpansLiveInTheCallersContext pins that each call's span is parented
// under the batch, whatever span the context the caller passed holds.
func TestBatch_SpansLiveInTheCallersContext(t *testing.T) {
	client := &fakeBatch{}
	b, rec := newTestBatch(t, client)

	callerCtx, caller := b.inst.Start(t.Context(), trace.SpanKindServer, "Caller")
	require.NoError(t, b.Send(callerCtx))
	caller.End()

	spans := rec.Spans()
	send := spanNamed(t, spans, "ClickHouse: Batch / Send")
	assert.Equal(t, spanNamed(t, spans, "ClickHouse: Batch").SpanContext().SpanID(), send.Parent().SpanID())
	assert.NotEqual(t, spanNamed(t, spans, "ClickHouse: Caller").SpanContext().SpanID(), send.Parent().SpanID())
}
