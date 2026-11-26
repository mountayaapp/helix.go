package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// newRecordingInstrument builds an Instrument whose spans are recorded in memory,
// without a Service: the integration package cannot import service without a
// cycle, and the Instrument does not need one.
func newRecordingInstrument(tb testing.TB, cfg InstrumentConfig) (*Instrument, *tracetest.SpanRecorder) {
	tb.Helper()

	recorder := tracetest.NewSpanRecorder()
	cfg.TracerProvider = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	inst, err := NewInstrument(cfg)
	require.NoError(tb, err)

	return inst, recorder
}

// mustInstrument builds an Instrument for a config the test asserts is valid.
func mustInstrument(tb testing.TB, cfg InstrumentConfig) *Instrument {
	tb.Helper()

	inst, err := NewInstrument(cfg)
	require.NoError(tb, err)

	return inst
}

func TestKey(t *testing.T) {
	assert.Equal(t, attribute.Key("postgres.query"), Key("postgres", "query"))
	assert.Equal(t, attribute.Key("temporal.workflow.run_id"), Key("temporal", "workflow.run_id"))
}

func TestInstrument_Key(t *testing.T) {
	i := mustInstrument(t, InstrumentConfig{Identifier: "valkey", Humanized: "Valkey"})

	assert.Equal(t, attribute.Key("valkey.key"), i.Key("key"))
	assert.Equal(t, Key("valkey", "key"), i.Key("key"))
}

func TestInstrument_Start(t *testing.T) {
	i, rec := newRecordingInstrument(t, InstrumentConfig{
		Identifier: "postgres",
		Humanized:  "PostgreSQL",
		Name:       "replica",
		Attributes: []attribute.KeyValue{Key("postgres", "database").String("helix")},
	})

	_, span := i.Start(t.Context(), oteltrace.SpanKindClient, "Exec")
	span.SetAttributes(Key("postgres", "query").String("SELECT 1"))
	span.End()

	ended := rec.Ended()
	require.Len(t, ended, 1)

	assert.Equal(t, "PostgreSQL: Exec", ended[0].Name())
	assert.Equal(t, oteltrace.SpanKindClient, ended[0].SpanKind())
	assert.Equal(t, []attribute.KeyValue{
		attribute.String("postgres.name", "replica"),
		attribute.String("postgres.database", "helix"),
		attribute.String("postgres.query", "SELECT 1"),
	}, ended[0].Attributes())
}

// TestInstrument_StartStampsNameWithoutConfigName pins that a connection the
// application did not name still says which integration it came from, rather than
// carrying an empty attribute.
func TestInstrument_StartStampsNameWithoutConfigName(t *testing.T) {
	i, rec := newRecordingInstrument(t, InstrumentConfig{Identifier: "valkey", Humanized: "Valkey"})

	_, span := i.Start(t.Context(), oteltrace.SpanKindClient, "Get")
	span.End()

	require.Len(t, rec.Ended(), 1)
	assert.Equal(t, []attribute.KeyValue{attribute.String("valkey.name", "valkey")}, rec.Ended()[0].Attributes())
}

// TestInstrument_StartDoesNotAliasCallerAttributes pins that the Instrument holds
// a copy: an integration that reuses the slice it passed must not be able to
// rewrite what every span already emitted.
func TestInstrument_StartDoesNotAliasCallerAttributes(t *testing.T) {
	attrs := make([]attribute.KeyValue, 0, 4)
	attrs = append(attrs, Key("bucket", "driver").String("local"))

	i, rec := newRecordingInstrument(t, InstrumentConfig{
		Identifier: "bucket",
		Humanized:  "Bucket",
		Attributes: attrs,
	})

	// Writes into the spare capacity the caller still holds. If the Instrument had
	// kept the caller's slice rather than copying it, this would land in what it
	// stamps on every span.
	attrs = append(attrs, Key("bucket", "bucket").String("mutated"))
	require.Len(t, attrs, 2)

	_, span := i.Start(t.Context(), oteltrace.SpanKindClient, "Write")
	span.End()

	require.Len(t, rec.Ended(), 1)
	assert.Equal(t, []attribute.KeyValue{
		attribute.String("bucket.name", "bucket"),
		attribute.String("bucket.driver", "local"),
	}, rec.Ended()[0].Attributes())
}

func TestInstrument_StartNests(t *testing.T) {
	i, rec := newRecordingInstrument(t, InstrumentConfig{Identifier: "rest", Humanized: "REST"})

	ctx, parent := i.Start(t.Context(), oteltrace.SpanKindServer, "Request")
	_, child := i.Start(ctx, oteltrace.SpanKindInternal, "Validate")
	child.End()
	parent.End()

	ended := rec.Ended()
	require.Len(t, ended, 2)
	assert.Equal(t, ended[1].SpanContext().SpanID(), ended[0].Parent().SpanID())
}

func TestInstrument_Accessors(t *testing.T) {
	i := mustInstrument(t, InstrumentConfig{Identifier: "mcp", Humanized: "MCP", Name: "tools"})

	assert.Equal(t, "mcp", i.Identifier())
	assert.Equal(t, "MCP", i.Humanized())
	assert.Equal(t, "tools", i.Name())
	assert.NotNil(t, i.Meter())
}

func BenchmarkInstrument_Start(b *testing.B) {
	i := mustInstrument(b, InstrumentConfig{
		Identifier: "postgres",
		Humanized:  "PostgreSQL",
		Attributes: []attribute.KeyValue{Key("postgres", "database").String("helix")},
	})

	ctx := b.Context()

	b.ReportAllocs()
	for b.Loop() {
		_, span := i.Start(ctx, oteltrace.SpanKindClient, "Exec")
		span.End()
	}
}

// TestNewInstrument_RequiresIdentity pins that an Instrument cannot be built
// without the two fields every span name and attribute key is derived from. An
// empty one does not degrade the telemetry, it invents a vocabulary nothing else
// speaks — and a third-party integration has no AssertConvention to catch it.
func TestNewInstrument_RequiresIdentity(t *testing.T) {
	for name, cfg := range map[string]InstrumentConfig{
		"no identifier": {Humanized: "PostgreSQL"},
		"no humanized":  {Identifier: "postgres"},
		"neither":       {},
	} {
		t.Run(name, func(t *testing.T) {
			inst, err := NewInstrument(cfg)

			assert.Nil(t, inst)
			require.Error(t, err)
		})
	}
}

// TestInstrument_StartStampsAttributesAtCreation pins that attributes passed to
// Start are on the span when it is created, where a sampler can see them.
func TestInstrument_StartStampsAttributesAtCreation(t *testing.T) {
	var seen []attribute.KeyValue
	sampler := samplerFunc(func(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
		seen = p.Attributes
		return sdktrace.SamplingResult{Decision: sdktrace.RecordAndSample}
	})

	inst, err := NewInstrument(InstrumentConfig{
		Identifier:     "valkey",
		Humanized:      "Valkey",
		TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSampler(sampler)),
	})
	require.NoError(t, err)

	_, op := inst.StartOperation(t.Context(), oteltrace.SpanKindClient, "Get", inst.Key("key").String("k"))
	op.End(t.Context())

	assert.Contains(t, seen, attribute.String("valkey.key", "k"))
	assert.Contains(t, seen, attribute.String("valkey.name", "valkey"))
}

// samplerFunc adapts a function to sdktrace.Sampler.
type samplerFunc func(sdktrace.SamplingParameters) sdktrace.SamplingResult

func (f samplerFunc) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult { return f(p) }
func (samplerFunc) Description() string                                                  { return "func" }
