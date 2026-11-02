package clickhouse

import (
	"reflect"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// spanContext returns a valid span context whose identifiers are made of b.
func spanContext(b byte) oteltrace.SpanContext {
	var traceID oteltrace.TraceID
	var spanID oteltrace.SpanID
	for i := range traceID {
		traceID[i] = b
	}
	for i := range spanID {
		spanID[i] = b
	}

	return oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: oteltrace.FlagsSampled,
	})
}

// profileEventsCallback returns the code pointer of the profile-events callback o
// holds, which tells two callbacks apart without being able to call either.
func profileEventsCallback(o *clickhouse.QueryOptions) uintptr {
	return reflect.ValueOf(o).Elem().FieldByName("events").FieldByName("profileEvents").Pointer()
}

// TestTraced_SeesCallerOptions pins the driver's layout that traced reads through
// reflection. If a driver release renames the fields, this fails, rather than
// traced silently going back to overwriting what the caller set.
func TestTraced_SeesCallerOptions(t *testing.T) {
	var o clickhouse.QueryOptions
	assert.False(t, isSet(&o, "span"))
	assert.False(t, isSet(&o, "events", "profileEvents"))

	require.NoError(t, clickhouse.WithSpan(spanContext(1))(&o))
	require.NoError(t, clickhouse.WithProfileEvents(func([]clickhouse.ProfileEvent) {})(&o))
	assert.True(t, isSet(&o, "span"))
	assert.True(t, isSet(&o, "events", "profileEvents"))

	// A path that does not resolve reads as unset rather than panicking.
	assert.False(t, isSet(&o, "missing"))
	assert.False(t, isSet(&o, "span", "missing", "deeper"))
}

func TestTraced_SetsSpanAndCallbackWhenTheCallerDidNot(t *testing.T) {
	p := &profile{}

	var got clickhouse.QueryOptions
	require.NoError(t, traced(spanContext(2), nil)(&got))

	var want clickhouse.QueryOptions
	require.NoError(t, clickhouse.WithSpan(spanContext(2))(&want))
	assert.Equal(t, want, got)

	require.NoError(t, traced(spanContext(2), p.collect)(&got))
	assert.True(t, isSet(&got, "events", "profileEvents"))
}

// TestTraced_KeepsCallerOptions is the regression test for queryContext
// overwriting the span and the profile-events callback a caller had set through
// clickhouse.Context.
func TestTraced_KeepsCallerOptions(t *testing.T) {
	theirs := func([]clickhouse.ProfileEvent) {}
	p := &profile{}

	var got clickhouse.QueryOptions
	require.NoError(t, clickhouse.WithSpan(spanContext(1))(&got))
	require.NoError(t, traced(spanContext(2), nil)(&got))

	var want clickhouse.QueryOptions
	require.NoError(t, clickhouse.WithSpan(spanContext(1))(&want))
	assert.Equal(t, want, got)

	require.NoError(t, clickhouse.WithProfileEvents(theirs)(&got))
	require.NoError(t, traced(spanContext(2), p.collect)(&got))
	assert.Equal(t, reflect.ValueOf(theirs).Pointer(), profileEventsCallback(&got))
}

func TestProfile_SumsTheShortList(t *testing.T) {
	p := &profile{}
	assert.Empty(t, p.attributes())

	p.collect([]clickhouse.ProfileEvent{
		{Name: "SelectedRows", Value: 10},
		{Name: "SelectedBytes", Value: 100},
		{Name: "OSCPUVirtualTimeMicroseconds", Value: 42},
	})
	p.collect([]clickhouse.ProfileEvent{
		{Name: "SelectedRows", Value: 5},
	})

	assert.ElementsMatch(t, []attribute.KeyValue{
		attribute.Int64("clickhouse.selected.rows", 15),
		attribute.Int64("clickhouse.selected.bytes", 100),
	}, p.attributes())
}
