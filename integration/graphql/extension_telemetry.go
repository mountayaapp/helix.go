package graphql

import (
	"context"
	"errors"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/telemetry/trace"

	gqlgen "github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"go.opentelemetry.io/otel/attribute"
)

// Pre-computed attribute keys to avoid allocations on every call.
var (
	attrKeyOperationType = integration.Key(identifier, "operation.type")
	attrKeyOperationName = integration.Key(identifier, "operation.name")
	attrKeyErrors        = integration.Key(identifier, "errors")
	attrKeyField         = integration.Key(identifier, "field")
	attrKeyObject        = integration.Key(identifier, "object")
)

// clientCodes are the extensions.code values of an error that is the client's
// doing rather than the server's. An operation failing with only these leaves its
// span unset, the way a 4xx leaves an HTTP span unset, so that a backend can tell
// a broken server from a client sending broken requests.
var clientCodes = map[string]bool{
	errorstack.CodeBadRequest:         true,
	errorstack.CodeUnauthorized:       true,
	errorstack.CodePaymentRequired:    true,
	errorstack.CodeForbidden:          true,
	errorstack.CodeNotFound:           true,
	errorstack.CodeMethodNotAllowed:   true,
	errorstack.CodeConflict:           true,
	errorstack.CodePreconditionFailed: true,
	errorstack.CodePayloadTooLarge:    true,
	errorstack.CodeTooManyRequests:    true,
	errorstack.CodeValidationFailed:   true,

	// gqlgen's own: a document that does not parse or validate, a persisted query
	// the server does not have — which is the ordinary first leg of the APQ
	// protocol — and an operation refused for its cost or its depth.
	errcode.ParseFailed:         true,
	errcode.ValidationFailed:    true,
	"PERSISTED_QUERY_NOT_FOUND": true,
	"COMPLEXITY_LIMIT_EXCEEDED": true,
	codeDepthLimitExceeded:      true,
}

// Ensure telemetry satisfies the gqlgen hooks it claims. gqlgen discovers them by
// type assertion when the extension is registered, so a signature drifting out of
// shape would otherwise fail silently as missing telemetry rather than as a build
// error.
var (
	_ gqlgen.HandlerExtension    = (*telemetry)(nil)
	_ gqlgen.ResponseInterceptor = (*telemetry)(nil)
	_ gqlgen.FieldInterceptor    = (*telemetry)(nil)
)

// telemetry is the gqlgen extension that traces GraphQL execution.
//
// Without it this integration emitted exactly one span per request — the HTTP
// one — which says a POST arrived at /graphql and nothing about what it asked
// for. A GraphQL endpoint has one path by design, so the HTTP layer is precisely
// the layer that cannot tell two requests apart.
type telemetry struct {
	inst      *integration.Instrument
	resolvers ResolverTracing

	// fields maps a field to the operation its span is named after,
	// "<Object>.<field>". It is built once from the schema in Validate and only
	// read afterwards, so that a resolver span does not pay for building its own
	// name on the hottest path a GraphQL server has.
	fields map[fieldKey]string
}

// fieldKey identifies a field of the schema.
type fieldKey struct {
	object string
	field  string
}

// ExtensionName identifies the extension in gqlgen's own stats and logging.
func (t *telemetry) ExtensionName() string {
	return "helix.go/telemetry"
}

// Validate builds the span names of the schema's fields. gqlgen calls it once,
// when the extension is registered.
func (t *telemetry) Validate(es gqlgen.ExecutableSchema) error {
	t.fields = map[fieldKey]string{}
	if es == nil || es.Schema() == nil {
		return nil
	}

	for _, def := range es.Schema().Types {
		if def.Kind != ast.Object {
			continue
		}

		for _, field := range def.Fields {
			t.fields[fieldKey{object: def.Name, field: field.Name}] = def.Name + "." + field.Name
		}
	}

	return nil
}

// InterceptResponse opens the operation span: "GraphQL: Query", "GraphQL:
// Mutation" or "GraphQL: Subscription". The operation name goes on the span as
// an attribute rather than in its name: the client chooses it, and a span name a
// client chooses is a span name per client whim.
//
// Ending the span records graphql.operation.duration, with the operation type as
// its graphql.operation dimension.
//
// A request whose document did not parse, or named an operation it does not
// contain, has no operation to name a span after, and gets none: its HTTP span is
// the honest shape.
func (t *telemetry) InterceptResponse(ctx context.Context, next gqlgen.ResponseHandler) *gqlgen.Response {
	if !gqlgen.HasOperationContext(ctx) {
		return next(ctx)
	}

	opCtx := gqlgen.GetOperationContext(ctx)
	if opCtx.Operation == nil {
		return next(ctx)
	}

	ctx, op := t.inst.StartOperation(ctx, trace.SpanKindInternal, operationName(opCtx.Operation.Operation))
	defer op.End(ctx)

	if op.IsRecording() {
		attrs := make([]attribute.KeyValue, 0, 2)
		attrs = append(attrs, attrKeyOperationType.String(string(opCtx.Operation.Operation)))

		// An anonymous operation has no name, and an empty attribute is not the same
		// thing as an absent one: it makes every anonymous query look like a named
		// one whose name happens to be blank.
		if opCtx.Operation.Name != "" {
			attrs = append(attrs, attrKeyOperationName.String(opCtx.Operation.Name))
		}

		op.SetAttributes(attrs...)
	}

	defer func() {
		if v := recover(); v != nil {
			op.RecordError("failed to resolve operation", panicError{value: v})
			panic(v)
		}
	}()

	resp := next(ctx)
	if resp == nil {
		return nil
	}

	// A GraphQL operation can fail partially: the transport answers 200 with an
	// "errors" array, so the HTTP span records a success. This is where that shows
	// up as a failure — when it is the server's.
	if len(resp.Errors) > 0 {
		if op.IsRecording() {
			op.SetAttributes(attrKeyErrors.Int(len(resp.Errors)))
		}

		op.RecordError("failed to resolve operation", serverErrors(resp.Errors))
	}

	return resp
}

// InterceptField opens a span around a resolver, when Config.TraceResolvers
// selects it.
//
// Only around a *resolver*: gqlgen calls this for every field in the response,
// including the ones it fills in by reading a struct member, and a span for each
// of those would describe memory access as though it were work. A field with a
// user-written resolver behind it is the one that talks to a database or another
// service, and is the reason a query was slow.
func (t *telemetry) InterceptField(ctx context.Context, next gqlgen.Resolver) (any, error) {
	fc := gqlgen.GetFieldContext(ctx)
	if fc == nil || !fc.IsResolver || !t.traces(fc) {
		return next(ctx)
	}

	ctx, span := t.inst.Start(ctx, trace.SpanKindInternal, t.spanName(fc))
	defer span.End()

	// Gated the way InterceptResponse gates its own: this runs once per resolver,
	// which is the hottest path in a GraphQL server.
	if span.IsRecording() {
		span.SetAttributes(
			attrKeyObject.String(fc.Object),
			attrKeyField.String(fc.Field.Name),
		)
	}

	// gqlgen recovers a resolver's panic further up, around this interceptor, so
	// without this the span would end as though the resolver had returned.
	defer func() {
		if v := recover(); v != nil {
			span.RecordError("failed to resolve field", panicError{value: v})
			panic(v)
		}
	}()

	res, err := next(ctx)
	if err != nil && !clientCodes[errorCode(err)] {
		span.RecordError("failed to resolve field", err)
	}

	return res, err
}

// traces reports whether the resolver of fc gets a span.
func (t *telemetry) traces(fc *gqlgen.FieldContext) bool {
	switch t.resolvers {
	case ResolverTracingAll:
		return true
	case ResolverTracingNone:
		return false
	default:
		return isRootField(fc)
	}
}

// spanName returns the operation a resolver span is named after.
func (t *telemetry) spanName(fc *gqlgen.FieldContext) string {
	if name, ok := t.fields[fieldKey{object: fc.Object, field: fc.Field.Name}]; ok {
		return name
	}

	return fc.Object + "." + fc.Field.Name
}

// isRootField reports whether fc is a field of the operation's root type. gqlgen
// marks the root object with a field context of its own, carrying the type but no
// field, so a root field is one with no field above it.
func isRootField(fc *gqlgen.FieldContext) bool {
	for parent := fc.Parent; parent != nil; parent = parent.Parent {
		if parent.Field.Field != nil {
			return false
		}
	}

	return true
}

// operationName returns the operation a span is named after for an operation
// type, in the TitleCase every span name in helix.go uses.
func operationName(op ast.Operation) string {
	switch op {
	case ast.Query:
		return "Query"
	case ast.Mutation:
		return "Mutation"
	case ast.Subscription:
		return "Subscription"
	default:
		return "Operation"
	}
}

// serverErrors returns the errors of a response the server is responsible for,
// as classified by their extensions.code, joined, or nil when every one of them
// is the client's. It records what a resolver actually returned rather than what
// the client was shown: the error presenter keeps that on the presented error,
// out of the response.
func serverErrors(list gqlerror.List) error {
	var errs []error
	for _, gqlErr := range list {
		if code, _ := gqlErr.Extensions["code"].(string); clientCodes[code] {
			continue
		}

		if gqlErr.Err != nil {
			errs = append(errs, gqlErr.Err)
		} else {
			errs = append(errs, gqlErr)
		}
	}

	return errors.Join(errs...)
}

// errorCode returns the extensions.code a resolver's error will be presented
// with: that of the first entry of an *errorstack.Error in its chain, or else that
// of a *gqlerror.Error.
func errorCode(err error) string {
	var stack *errorstack.Error
	if errors.As(err, &stack) && len(stack.Entries) > 0 {
		code, _ := stack.Entries[0].Extensions["code"].(string)
		return code
	}

	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) {
		code, _ := gqlErr.Extensions["code"].(string)
		return code
	}

	return ""
}
