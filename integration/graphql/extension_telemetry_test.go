package graphql

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service/servicetest"

	gqlgen "github.com/99designs/gqlgen/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// newRecordingTelemetry builds the extension with its spans recorded in memory,
// validated against the test schema as gqlgen would when it is registered.
func newRecordingTelemetry(tb testing.TB, resolvers ResolverTracing) (*telemetry, *tracetest.SpanRecorder) {
	tb.Helper()

	recorder := tracetest.NewSpanRecorder()

	inst, err := integration.NewInstrument(integration.InstrumentConfig{
		Identifier:     identifier,
		Humanized:      humanized,
		Name:           "golden",
		TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)),
	})
	require.NoError(tb, err)

	ext := &telemetry{inst: inst, resolvers: resolvers}
	require.NoError(tb, ext.Validate(testSchema(nil)))

	return ext, recorder
}

// fieldContext returns a context carrying the root field gqlgen is about to
// resolve, under the field context gqlgen marks the root object with.
func fieldContext(ctx context.Context, object, field string, isResolver bool) context.Context {
	ctx = gqlgen.WithFieldContext(ctx, &gqlgen.FieldContext{Object: object})

	return gqlgen.WithFieldContext(ctx, &gqlgen.FieldContext{
		Object:     object,
		Field:      gqlgen.CollectedField{Field: &ast.Field{Name: field, Alias: field}},
		IsResolver: isResolver,
	})
}

// nestedFieldContext returns a context carrying a resolver field one level below
// the root field Query.thing.
func nestedFieldContext(ctx context.Context) context.Context {
	return gqlgen.WithFieldContext(fieldContext(ctx, "Query", "thing", true), &gqlgen.FieldContext{
		Object:     "Thing",
		Field:      gqlgen.CollectedField{Field: &ast.Field{Name: "child", Alias: "child"}},
		IsResolver: true,
	})
}

// TestInterceptField covers the resolver spans on their own; the operation span
// they nest under is covered through a whole request below.
func TestInterceptField(t *testing.T) {
	t.Run("a root resolver gets a span", func(t *testing.T) {
		ext, rec := newRecordingTelemetry(t, ResolverTracingRoot)

		res, err := ext.InterceptField(fieldContext(t.Context(), "Query", "name", true),
			func(context.Context) (any, error) { return "ok", nil })

		require.NoError(t, err)
		assert.Equal(t, "ok", res)

		ended := rec.Ended()
		require.Len(t, ended, 1)
		assert.Equal(t, "GraphQL: Query.name", ended[0].Name())
		assert.Equal(t, oteltrace.SpanKindInternal, ended[0].SpanKind())
		assert.Equal(t, codes.Unset, ended[0].Status().Code)
		assert.Equal(t, []attribute.KeyValue{
			attribute.String("graphql.name", "golden"),
			attribute.String("graphql.object", "Query"),
			attribute.String("graphql.field", "name"),
		}, ended[0].Attributes())
	})

	t.Run("span names come from the schema", func(t *testing.T) {
		ext, _ := newRecordingTelemetry(t, ResolverTracingRoot)

		assert.Equal(t, "Thing.child", ext.fields[fieldKey{object: "Thing", field: "child"}])
		assert.Equal(t, "Query.name", ext.spanName(gqlgen.GetFieldContext(fieldContext(t.Context(), "Query", "name", true))))
		assert.Equal(t, "Query.unknown", ext.spanName(gqlgen.GetFieldContext(fieldContext(t.Context(), "Query", "unknown", true))),
			"a field missing from the schema is still named")
	})

	// gqlgen calls InterceptField for every field in the response, including the
	// ones it fills in by reading a struct member. A span for each of those would
	// describe memory access as though it were work.
	t.Run("a plain field gets none", func(t *testing.T) {
		ext, rec := newRecordingTelemetry(t, ResolverTracingAll)

		_, err := ext.InterceptField(fieldContext(t.Context(), "Thing", "id", false),
			func(context.Context) (any, error) { return 1, nil })

		require.NoError(t, err)
		assert.Empty(t, rec.Ended())
	})

	t.Run("a field with no context at all is passed through", func(t *testing.T) {
		ext, rec := newRecordingTelemetry(t, ResolverTracingAll)

		_, err := ext.InterceptField(t.Context(), func(context.Context) (any, error) { return nil, nil })

		require.NoError(t, err)
		assert.Empty(t, rec.Ended())
	})

	t.Run("a failing resolver records the error", func(t *testing.T) {
		ext, rec := newRecordingTelemetry(t, ResolverTracingRoot)

		_, err := ext.InterceptField(fieldContext(t.Context(), "Mutation", "rename", true),
			func(context.Context) (any, error) { return nil, errors.New("boom") })

		require.Error(t, err)
		require.Len(t, rec.Ended(), 1)
		assert.Equal(t, "GraphQL: Mutation.rename", rec.Ended()[0].Name())
		assert.Equal(t, codes.Error, rec.Ended()[0].Status().Code)
		assert.Len(t, rec.Ended()[0].Events(), 1, "the error must be recorded as an exception event")
	})

	t.Run("a client error leaves the span unset", func(t *testing.T) {
		ext, rec := newRecordingTelemetry(t, ResolverTracingRoot)

		_, err := ext.InterceptField(fieldContext(t.Context(), "Query", "thing", true),
			func(context.Context) (any, error) {
				return nil, errorstack.New("Thing does not exist", errorstack.WithCode(errorstack.CodeNotFound))
			})

		require.Error(t, err)
		require.Len(t, rec.Ended(), 1)
		assert.Equal(t, codes.Unset, rec.Ended()[0].Status().Code)
		assert.Empty(t, rec.Ended()[0].Events())
	})

	// gqlgen recovers a resolver's panic above the interceptor, so the span used to
	// end as though the resolver had returned.
	t.Run("a panicking resolver records the panic", func(t *testing.T) {
		ext, rec := newRecordingTelemetry(t, ResolverTracingRoot)

		assert.PanicsWithValue(t, "boom", func() {
			_, _ = ext.InterceptField(fieldContext(t.Context(), "Query", "name", true),
				func(context.Context) (any, error) { panic("boom") })
		}, "the panic must reach gqlgen's recovery")

		require.Len(t, rec.Ended(), 1)
		assert.Equal(t, codes.Error, rec.Ended()[0].Status().Code)
		require.Len(t, rec.Ended()[0].Events(), 1)
		assert.Contains(t, rec.Ended()[0].Events()[0].Attributes, attribute.String("exception.message", "panic: boom"))
	})
}

func TestInterceptField_TraceResolvers(t *testing.T) {
	testcases := []struct {
		mode   ResolverTracing
		root   bool
		nested bool
	}{
		{mode: ResolverTracingRoot, root: true, nested: false},
		{mode: ResolverTracingAll, root: true, nested: true},
		{mode: ResolverTracingNone, root: false, nested: false},
	}

	resolve := func(context.Context) (any, error) { return nil, nil }

	for _, tc := range testcases {
		t.Run(string(tc.mode), func(t *testing.T) {
			ext, rec := newRecordingTelemetry(t, tc.mode)

			_, _ = ext.InterceptField(fieldContext(t.Context(), "Query", "thing", true), resolve)
			_, _ = ext.InterceptField(nestedFieldContext(t.Context()), resolve)

			var names []string
			for _, span := range rec.Ended() {
				names = append(names, span.Name())
			}

			assert.Equal(t, tc.root, slices.Contains(names, "GraphQL: Query.thing"), "root field traced")
			assert.Equal(t, tc.nested, slices.Contains(names, "GraphQL: Thing.child"), "nested field traced")
		})
	}
}

func TestIsRootField(t *testing.T) {
	assert.True(t, isRootField(gqlgen.GetFieldContext(fieldContext(t.Context(), "Query", "thing", true))))
	assert.False(t, isRootField(gqlgen.GetFieldContext(nestedFieldContext(t.Context()))))
}

func TestExtensionName(t *testing.T) {
	ext, _ := newRecordingTelemetry(t, ResolverTracingRoot)

	assert.Equal(t, "helix.go/telemetry", ext.ExtensionName())
	assert.NoError(t, ext.Validate(nil))
}

// operationSpan returns the operation span rec recorded, failing the test when
// there is not exactly one.
func operationSpan(t *testing.T, rec *servicetest.Recorder) sdktrace.ReadOnlySpan {
	t.Helper()

	var found []sdktrace.ReadOnlySpan
	for _, span := range rec.Spans() {
		switch span.Name() {
		case "GraphQL: Query", "GraphQL: Mutation", "GraphQL: Subscription":
			found = append(found, span)
		}
	}

	require.Len(t, found, 1, "operation spans")
	return found[0]
}

// exceptionMessage returns the message of an exception event.
func exceptionMessage(event sdktrace.Event) string {
	for _, attr := range event.Attributes {
		if attr.Key == "exception.message" {
			return attr.Value.AsString()
		}
	}

	return ""
}

// attributes returns the attributes of span as a map.
func attributes(span sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	out := map[attribute.Key]attribute.Value{}
	for _, attr := range span.Attributes() {
		out[attr.Key] = attr.Value
	}

	return out
}

// TestInterceptResponse covers the operation span through a whole request.
func TestInterceptResponse(t *testing.T) {
	t.Run("named after the operation type, with its name as an attribute", func(t *testing.T) {
		h, rec := newTestHandler(t, testConfig(map[string]resolverFunc{"name": constant("helix")}))

		postGraphQL(h, query("query ListThings { name }"))

		span := operationSpan(t, rec)
		assert.Equal(t, "GraphQL: Query", span.Name())
		assert.Equal(t, oteltrace.SpanKindInternal, span.SpanKind())
		assert.Equal(t, codes.Unset, span.Status().Code)

		attrs := attributes(span)
		assert.Equal(t, "ListThings", attrs["graphql.operation.name"].AsString())
		assert.Equal(t, "query", attrs["graphql.operation.type"].AsString())
	})

	t.Run("an anonymous operation has no name attribute", func(t *testing.T) {
		h, rec := newTestHandler(t, testConfig(map[string]resolverFunc{"name": constant("helix")}))

		postGraphQL(h, query("{ name }"))

		assert.NotContains(t, attributes(operationSpan(t, rec)), attribute.Key("graphql.operation.name"))
	})

	t.Run("a mutation", func(t *testing.T) {
		h, rec := newTestHandler(t, testConfig(map[string]resolverFunc{"rename": constant("helix")}))

		postGraphQL(h, query(`mutation { rename(name: "helix") }`))

		assert.Equal(t, "GraphQL: Mutation", operationSpan(t, rec).Name())
	})

	// GQL-8: any error used to mark the operation failed, so a client asking for
	// something that does not exist looked like a server failing.
	t.Run("a client error leaves the span unset", func(t *testing.T) {
		h, rec := newTestHandler(t, testConfig(map[string]resolverFunc{
			"name": func(context.Context) (any, error) {
				return nil, errorstack.New("Thing does not exist", errorstack.WithCode(errorstack.CodeNotFound))
			},
		}))

		postGraphQL(h, query("{ name }"))

		span := operationSpan(t, rec)
		assert.Equal(t, codes.Unset, span.Status().Code)
		assert.Empty(t, span.Events())
		assert.Equal(t, int64(1), attributes(span)["graphql.errors"].AsInt64())
	})

	t.Run("an operation refused for its cost leaves the span unset", func(t *testing.T) {
		cfg := testConfig(nil)
		cfg.ComplexityLimit = 1
		h, rec := newTestHandler(t, cfg)

		postGraphQL(h, query("{ name other }"))

		assert.Equal(t, codes.Unset, operationSpan(t, rec).Status().Code)
	})

	// The client is shown a masked error; the span records the real one.
	t.Run("a server error records what the resolver returned", func(t *testing.T) {
		h, rec := newTestHandler(t, testConfig(map[string]resolverFunc{
			"name": func(context.Context) (any, error) {
				return nil, errors.New("dial tcp 10.0.0.7:5432: connection refused")
			},
		}))

		postGraphQL(h, query("{ name }"))

		span := operationSpan(t, rec)
		assert.Equal(t, codes.Error, span.Status().Code)
		require.Len(t, span.Events(), 1)
		assert.Contains(t, span.Events()[0].Attributes, attribute.String("exception.message", "dial tcp 10.0.0.7:5432: connection refused"))
	})

	t.Run("a panicking resolver fails the operation", func(t *testing.T) {
		h, rec := newTestHandler(t, testConfig(map[string]resolverFunc{
			"name": func(context.Context) (any, error) { panic("boom") },
		}))

		postGraphQL(h, query("{ name }"))

		span := operationSpan(t, rec)
		assert.Equal(t, codes.Error, span.Status().Code)
		require.Len(t, span.Events(), 1)
		assert.Contains(t, exceptionMessage(span.Events()[0]), "panic: boom")
	})

	t.Run("a document that does not parse has no operation span", func(t *testing.T) {
		h, rec := newTestHandler(t, testConfig(nil))

		postGraphQL(h, query("{ name"))

		for _, span := range rec.Spans() {
			assert.Equal(t, oteltrace.SpanKindServer, span.SpanKind(), "only the HTTP span: %s", span.Name())
		}
	})

	t.Run("records the operation duration by operation type", func(t *testing.T) {
		h, rec := newTestHandler(t, testConfig(map[string]resolverFunc{"name": constant("helix")}))

		postGraphQL(h, query("query ListThings { name }"))

		var found bool
		for _, m := range rec.Metrics() {
			if m.Name != "graphql.operation.duration" {
				continue
			}

			found = true
			assert.Contains(t, pointAttributes(t, m), "graphql.name=graphql,graphql.operation=Query,graphql.outcome=ok")
		}

		assert.True(t, found, "graphql.operation.duration must be recorded")
	})
}

// pointAttributes returns the attribute sets of the data points of a duration
// histogram, each encoded as "key=value,...".
func pointAttributes(t *testing.T, m metricdata.Metrics) []string {
	t.Helper()

	histogram, ok := m.Data.(metricdata.Histogram[float64])
	require.True(t, ok, "%s is not a float64 histogram", m.Name)

	var out []string
	for _, point := range histogram.DataPoints {
		out = append(out, point.Attributes.Encoded(attribute.DefaultEncoder()))
	}

	return out
}
