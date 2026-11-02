package temporal

import (
	"testing"

	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service"
	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/interceptor"
	sdklog "go.temporal.io/sdk/log"
)

// newTestTracer builds the tracer dial builds, over a recording Service.
func newTestTracer(t *testing.T) (interceptor.Tracer, *servicetest.Recorder) {
	t.Helper()

	svc, rec := servicetest.New(t)

	inst, err := integration.NewInstrument(integration.InstrumentConfig{
		Identifier: identifier,
		Humanized:  humanized,
		Name:       "orders",
		Attributes: []attribute.KeyValue{
			attrKeyServerAddress.String("127.0.0.1:7233"),
			attrKeyNamespace.String("default"),
		},
		TracerProvider: service.TracerProvider(svc),
	})
	require.NoError(t, err)

	tracer, err := buildTracer(inst, svc)
	require.NoError(t, err)

	return tracer, rec
}

// startAndFinish starts a span through tracer with opts, finishes it and returns
// it as recorded.
func startAndFinish(t *testing.T, tracer interceptor.Tracer, rec *servicetest.Recorder, opts *interceptor.TracerStartSpanOptions) sdktrace.ReadOnlySpan {
	t.Helper()

	span, err := tracer.StartSpan(opts)
	require.NoError(t, err)
	span.Finish(&interceptor.TracerFinishSpanOptions{})

	spans := rec.Spans()
	require.NotEmpty(t, spans)

	return spans[len(spans)-1]
}

// attributesOf returns the attributes of span as a map.
func attributesOf(span sdktrace.ReadOnlySpan) map[string]string {
	out := make(map[string]string, len(span.Attributes()))
	for _, attr := range span.Attributes() {
		out[string(attr.Key)] = attr.Value.String()
	}

	return out
}

func TestCustomTracer_StartSpan(t *testing.T) {
	tracer, rec := newTestTracer(t)

	span := startAndFinish(t, tracer, rec, &interceptor.TracerStartSpanOptions{
		Operation: "RunActivity",
		Name:      "charge-user",
		Tags: map[string]string{
			"temporalWorkflowID": "order-1",
			"temporalRunID":      "run-1",
			"temporalActivityID": "5",
		},
	})

	// The operation names the span, spelled the way span names are spelled here,
	// and the activity's type becomes an attribute rather than a suffix of it.
	assert.Equal(t, "Temporal: Run Activity", span.Name())
	assert.Equal(t, oteltrace.SpanKindServer, span.SpanKind())

	assert.Equal(t, map[string]string{
		"temporal.name":            "orders",
		"temporal.server.address":  "127.0.0.1:7233",
		"temporal.namespace":       "default",
		"temporal.workflow.id":     "order-1",
		"temporal.workflow.run_id": "run-1",
		"temporal.activity.id":     "5",
		"temporal.activity.type":   "charge-user",
	}, attributesOf(span))

	servicetest.AssertConvention(t, rec.Spans())
}

func TestCustomTracer_StartSpan_TypesEveryOperation(t *testing.T) {
	testcases := []struct {
		operation string
		name      string
		key       string
	}{
		{"StartWorkflow", "Temporal: Start Workflow", "temporal.workflow.type"},
		{"RunWorkflow", "Temporal: Run Workflow", "temporal.workflow.type"},
		{"StartChildWorkflow", "Temporal: Start Child Workflow", "temporal.workflow.type"},
		{"SignalWithStartWorkflow", "Temporal: Signal With Start Workflow", "temporal.workflow.type"},
		{"StartActivity", "Temporal: Start Activity", "temporal.activity.type"},
		{"SignalWorkflow", "Temporal: Signal Workflow", "temporal.signal.type"},
		{"HandleSignal", "Temporal: Handle Signal", "temporal.signal.type"},
		{"SignalExternalWorkflow", "Temporal: Signal External Workflow", "temporal.signal.type"},
		{"QueryWorkflow", "Temporal: Query Workflow", "temporal.query.type"},
		{"HandleQuery", "Temporal: Handle Query", "temporal.query.type"},
		{"UpdateWorkflow", "Temporal: Update Workflow", "temporal.update.type"},
		{"ValidateUpdate", "Temporal: Validate Update", "temporal.update.type"},
		{"HandleUpdate", "Temporal: Handle Update", "temporal.update.type"},
		{"StartNexusOperation", "Temporal: Start Nexus Operation", "temporal.nexus.operation"},
	}

	for _, tc := range testcases {
		t.Run(tc.operation, func(t *testing.T) {
			tracer, rec := newTestTracer(t)

			span := startAndFinish(t, tracer, rec, &interceptor.TracerStartSpanOptions{
				Operation: tc.operation,
				Name:      "the-name",
				Tags:      map[string]string{},
			})

			assert.Equal(t, tc.name, span.Name())
			assert.Equal(t, "the-name", attributesOf(span)[tc.key])
		})
	}
}

func TestCustomTracer_StartSpan_WithoutTags(t *testing.T) {
	tracer, rec := newTestTracer(t)

	// CreateSchedule is started with no tags at all.
	var span sdktrace.ReadOnlySpan
	assert.NotPanics(t, func() {
		span = startAndFinish(t, tracer, rec, &interceptor.TracerStartSpanOptions{
			Operation: "CreateSchedule",
			Name:      "nightly",
			ToHeader:  true,
		})
	})

	assert.Equal(t, "Temporal: Create Schedule", span.Name())
	assert.Equal(t, oteltrace.SpanKindClient, span.SpanKind())
	assert.Equal(t, "nightly", attributesOf(span)["temporal.schedule.id"])
}

func TestCustomTracer_StartSpan_EmptyTagsAreDropped(t *testing.T) {
	tracer, rec := newTestTracer(t)

	span := startAndFinish(t, tracer, rec, &interceptor.TracerStartSpanOptions{
		Operation: "StartActivity",
		Tags:      map[string]string{"temporalActivityID": ""},
	})

	attrs := attributesOf(span)
	assert.NotContains(t, attrs, "temporalActivityID")
	assert.NotContains(t, attrs, "temporal.activity.id")
	assert.NotContains(t, attrs, "temporal.activity.type", "an empty name is not a type")
}

func TestCustomTracer_StartSpan_UnknownOperation(t *testing.T) {
	tracer, rec := newTestTracer(t)

	span := startAndFinish(t, tracer, rec, &interceptor.TracerStartSpanOptions{
		Operation: "DoSomethingNew",
		Name:      "x",
		Tags:      map[string]string{},
	})

	assert.Equal(t, "Temporal: Do Something New", span.Name())
}

func TestCustomTracer_DoesNotPropagateBaggage(t *testing.T) {
	tracer, _ := newTestTracer(t)

	// A parent as the tracer reads it from a header written upstream, carrying
	// whatever baggage that side had — on a server, whatever its caller sent.
	member, err := baggage.NewMemberRaw("user.email", "someone@example.com")
	require.NoError(t, err)

	bag, err := baggage.New(member)
	require.NoError(t, err)

	parent, err := tracer.UnmarshalSpan(map[string]string{
		"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
		"baggage":     bag.String(),
	})
	require.NoError(t, err)

	span, err := tracer.StartSpan(&interceptor.TracerStartSpanOptions{
		Parent:    parent,
		Operation: "StartActivity",
		Name:      "charge-user",
		ToHeader:  true,
	})
	require.NoError(t, err)
	defer span.Finish(&interceptor.TracerFinishSpanOptions{})

	header, err := tracer.MarshalSpan(span)
	require.NoError(t, err)

	// The trace continues; the baggage does not reach the header Temporal
	// persists in history.
	assert.Contains(t, header, "traceparent")
	assert.NotContains(t, header, "baggage")
}

// recordingLogger is a Temporal logger that keeps the key-value pairs of the last
// line it was given.
type recordingLogger struct {
	keyvals []any
}

func (l *recordingLogger) Debug(_ string, keyvals ...any) { l.keyvals = keyvals }
func (l *recordingLogger) Info(_ string, keyvals ...any)  { l.keyvals = keyvals }
func (l *recordingLogger) Warn(_ string, keyvals ...any)  { l.keyvals = keyvals }
func (l *recordingLogger) Error(_ string, keyvals ...any) { l.keyvals = keyvals }

func TestCustomTracer_GetLogger(t *testing.T) {
	tracer, _ := newTestTracer(t)

	span, err := tracer.StartSpan(&interceptor.TracerStartSpanOptions{Operation: "RunWorkflow", Name: "process-order"})
	require.NoError(t, err)
	defer span.Finish(&interceptor.TracerFinishSpanOptions{})

	base := &recordingLogger{}
	tracer.GetLogger(base, span).Info("hello", "key", "value")

	// The span's context is handed on for customLogger to correlate the line with,
	// rather than the contrib tracer's TraceID and SpanID pairs.
	require.Len(t, base.keyvals, 4)
	assert.Equal(t, spanContextKey{}, base.keyvals[0])
	assert.Equal(t, span.(oteltrace.Span).SpanContext(), base.keyvals[1])
	assert.Equal(t, []any{"key", "value"}, base.keyvals[2:])

	// A reference that is not a span leaves the logger alone.
	var unchanged sdklog.Logger = base
	assert.Same(t, base, tracer.GetLogger(unchanged, "not a span"))
}

func TestHumanize(t *testing.T) {
	testcases := map[string]string{
		"RunWorkflow":                   "Run Workflow",
		"SignalWithStartWorkflow":       "Signal With Start Workflow",
		"RunStartNexusOperationHandler": "Run Start Nexus Operation Handler",
		"Single":                        "Single",
		"":                              "",
	}

	for in, want := range testcases {
		assert.Equal(t, want, humanize(in), "humanize(%q)", in)
	}
}

func TestOperationNameKeys_CoverTagRenameTargets(t *testing.T) {
	// Every renamed tag lands in this integration's namespace.
	for old, renamed := range tagRenames {
		assert.Regexp(t, `^temporal\.[a-z_.]+$`, renamed, "tag %q", old)
	}

	for operation, key := range operationNameKeys {
		assert.Regexp(t, `^temporal\.[a-z_.]+$`, key, "operation %q", operation)
	}
}
