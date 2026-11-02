package integration

import (
	"context"

	"github.com/mountayaapp/helix.go/errorstack"
	internaltrace "github.com/mountayaapp/helix.go/internal/telemetry/trace"
	"github.com/mountayaapp/helix.go/telemetry/trace"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Key builds the attribute key "<identifier>.<suffix>", the only shape a helix
// attribute key takes.
//
// Integrations declare their keys as package-level variables so the concatenation
// is paid once at init rather than on every span:
//
//	var attrKeyQuery = integration.Key(identifier, "query")
func Key(identifier, suffix string) attribute.Key {
	return attribute.Key(identifier + "." + suffix)
}

// InstrumentConfig describes the integration an Instrument speaks for.
type InstrumentConfig struct {

	// Identifier is the integration's unique identifier, and the namespace every
	// attribute key it emits is built from: "postgres", "rest", "bucket".
	//
	// Required.
	Identifier string

	// Humanized is the integration's human-readable name, and the first half of
	// every span name it opens: "PostgreSQL", "REST", "Bucket".
	//
	// Required.
	Humanized string

	// Name is what the application called this instance at Attach or Serve time.
	// It is stamped on every span as "<identifier>.name", so that two pools of the
	// same kind are told apart in a trace the way they already are in health
	// output. Defaults to Identifier when left empty.
	Name string

	// Attributes are stamped on every span the Instrument opens, alongside the
	// name. They are the config-derived facts that do not change for the life of
	// the connection — a database, a bucket, a server address — which each
	// integration used to re-set on every span through a helper of its own.
	//
	// Build their keys with Key so they share the integration's namespace.
	Attributes []attribute.KeyValue

	// TracerProvider supplies the tracer. Integrations get it from
	// service.TracerProvider. A nil provider falls back to the globally registered
	// one, so an integration built without a Service still emits.
	TracerProvider oteltrace.TracerProvider

	// MeterProvider supplies the meter. Integrations get it from
	// service.MeterProvider. A nil provider falls back to the globally registered
	// one.
	MeterProvider otelmetric.MeterProvider

	// Propagator carries trace context across process boundaries. Integrations get
	// it from service.Propagator. A nil propagator falls back to the globally
	// registered one.
	//
	// It is carried here rather than read from the global registry at each call
	// because a Service built with Config.SkipGlobals leaves that registry holding
	// an empty propagator, which extracts and injects nothing.
	Propagator propagation.TextMapPropagator
}

// Instrument identifies an integration to the telemetry pipeline. It is the one
// place a span name or an attribute key is built, so that nine integrations
// cannot drift into nine conventions.
//
// The convention it enforces is not new — it is what the integrations already did
// by hand, in six differently-named helpers: a span is named
// "<Humanized>: <Operation>" and an attribute key is "<identifier>.<path>". What
// is new is that there is one implementation of it, which a third-party
// integration can reach for too, and a test — servicetest.AssertConvention — that
// can check the result.
//
// An Instrument is immutable after construction and safe for concurrent use.
type Instrument struct {
	identifier string
	humanized  string
	name       string

	// keyPrefix is "<identifier>." and spanPrefix is "<Humanized>: ", both
	// pre-built so that neither Key nor Start pays for a three-way concatenation.
	keyPrefix  string
	spanPrefix string

	// attributes are stamped on every span at creation time rather than after it,
	// so a sampler that looks at attributes sees them.
	attributes []attribute.KeyValue

	// identity is the subset of attributes that belongs on an instrument as well
	// as on a span: the name, and nothing whose cardinality a time series could
	// not carry. Without it two servers of the same kind write the same metric
	// under indistinguishable series.
	identity []attribute.KeyValue

	tracer     oteltrace.Tracer
	meter      otelmetric.Meter
	propagator propagation.TextMapPropagator

	// keyOperation and keyOutcome are the attribute keys an Operation's
	// measurement is recorded under, built once rather than on every End.
	keyOperation attribute.Key
	keyOutcome   attribute.Key

	// operations is the histogram every Operation this Instrument starts is
	// measured into. It is built here rather than per operation because an
	// instrument is registered once, and it is built for every integration —
	// including the ones that never start an Operation — because an instrument
	// nothing records into exports nothing.
	operations otelmetric.Float64Histogram
}

// NewInstrument builds the Instrument an integration emits all of its telemetry
// through.
//
// Integrations create exactly one, at connection or server construction time, and
// hold it for the life of the connection.
//
// Returns an error when Identifier or Humanized is empty. They are what every
// span name and every attribute key is built from, so an empty one does not
// degrade the telemetry, it produces a vocabulary — ".name", ": Exec" — that no
// backend and no other integration shares. Reporting it here is what gives a
// third-party integration the signal servicetest.AssertConvention gives the ones
// in this repository.
func NewInstrument(cfg InstrumentConfig) (*Instrument, error) {
	var entries []errorstack.Entry
	if cfg.Identifier == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"instrument", "identifier"},
		})
	}

	if cfg.Humanized == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"instrument", "humanized"},
		})
	}

	if len(entries) > 0 {
		return nil, errorstack.NewValidation(entries...)
	}

	name := cfg.Name
	if name == "" {
		name = cfg.Identifier
	}

	tracerProvider := cfg.TracerProvider
	if tracerProvider == nil {
		tracerProvider = otel.GetTracerProvider()
	}

	meterProvider := cfg.MeterProvider
	if meterProvider == nil {
		meterProvider = otel.GetMeterProvider()
	}

	propagator := cfg.Propagator
	if propagator == nil {
		propagator = otel.GetTextMapPropagator()
	}

	i := &Instrument{
		identifier: cfg.Identifier,
		humanized:  cfg.Humanized,
		name:       name,
		keyPrefix:  cfg.Identifier + ".",
		spanPrefix: cfg.Humanized + ": ",
		tracer:     tracerProvider.Tracer(internaltrace.ScopeName),
		meter:      meterProvider.Meter(internaltrace.ScopeName),
		propagator: propagator,
	}

	i.keyOperation = i.Key(suffixOperation)
	i.keyOutcome = i.Key(suffixOutcome)
	i.operations = newOperationHistogram(i.meter, i.keyPrefix, i.humanized)

	// The name goes first so that it is the attribute a reader sees at the top of
	// every span, and is copied rather than appended to the caller's slice, which
	// the caller still owns.
	i.attributes = make([]attribute.KeyValue, 0, len(cfg.Attributes)+1)
	i.attributes = append(i.attributes, i.Key("name").String(name))
	i.attributes = append(i.attributes, cfg.Attributes...)
	i.identity = i.attributes[:1:1]

	return i, nil
}

// Identifier returns the integration's unique identifier.
func (i *Instrument) Identifier() string { return i.identifier }

// Humanized returns the integration's human-readable name.
func (i *Instrument) Humanized() string { return i.humanized }

// Name returns the name the application gave this instance.
func (i *Instrument) Name() string { return i.name }

// Meter returns the integration's OpenTelemetry meter, for building the
// instruments it records. There is no matching Tracer accessor on purpose: a span
// is created through Start, which is what makes the naming convention hold.
func (i *Instrument) Meter() otelmetric.Meter { return i.meter }

// AppendIdentity appends the attributes that identify this instance — today, its
// name — to dst, in the manner of the standard library's append.
//
// It is what an instrument's measurements are keyed on, and it is deliberately
// narrower than AppendAttributes: everything in it has to be safe to put on a
// time series, so a config-derived fact with real cardinality stays on spans
// only. Record every measurement with it, or two servers of the same kind write
// the same metric under series nothing can tell apart.
func (i *Instrument) AppendIdentity(dst []attribute.KeyValue) []attribute.KeyValue {
	return append(dst, i.identity...)
}

// Key builds the attribute key "<identifier>.<suffix>".
func (i *Instrument) Key(suffix string) attribute.Key {
	return attribute.Key(i.keyPrefix + suffix)
}

// SpanName returns "<Humanized>: <operation>", the name Start would have given a
// span.
//
// It exists for the one integration whose spans are created by instrumentation it
// does not own: the Temporal SDK's tracing interceptor makes the span, because
// replay-safe span identifiers and header propagation are correctness rather than
// naming, so there is no Start call of ours to name it.
func (i *Instrument) SpanName(operation string) string {
	return i.spanPrefix + operation
}

// AppendAttributes appends the attributes Start stamps on every span — the name,
// and whatever InstrumentConfig.Attributes declared as constant — to dst and
// returns the extended slice, in the manner of the standard library's append.
//
// Like SpanName, it is for a span this Instrument did not open.
func (i *Instrument) AppendAttributes(dst []attribute.KeyValue) []attribute.KeyValue {
	return append(dst, i.attributes...)
}

// Start opens the span "<Humanized>: <operation>", pre-stamped with the
// integration's identity: its name, and whatever InstrumentConfig.Attributes
// declared as constant for the connection.
//
// # Naming an operation
//
// Two separators, and they mean different things:
//
//   - " / " separates a scope from the operation inside it — "Pool / Acquire",
//     "Transaction / Commit", "Batch / Send". The left side is where the work
//     happened, the right side is what happened.
//   - A plain space is part of the operation's own name, because the thing being
//     named is spelled that way — "Copy From", "Async Insert", "GET /things/:id".
//
// Both are TitleCase, and neither carries a value: an identifier, a key or a
// concrete path in a span name mints a new name per request, which is what makes
// a trace view unusable. Those belong in attributes.
//
// attrs are stamped on the span at creation, alongside the integration's identity:
// pass what is known before the work starts — a key, a table — here rather than
// setting it afterwards, so that a sampler deciding on attributes sees it.
//
// The returned Span must be ended by the caller, as with any span.
func (i *Instrument) Start(
	ctx context.Context, kind trace.SpanKind, operation string, attrs ...attribute.KeyValue,
) (context.Context, *trace.Span) {
	if len(attrs) == 0 {
		return i.start(ctx, kind, operation)
	}

	return i.start(ctx, kind, operation, oteltrace.WithAttributes(attrs...))
}

// start is Start with additional span options, for the callers in this package
// that need one: a public server starting a new root linked to its caller.
func (i *Instrument) start(
	ctx context.Context, kind trace.SpanKind, operation string, extra ...oteltrace.SpanStartOption,
) (context.Context, *trace.Span) {
	opts := make([]oteltrace.SpanStartOption, 0, len(extra)+2)
	opts = append(opts, oteltrace.WithSpanKind(kind), oteltrace.WithAttributes(i.attributes...))
	opts = append(opts, extra...)

	ctx, span := i.tracer.Start(ctx, i.spanPrefix+operation, opts...)

	return ctx, trace.NewSpan(span)
}

// StartEntry opens the entry span of a trace — the span a server integration
// creates when it accepts work — and, in addition to what Start does, stamps the
// attributes of the Event carried by ctx onto it and merges the event's
// propagated identifiers into the context's baggage.
//
// Entry spans are the one place the Event belongs: child spans inherit the trace
// without paying to repeat some thirty attributes.
func (i *Instrument) StartEntry(ctx context.Context, kind trace.SpanKind, operation string) (context.Context, *trace.Span) {
	ctx, span := i.Start(ctx, kind, operation)

	return trace.StampEvent(ctx), span
}
