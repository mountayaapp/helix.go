package mcp

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mountaya/helix.go/service/servicetest"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// panickingMethodsConfig returns the toy server's Config with an "explode" tool
// and an "explode" prompt whose handlers panic with panicValue, on the goroutine
// the SDK handles each method on.
func panickingMethodsConfig() Config {
	cfg := toyConfig()

	register := cfg.Register
	cfg.Register = func(server *Server) {
		register(server)

		AddTool(server, &Tool{Name: "explode", Description: "Panics."}, func(context.Context, *CallToolRequest, greetInput) (*CallToolResult, greetOutput, error) {
			panic(panicValue)
		})

		server.AddPrompt(&mcpsdk.Prompt{Name: "explode", Description: "Panics."}, func(context.Context, *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
			panic(panicValue)
		})
	}

	return cfg
}

// holdsThePanic reports whether text holds anything of panicValue or of a stack.
func holdsThePanic(text string) bool {
	for _, piece := range []string{"tok_", "exchange failed", "goroutine", ".go:"} {
		if strings.Contains(text, piece) {
			return true
		}
	}

	return false
}

// toolCallSpan returns the ended "MCP: tools/call" span of the call to tool.
func toolCallSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, tool string) sdktrace.ReadOnlySpan {
	t.Helper()

	for _, span := range spans {
		if span.Name() != "MCP: tools/call" {
			continue
		}

		for _, attr := range span.Attributes() {
			if string(attr.Key) == "mcp.tool" && attr.Value.AsString() == tool {
				return span
			}
		}
	}

	require.FailNowf(t, "no span", "no tools/call span for %q", tool)
	return nil
}

// TestRecover_APanickingToolIsAToolError pins what a client calling a tool whose
// handler panics is answered: the tool error a handler returning an error is
// answered with, saying "Internal server error" and nothing of the panic. The SDK
// handles the call on a goroutine of its own, where the panic used to end the
// process; the session goes on serving its other calls, and the value and the
// stack are on the call's span.
func TestRecover_APanickingToolIsAToolError(t *testing.T) {
	_, rec := servicetest.New(t)
	m := newTestMCP(t, panickingMethodsConfig())

	srv := httptest.NewServer(m.handler)
	defer srv.Close()

	session := connect(t, srv.URL)

	res, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "explode",
		Arguments: map[string]any{"name": "Ada"},
	})
	require.NoError(t, err, "a panicking tool is a tool error, not a failed call")
	assert.True(t, res.IsError)
	require.Len(t, res.Content, 1)

	text, ok := res.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok)
	assert.Equal(t, "Internal server error", text.Text)
	assert.Nil(t, res.StructuredContent)

	encoded, err := json.Marshal(res)
	require.NoError(t, err)
	assert.False(t, holdsThePanic(string(encoded)), "the result holds the panic: %s", encoded)

	greeted, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "Ada"},
	})
	require.NoError(t, err, "the session stopped serving after the panic")
	assert.False(t, greeted.IsError)

	span := toolCallSpan(t, rec.Spans(), "explode")

	recorded := map[string]string{}
	for _, event := range span.Events() {
		for _, attr := range event.Attributes {
			if _, seen := recorded[event.Name+"/"+string(attr.Key)]; !seen {
				recorded[event.Name+"/"+string(attr.Key)] = attr.Value.AsString()
			}
		}
	}

	assert.Equal(t, "panic: "+panicValue, recorded["exception/exception.message"])
	assert.Contains(t, recorded["panic/exception.stacktrace"], "method_recover_test.go", "the stack is not the panicking goroutine's")
}

// TestRecover_APanickingPromptIsAnInternalError pins what any other method whose
// handler panics is answered: the JSON-RPC internal error, saying "Internal server
// error" and nothing of the panic, and the session goes on.
func TestRecover_APanickingPromptIsAnInternalError(t *testing.T) {
	m := newTestMCP(t, panickingMethodsConfig())

	srv := httptest.NewServer(m.handler)
	defer srv.Close()

	session := connect(t, srv.URL)

	_, err := session.GetPrompt(t.Context(), &mcpsdk.GetPromptParams{Name: "explode"})
	require.Error(t, err)
	assert.False(t, holdsThePanic(err.Error()), "the error holds the panic: %v", err)

	wire, ok := errors.AsType[*jsonrpc.Error](err)
	require.True(t, ok, "err = %v, want a JSON-RPC error", err)
	assert.Equal(t, int64(jsonrpc.CodeInternalError), wire.Code)
	assert.Equal(t, "Internal server error", wire.Message)

	tools, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err, "the session stopped serving after the panic")
	assert.NotEmpty(t, tools.Tools)
}
