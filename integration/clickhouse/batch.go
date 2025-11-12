package clickhouse

import (
	"context"
	"fmt"

	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

/*
batch implements the Batch interface and allows to wrap the ClickHouse batch
functions for automatic tracing and error recording.
*/
type batch struct {
	config     *Config
	client     driver.Batch
	parentSpan *trace.Span
}

/*
Batch represents a database batch, holding the parent span triggering the batch.
*/
type Batch interface {
	AppendStruct(ctx context.Context, value any) error
	Send(ctx context.Context) error
}

/*
AppendStruct append a new entry into the batch in progress.

It automatically handles error recording.
*/
func (b *batch) AppendStruct(ctx context.Context, value any) error {
	err := b.client.AppendStruct(value)
	if err != nil {
		b.parentSpan.RecordError("failed to append struct to batch", err)
	}

	return err
}

/*
Send sends the batch to ClickHouse. It also properly closes the batch as well as
the spans tied to the batch.

It automatically handles tracing and error recording.
*/
func (b *batch) Send(ctx context.Context) error {
	defer b.parentSpan.End()

	_, span := trace.Start(ctx, trace.SpanKindClient, fmt.Sprintf("%s: Batch / Send", humanized))
	defer span.End()

	defer b.client.Close()

	err := b.client.Send()
	if err != nil {
		span.RecordError("failed to send batch", err)
	}

	setDefaultAttributes(span, b.config)

	return err
}
