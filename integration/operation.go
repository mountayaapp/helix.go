package integration

import (
	"context"
	"time"

	"github.com/mountaya/helix.go/telemetry/trace"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

// Attribute keys an operation's measurement is recorded under. They are suffixes
// rather than keys because each integration namespaces them with its own
// identifier, the same way the HTTP ones are.
const (
	suffixOperation         = "operation"
	suffixOutcome           = "outcome"
	suffixOperationDuration = "operation.duration"
)

// Outcomes an operation can be measured under. Two values, so that an error rate
// is a ratio of one series to another rather than a join across two metrics.
const (
	outcomeOK    = "ok"
	outcomeError = "error"
)

// Operation is one traced and measured unit of work an integration performs: a
// query, a blob read, a cache write.
//
// It is a span that also feeds a duration histogram, and it exists because the
// two were not the same thing before: every integration opened spans through the
// Instrument and none of them recorded a measurement, so a service running nine
// integrations had one vocabulary in its traces and nothing at all in its
// metrics. Using it is the same shape as using a span directly.
//
// It embeds the span, so SetAttributes, IsRecording and AddEvent are the ones
// documented on trace.Span. RecordError and End are the two it takes over: the
// first so that a failure is visible in the measurement as well as on the span,
// the second so that ending the operation is what records it.
type Operation struct {
	*trace.Span

	inst    *Instrument
	name    string
	started time.Time
	failed  bool
	ended   bool
}

// StartOperation opens the span "<Humanized>: <operation>" and begins measuring
// it. attrs are stamped on the span at creation, as with Start; they are not part
// of the measurement. The returned Operation must be ended by the caller:
//
//	ctx, op := inst.StartOperation(ctx, trace.SpanKindClient, "Get")
//	defer op.End(ctx)
func (i *Instrument) StartOperation(
	ctx context.Context, kind trace.SpanKind, operation string, attrs ...attribute.KeyValue,
) (context.Context, *Operation) {
	ctx, span := i.Start(ctx, kind, operation, attrs...)

	return ctx, &Operation{
		Span:    span,
		inst:    i,
		name:    operation,
		started: time.Now(),
	}
}

// RecordError records the error as an exception on the span, sets the span status
// to error, and marks the measurement as a failure. A nil error does nothing, so
// it can be called unconditionally.
func (o *Operation) RecordError(msg string, err error) {
	if err == nil {
		return
	}

	o.failed = true
	o.Span.RecordError(msg, err)
}

// End closes the span and records how long the operation took, under the
// integration's identity, the operation's name and its outcome.
//
// ctx is the one StartOperation returned, so that the measurement carries an
// exemplar pointing back at the span that produced it.
//
// Ending twice is a no-op, matching OpenTelemetry's contract for Span.End. That
// matters for a caller holding an operation open across several methods, where
// the natural shape is a deferred guard alongside an explicit close.
func (o *Operation) End(ctx context.Context) {
	if o.ended {
		return
	}

	o.ended = true
	o.Span.End()

	outcome := outcomeOK
	if o.failed {
		outcome = outcomeError
	}

	attrs := make([]attribute.KeyValue, 0, len(o.inst.identity)+2)
	attrs = o.inst.AppendIdentity(attrs)
	attrs = append(attrs,
		o.inst.keyOperation.String(o.name),
		o.inst.keyOutcome.String(outcome),
	)

	o.inst.operations.Record(ctx, time.Since(o.started).Seconds(), otelmetric.WithAttributes(attrs...))
}

// newOperationHistogram returns the instrument every Operation of i is measured
// into. A provider that rejects it hands back a working no-op, so the error is
// dropped rather than turned into a construction failure over telemetry.
func newOperationHistogram(meter otelmetric.Meter, keyPrefix, humanized string) otelmetric.Float64Histogram {
	histogram, _ := meter.Float64Histogram(keyPrefix+suffixOperationDuration,
		otelmetric.WithUnit("s"),
		otelmetric.WithDescription("Duration of "+humanized+" operations."),
		otelmetric.WithExplicitBucketBoundaries(durationBuckets...),
	)

	return histogram
}
