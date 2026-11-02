package mcp

import (
	"context"
	"errors"

	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/telemetry/trace"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

// Pre-computed attribute keys to avoid allocations on every call.
//
// The JSON-RPC method is "mcp.rpc.method" rather than the more obvious
// "mcp.method" because the HTTP instrumentation already owns that one, for the
// verb. Two keys of the same name meaning different things on different spans is
// the drift this convention exists to prevent: a dashboard counting by mcp.method
// would mix POST with tools/call.
var (
	attrKeyMethod = integration.Key(identifier, "rpc.method")
	attrKeyTool   = integration.Key(identifier, "tool")
	attrKeyPrompt = integration.Key(identifier, "prompt")
	attrKeyURI    = integration.Key(identifier, "uri")
)

// errToolResult is what a tool call is recorded as when its result reports an
// error without saying which.
var errToolResult = errors.New("tool call returned an error result")

// middlewareTelemetry returns the SDK receiving middleware that traces and
// measures each MCP method the server handles.
//
// Without it this integration emitted exactly one span per request — the HTTP
// one — which says a POST arrived at /mcp. Every MCP call is a POST to that same
// path, so the HTTP layer is precisely the layer that cannot tell a tool call
// from a resource read, let alone say which tool.
//
// It is receiving rather than sending middleware: this is a server, so the
// interesting direction is the one where somebody else's request arrives.
func (m *mcp) middlewareTelemetry() mcpsdk.Middleware {
	return func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {

			// Named for the method alone. The SDK only reaches this middleware for a
			// method it serves, so the set of names is fixed; the tool, prompt or
			// resource a request names is chosen by the client before anything checks
			// it, and goes in an attribute instead. Internal, because the server span
			// for the request is the HTTP one this nests under.
			ctx, op := m.inst.StartOperation(ctx, trace.SpanKindInternal, method)
			defer op.End(ctx)

			if op.IsRecording() {
				attrs := make([]attribute.KeyValue, 0, 2)
				attrs = append(attrs, attrKeyMethod.String(method))
				op.SetAttributes(appendSubject(attrs, req)...)
			}

			result, err := next(ctx, method, req)
			op.RecordError("failed to handle request", err)

			// A tool that fails reports it in its result rather than as an error, so
			// that the model calling it can read what went wrong. To everything else
			// it is a failed call all the same.
			if res, ok := result.(*mcpsdk.CallToolResult); ok && res != nil && res.IsError {
				cause := res.GetError()
				if cause == nil {
					cause = errToolResult
				}

				op.RecordError("tool call returned an error", cause)
			}

			return result, err
		}
	}
}

// appendSubject appends the attribute naming what a request acted on — which
// tool, which prompt, which resource — for the methods that act on something.
func appendSubject(attrs []attribute.KeyValue, req mcpsdk.Request) []attribute.KeyValue {
	switch params := req.GetParams().(type) {
	case *mcpsdk.CallToolParamsRaw:
		if params != nil {
			return append(attrs, attrKeyTool.String(params.Name))
		}
	case *mcpsdk.GetPromptParams:
		if params != nil {
			return append(attrs, attrKeyPrompt.String(params.Name))
		}
	case *mcpsdk.ReadResourceParams:
		if params != nil {
			return append(attrs, attrKeyURI.String(params.URI))
		}
	}

	return attrs
}
