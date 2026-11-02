package temporal

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mountayaapp/helix.go/integration"
	internaltrace "github.com/mountayaapp/helix.go/internal/telemetry/trace"
	"github.com/mountayaapp/helix.go/service"

	oteltrace "go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
	sdklog "go.temporal.io/sdk/log"
)

// spanKeyIdentifier is the unique internal type for storing an OTEL Span in
// Temporal workflow and activity contexts.
type spanKeyIdentifier struct{}

// spanCtxKey is the context key the SDK's tracing interceptor stores the span it
// opened under, in workflow and activity contexts alike.
var spanCtxKey spanKeyIdentifier

// tagRenames maps the tag names the Temporal SDK sets on a span to this
// integration's own.
//
// The right-hand side is built from the identifier rather than written out, so
// that this package has one mechanism for naming an attribute instead of two.
var tagRenames = map[string]string{
	"temporalWorkflowID": string(attrKeyWorkflowID),
	"temporalRunID":      string(attrKeyWorkflowRunID),
	"temporalActivityID": string(attrKeyActivityID),
	"temporalUpdateID":   string(attrKeyUpdateID),
}

// operationNameKeys maps each operation the SDK's tracing interceptor opens a
// span for to the attribute its name goes under: the workflow, activity, signal,
// query or update type the operation is about.
//
// The SDK puts that name in the span name — "RunWorkflow:process-order" — and
// nowhere else. Span names here carry no value, so the name is moved to an
// attribute, where it can be filtered on without minting a span name per type.
var operationNameKeys = map[string]string{
	"StartWorkflow":                  string(attrKeyWorkflowType),
	"RunWorkflow":                    string(attrKeyWorkflowType),
	"StartChildWorkflow":             string(attrKeyWorkflowType),
	"SignalWithStartWorkflow":        string(attrKeyWorkflowType),
	"StartActivity":                  string(attrKeyActivityType),
	"RunActivity":                    string(attrKeyActivityType),
	"SignalWorkflow":                 string(attrKeySignalType),
	"SignalExternalWorkflow":         string(attrKeySignalType),
	"SignalChildWorkflow":            string(attrKeySignalType),
	"HandleSignal":                   string(attrKeySignalType),
	"QueryWorkflow":                  string(attrKeyQueryType),
	"HandleQuery":                    string(attrKeyQueryType),
	"UpdateWorkflow":                 string(attrKeyUpdateType),
	"UpdateWithStartWorkflow":        string(attrKeyUpdateType),
	"ValidateUpdate":                 string(attrKeyUpdateType),
	"HandleUpdate":                   string(attrKeyUpdateType),
	"CreateSchedule":                 string(attrKeyScheduleID),
	"StartNexusOperation":            string(attrKeyNexusOperation),
	"RunStartNexusOperationHandler":  string(attrKeyNexusOperation),
	"RunCancelNexusOperationHandler": string(attrKeyNexusOperation),
}

// buildTracer builds the tracer the SDK's tracing interceptor opens its spans
// through, using the Service's own provider and propagator and naming every span
// through inst.
//
// Baggage is disabled on purpose. The contrib tracer otherwise copies whatever
// baggage the calling context holds into the "_tracer-data" header, which Temporal
// persists in workflow history — and on a context serving a request, that baggage
// is whatever the caller put in its headers. What a worker needs from upstream
// travels in the "event" header instead, under the rules customPropagator applies.
func buildTracer(inst *integration.Instrument, svc *service.Service) (interceptor.Tracer, error) {

	// The shared scope, not one of this package's own: every span helix emits
	// carries the same instrumentation scope, so that a backend filtering on it
	// gets all of helix's telemetry and none of anybody else's.
	otelTracer := service.TracerProvider(svc).Tracer(internaltrace.ScopeName)

	// Built once rather than per span. The constant attributes go in at creation,
	// so that a sampler deciding on attributes sees them.
	constant := oteltrace.WithAttributes(inst.AppendAttributes(nil)...)

	names := make(map[string]string, len(operationNameKeys))
	for operation := range operationNameKeys {
		names[operation] = inst.SpanName(humanize(operation))
	}

	t, err := opentelemetry.NewTracer(opentelemetry.TracerOptions{
		Tracer:            otelTracer,
		TextMapPropagator: service.Propagator(svc),
		SpanContextKey:    spanCtxKey,
		DisableBaggage:    true,
		SpanStarter: func(ctx context.Context, tracer oteltrace.Tracer, spanName string, opts ...oteltrace.SpanStartOption) oteltrace.Span {

			// The SDK names a span "<Operation>:<name>", such as
			// "RunWorkflow:process-order". The name is an attribute already — see
			// customTracer.StartSpan — so only the operation is kept, spelled the way
			// span names are spelled here.
			operation, _, _ := strings.Cut(spanName, ":")

			name, ok := names[operation]
			if !ok {
				name = inst.SpanName(humanize(operation))
			}

			// Started through the SDK's tracer rather than the Instrument's: opts
			// carries the span kind and the parent linkage the interceptor worked
			// out, and dropping them would flatten the trace.
			_, span := tracer.Start(ctx, name, append(opts, constant)...)

			return span
		},
	})
	if err != nil {
		return nil, err
	}

	return customTracer{Tracer: t}, nil
}

// customTracer embeds the contrib OpenTelemetry tracer and overrides how its
// spans are tagged and how its loggers are correlated, so that what the SDK emits
// speaks this integration's vocabulary.
type customTracer struct {
	interceptor.Tracer
}

// StartSpan starts and returns a span with the given options, with the SDK's tags
// renamed and the operation's name added as an attribute.
func (t customTracer) StartSpan(opts *interceptor.TracerStartSpanOptions) (interceptor.TracerSpan, error) {

	// Not every operation sets tags: CreateSchedule passes none.
	if opts.Tags == nil {
		opts.Tags = make(map[string]string, 1)
	}

	for old, renamed := range tagRenames {
		if v := opts.Tags[old]; v != "" {
			opts.Tags[renamed] = v
		}

		delete(opts.Tags, old)
	}

	if key, ok := operationNameKeys[opts.Operation]; ok && opts.Name != "" {
		opts.Tags[key] = opts.Name
	}

	return t.Tracer.StartSpan(opts)
}

// GetLogger returns a logger whose lines are correlated with the span ref
// refers to.
//
// The contrib tracer adds TraceID and SpanID key-value pairs, which the Service's
// logger would write as two more fields under names of their own. The span's
// context is handed to customLogger instead, which writes each line with it, so
// that the line carries the trace_id and span_id every other helix line carries.
func (t customTracer) GetLogger(logger sdklog.Logger, ref interceptor.TracerSpanRef) sdklog.Logger {
	span, ok := ref.(oteltrace.Span)
	if !ok {
		return logger
	}

	return sdklog.With(logger, spanContextKey{}, span.SpanContext())
}

// humanize spells an SDK operation the way span names are spelled here, with a
// space between words: "SignalWithStartWorkflow" becomes
// "Signal With Start Workflow".
func humanize(operation string) string {
	var b strings.Builder
	b.Grow(len(operation) + 4)

	prev := utf8.RuneError
	for _, r := range operation {
		if unicode.IsUpper(r) && unicode.IsLower(prev) {
			b.WriteByte(' ')
		}

		b.WriteRune(r)
		prev = r
	}

	return b.String()
}
