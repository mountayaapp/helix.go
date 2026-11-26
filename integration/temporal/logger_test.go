package temporal

import (
	"testing"

	internallog "github.com/mountaya/helix.go/internal/telemetry/log"
	"github.com/mountaya/helix.go/service/servicetest"
	"github.com/mountaya/helix.go/telemetry/log"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	oteltrace "go.opentelemetry.io/otel/trace"
	temporallog "go.temporal.io/sdk/log"
)

// testSpanContext is a valid span context, as the tracer hands one to the logger.
func testSpanContext(t *testing.T) oteltrace.SpanContext {
	t.Helper()

	traceID, err := oteltrace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	require.NoError(t, err)

	spanID, err := oteltrace.SpanIDFromHex("b7ad6b7169203331")
	require.NoError(t, err)

	return oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: oteltrace.FlagsSampled,
	})
}

// attributeKeys returns the keys of the attributes of record.
func attributeKeys(record sdklog.Record) []string {
	var keys []string
	record.WalkAttributes(func(kv attribute.KeyValue) bool {
		keys = append(keys, string(kv.Key))
		return true
	})

	return keys
}

func TestCustomLogger_Parse(t *testing.T) {
	l := &customLogger{logger: internallog.NewNopLogger()}

	testcases := []struct {
		name    string
		keyvals []any
		want    []log.Field
	}{
		{
			name:    "pairs become fields",
			keyvals: []any{"key1", "value1", "key2", 42},
			want:    []log.Field{log.Any("key1", "value1"), log.Any("key2", 42)},
		},
		{
			name:    "a trailing key without a value is dropped",
			keyvals: []any{"key1", "value1", "orphan"},
			want:    []log.Field{log.Any("key1", "value1")},
		},
		{
			name:    "a key that is not a string is formatted",
			keyvals: []any{123, "value"},
			want:    []log.Field{log.Any("123", "value")},
		},
		{
			name:    "nothing gives no fields",
			keyvals: nil,
			want:    nil,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			sc, fields := l.parse(tc.keyvals)

			assert.Equal(t, tc.want, fields)
			assert.False(t, sc.IsValid())
		})
	}

	t.Run("the span context is taken, not logged", func(t *testing.T) {
		sc, fields := l.parse([]any{spanContextKey{}, testSpanContext(t), "key", "value"})

		assert.Equal(t, testSpanContext(t), sc)
		assert.Equal(t, []log.Field{log.Any("key", "value")}, fields)
	})
}

func TestCustomLogger_CorrelatesWithTheSpanItWasGiven(t *testing.T) {
	svc, rec := servicetest.New(t)
	l := newCustomLogger(svc)

	// The way the SDK's workflow and activity loggers reach it: through With, or as
	// key-value pairs prepended to every line.
	temporallog.With(l, spanContextKey{}, testSpanContext(t)).Info("through With", "key", "value")
	l.Warn("through the line", spanContextKey{}, testSpanContext(t))

	records := rec.Records()
	require.Len(t, records, 2)

	for _, record := range records {
		assert.Equal(t, testSpanContext(t).TraceID(), record.TraceID(), "record %q", record.Body().AsString())
		assert.Equal(t, testSpanContext(t).SpanID(), record.SpanID(), "record %q", record.Body().AsString())

		// Correlated natively, rather than with fields of their own.
		assert.NotContains(t, attributeKeys(record), "TraceID")
		assert.NotContains(t, attributeKeys(record), "SpanID")
	}

	assert.Contains(t, attributeKeys(records[0]), "key")
}

func TestCustomLogger_LinesWithoutASpanAreNotCorrelated(t *testing.T) {
	svc, rec := servicetest.New(t)

	// A context carrying a span when the client was built must not leak onto
	// every line the SDK logs afterwards: the logger holds no context at all.
	newCustomLogger(svc).Error("background line")

	records := rec.Records()
	require.Len(t, records, 1)
	assert.False(t, records[0].TraceID().IsValid())
}

func TestCustomLogger_AllLevels(t *testing.T) {
	svc, rec := servicetest.New(t)
	l := newCustomLogger(svc)

	l.Debug("debug")
	l.Info("info")
	l.Warn("warn")
	l.Error("error")

	var bodies []string
	for _, record := range rec.Records() {
		bodies = append(bodies, record.Body().AsString())
	}

	// Debug is below the Service's default level.
	assert.Equal(t, []string{"info", "warn", "error"}, bodies)
}
