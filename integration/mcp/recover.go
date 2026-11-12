package mcp

import (
	"context"
	"errors"
	"net/http"

	"github.com/mountayaapp/helix.go/integration"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// recoverPanics answers a request whose handling panicked on its own goroutine
// with the 500 error envelope, INTERNAL_ERROR, the shape of the other HTTP-layer
// errors this integration raises itself, when nothing of its response has been
// sent yet.
//
// Without it such a panic unwinds to net/http, which closes the connection with
// no status line at all and prints the panic and its stack to the process's
// standard error: an MCP client sees its transport drop, with nothing to tell a
// failure of the server from a network one. The envelope says nothing of the
// panic, whose value and stack are logged and recorded on the request's span.
//
// It sits just inside Config.EdgeMiddleware, so it covers Config.Middleware, the
// probes, the protected resource metadata and the transport, and the 500 it
// writes carries whatever the edge put on the response before passing the request
// on, and none of the headers set further in for the answer that never completed.
// A panic in EdgeMiddleware itself is not answered.
//
// A panic once the response has started, as on a stream already flushed, cannot
// be answered any more: it is logged and recorded the same way, and the response
// is aborted with http.ErrAbortHandler. http.ErrAbortHandler itself goes on
// unchanged.
//
// What it covers is the request's own goroutine: the middlewares and the
// transport's handling of the HTTP request. The SDK handles an MCP method, a
// tool call among them, on a goroutine of its own, which middlewareRecover
// covers.
//
// The recovery itself is the one every HTTP server integration shares
// (integration.RecoverPanics).
func recoverPanics(next http.Handler) http.Handler {
	return integration.RecoverPanics(next, "Recovered from a panic while serving an MCP request", nil)
}

// panicAnswer is all a client is told of an MCP method whose handling panicked:
// the text of the tool error a tool call is answered with, and the message of
// the JSON-RPC error any other method is.
const panicAnswer = "Internal server error"

// errToolCallPanicked is the error a tool call whose handler panicked is
// answered with, as the tool result holds it for the server's own telemetry. It
// is never sent: the result's text is panicAnswer.
var errToolCallPanicked = errors.New("tool call panicked")

// middlewareRecover returns the SDK receiving middleware that answers an MCP
// method whose handling panicked.
//
// The SDK handles each method on a goroutine of its own, outside the HTTP chain,
// and recovers nothing: a panic in a tool's handler, a prompt's, a resource's or
// a receiving middleware's would end the process, every session it served with
// it. It is recovered on that goroutine instead. A tool call is answered with a
// tool error, IsError and panicAnswer as its one text, as a handler returning an
// error is answered, so the model calling the tool reads that it failed. Any other
// method is answered with the JSON-RPC internal error (-32603), panicAnswer as its
// message. Neither says anything of the panic: its value and stack are logged and
// recorded on the method's span (integration.RecordPanic). The session goes on
// serving its other calls.
//
// It is added just inside middlewareTelemetry, so the panic is recorded on the
// method's own span, and outside every middleware the application added.
func (m *mcp) middlewareRecover() mcpsdk.Middleware {
	return func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, method string, req mcpsdk.Request) (result mcpsdk.Result, err error) {
			defer func() {
				v := recover()
				if v == nil {
					return
				}

				integration.RecordPanic(ctx, "Recovered from a panic while handling an MCP method", "failed to handle request", v)

				if _, ok := req.(*mcpsdk.CallToolRequest); ok {
					answer := &mcpsdk.CallToolResult{
						Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: panicAnswer}},
					}
					answer.SetError(errToolCallPanicked)

					result, err = answer, nil

					return
				}

				result, err = nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: panicAnswer}
			}()

			return next(ctx, method, req)
		}
	}
}
