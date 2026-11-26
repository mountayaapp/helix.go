package servicetest

import (
	"context"
	"testing"

	"github.com/mountaya/helix.go/service"
	"github.com/mountaya/helix.go/telemetry/log"
	"github.com/mountaya/helix.go/telemetry/trace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestNew_ReturnsAUsableService(t *testing.T) {
	svc, rec := New(t)

	require.NotNil(t, svc)
	require.NotNil(t, rec)
	assert.Empty(t, rec.Spans(), "a fresh Recorder has nothing in it")
}

func TestRecorder_RecordsSpansFromTheHelixTracer(t *testing.T) {
	svc, rec := New(t)

	ctx := service.Context(t.Context(), svc)
	_, span := trace.Start(ctx, trace.SpanKindClient, "Probe: Do")
	span.End()

	spans := rec.Spans()
	require.Len(t, spans, 1)
	assert.Equal(t, "Probe: Do", spans[0].Name())
}

// TestRecorder_RecordsSpansFromTheGlobalProvider pins why New installs globals.
// Instrumentation helix does not wire - gocloud.dev/blob, an otelhttp handler
// built without an explicit provider - reaches for otel.Tracer, and a Recorder
// that missed it would report less than the service actually emits.
func TestRecorder_RecordsSpansFromTheGlobalProvider(t *testing.T) {
	_, rec := New(t)

	_, span := otel.Tracer("third-party/instrumentation").Start(t.Context(), "SomeoneElse: Op")
	span.End()

	spans := rec.Spans()
	require.Len(t, spans, 1)
	assert.Equal(t, "SomeoneElse: Op", spans[0].Name())
	assert.Equal(t, "third-party/instrumentation", spans[0].InstrumentationScope().Name)
}

// TestRecorder_RecordsLogsFromTheGlobalProvider is the log counterpart of the span
// test above: a log bridge helix does not wire reaches for the global
// LoggerProvider, which New installs just as it installs the tracer provider.
func TestRecorder_RecordsLogsFromTheGlobalProvider(t *testing.T) {
	_, rec := New(t)

	var record otellog.Record
	record.SetBody(attribute.StringValue("someone else's message"))
	otel.Logger("third-party/instrumentation").Emit(t.Context(), record)

	var scope string
	for _, r := range rec.Records() {
		if r.Body().AsString() == "someone else's message" {
			scope = r.InstrumentationScope().Name
		}
	}
	assert.Equal(t, "third-party/instrumentation", scope, "Records() did not contain the emitted message")
}

// TestRecorder_RecordsTelemetryOutsideAServiceContext pins the fallback the
// telemetry/log and telemetry/trace packages share: a context service.Context never
// enriched — one built in main, or detached for background work — still reaches
// the Service, through the defaults New installs.
func TestRecorder_RecordsTelemetryOutsideAServiceContext(t *testing.T) {
	_, rec := New(t)

	_, span := trace.Start(t.Context(), trace.SpanKindInternal, "Probe: Background")
	span.End()
	log.Info(t.Context(), "background message")

	spans := rec.Spans()
	require.Len(t, spans, 1)
	assert.Equal(t, "Probe: Background", spans[0].Name())

	var found bool
	for _, r := range rec.Records() {
		if r.Body().AsString() == "background message" {
			found = true
		}
	}
	assert.True(t, found, "Records() did not contain the emitted message")
}

func TestRecorder_RecordsLogs(t *testing.T) {
	svc, rec := New(t)

	ctx := service.Context(t.Context(), svc)
	log.Info(ctx, "probe message", log.String("probe_key", "probe_value"))

	records := rec.Records()
	require.NotEmpty(t, records, "the logger must reach the recording provider")

	var found bool
	for _, r := range records {
		if r.Body().AsString() == "probe message" {
			found = true
		}
	}
	assert.True(t, found, "Records() did not contain the emitted message")
}

// TestRecorder_LogsAreCorrelatedWithTheirSpan pins that an exported record carries
// the trace and span it was logged under natively, rather than as two string
// attributes a backend cannot join on.
func TestRecorder_LogsAreCorrelatedWithTheirSpan(t *testing.T) {
	svc, rec := New(t)

	ctx, span := trace.Start(service.Context(t.Context(), svc), trace.SpanKindInternal, "Probe: Correlate")
	log.Info(ctx, "correlated message")
	span.End()

	spans := rec.Spans()
	require.Len(t, spans, 1)

	var record *sdklog.Record
	for _, r := range rec.Records() {
		if r.Body().AsString() == "correlated message" {
			record = &r
		}
	}
	require.NotNil(t, record, "Records() did not contain the emitted message")

	assert.Equal(t, spans[0].SpanContext().TraceID(), record.TraceID())
	assert.Equal(t, spans[0].SpanContext().SpanID(), record.SpanID())
	record.WalkAttributes(func(kv attribute.KeyValue) bool {
		assert.NotContains(t, []string{"trace_id", "span_id", "context"}, string(kv.Key),
			"correlation belongs on the record, not in its attributes")
		return true
	})
}

// TestRecorder_LogsBelowTheLevelAreNotExported pins that the level gates the
// export path as well as stderr: the bridge has no level of its own.
func TestRecorder_LogsBelowTheLevelAreNotExported(t *testing.T) {
	t.Setenv("OTEL_LOG_LEVEL", "info")
	svc, rec := New(t)

	ctx := service.Context(t.Context(), svc)
	log.Debug(ctx, "debug message")
	log.Info(ctx, "info message")

	var bodies []string
	for _, r := range rec.Records() {
		bodies = append(bodies, r.Body().AsString())
	}
	assert.Contains(t, bodies, "info message")
	assert.NotContains(t, bodies, "debug message")
}

func TestRecorder_RecordsMetrics(t *testing.T) {
	_, rec := New(t)

	counter, err := otel.Meter("probe").Int64Counter("probe.calls")
	require.NoError(t, err)
	counter.Add(t.Context(), 1)

	metrics := rec.Metrics()
	require.NotEmpty(t, metrics)

	var names []string
	for _, m := range metrics {
		names = append(names, m.Name)
	}
	assert.Contains(t, names, "probe.calls")
}

// TestRecorder_ReadableAfterStop pins the ownership rule servicetest depends on:
// providers supplied through Config are not the Service's to shut down, so the
// Recorder survives teardown and a test can assert on shutdown telemetry.
func TestRecorder_ReadableAfterStop(t *testing.T) {
	svc, rec := New(t)

	ctx := service.Context(t.Context(), svc)
	_, span := trace.Start(ctx, trace.SpanKindClient, "Probe: BeforeStop")
	span.End()

	require.NoError(t, svc.Stop(context.Background()))

	assert.Len(t, rec.Spans(), 1, "spans recorded before Stop are still readable after it")
	assert.Empty(t, rec.Metrics(), "the manual reader outlives the Service and reads cleanly")
}

func TestSummarize_KeepsShapeAndDropsValues(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	_, span := tp.Tracer("scope/x").Start(context.Background(), "Op",
		oteltrace.WithSpanKind(oteltrace.SpanKindClient),
		oteltrace.WithAttributes(
			attribute.String("zeta", "/tmp/whatever-changes-every-run"),
			attribute.Int("alpha", 42),
		),
	)
	span.End()

	got := Summarize(sr.Ended())
	require.Len(t, got, 1)

	assert.Equal(t, "Op", got[0].Name)
	assert.Equal(t, "scope/x", got[0].Scope)
	assert.Equal(t, "client", got[0].Kind)
	assert.Equal(t, []string{"alpha", "zeta"}, got[0].AttributeKeys, "keys are sorted, values excluded")
	assert.Equal(t, "scope/x | client | Op | alpha,zeta", got[0].String())
}
