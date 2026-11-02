package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service/servicetest"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// greetInput is the toy tool's typed input. The jsonschema struct tag is
// consumed by the SDK to auto-populate the tool's input schema.
type greetInput struct {
	Name string `json:"name" jsonschema:"the person to greet"`
}

// greetOutput is the toy tool's typed output, driving the auto-generated output
// schema.
type greetOutput struct {
	Greeting string `json:"greeting" jsonschema:"the rendered greeting"`
}

// newTestMCP builds an *mcp exactly like New does (server, routes and handler
// chain), but without an http.Server, so that the handler can be exercised
// directly or mounted on an httptest.Server.
//
// Its instrument uses the global providers, so a test that calls servicetest.New
// first records what the server emits.
func newTestMCP(t *testing.T, cfg Config) *mcp {
	t.Helper()

	require.NoError(t, cfg.sanitize())

	inst, err := integration.NewInstrument(integration.InstrumentConfig{
		Identifier: identifier,
		Humanized:  humanized,
		Name:       cfg.name(),
	})
	require.NoError(t, err)

	m, err := newMCP(&cfg, inst, integration.HTTPProbes{Readiness: cfg.Readiness})
	require.NoError(t, err)

	return m
}

// toyConfig returns a Config registering a read-only "greet" tool, and a "fail"
// tool whose handler always returns an error.
func toyConfig() Config {
	return Config{
		ServerInfo: ServerInfo{Name: "test-server", Version: "v1.0.0"},
		Register: func(server *Server) {
			AddTool(server, &Tool{
				Name:        "greet",
				Description: "Greets a person by name.",
				Annotations: &ToolAnnotations{
					Title:        "Greet",
					ReadOnlyHint: true,
				},
			}, func(_ context.Context, _ *CallToolRequest, in greetInput) (*CallToolResult, greetOutput, error) {
				return &CallToolResult{
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "Hi " + in.Name}},
				}, greetOutput{Greeting: "Hi " + in.Name}, nil
			})

			AddTool(server, &Tool{
				Name:        "fail",
				Description: "Always fails.",
			}, func(_ context.Context, _ *CallToolRequest, _ greetInput) (*CallToolResult, greetOutput, error) {
				return nil, greetOutput{}, errors.New("downstream is unavailable")
			})
		},
	}
}

// connect connects a real MCP client to the MCP transport at base.
func connect(t *testing.T, base string) *mcpsdk.ClientSession {
	t.Helper()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v1.0.0"}, nil)
	session, err := client.Connect(t.Context(), &mcpsdk.StreamableClientTransport{
		Endpoint:             base + "/mcp",
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	return session
}

// newPost returns a POST to the MCP transport carrying body, with the headers
// the Streamable HTTP transport requires.
func newPost(target, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	return req
}

// findSpan returns the first ended span named name, failing the test when there
// is none.
func findSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()

	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}

	require.Failf(t, "span not found", "no span named %q", name)
	return nil
}

func TestNew_NilService(t *testing.T) {
	err := New(t.Context(), nil, toyConfig())

	assert.Equal(t, errorstack.NewValidation(errorstack.Entry{
		Message: "Must be set",
		Path:    []any{"service"},
	}), err)
}

// TestNew_RegisterPanic pins that a mistake the SDK reports by panicking — here
// a tool whose input is not an object, which AddTool cannot build a schema for —
// fails New instead of the process.
func TestNew_RegisterPanic(t *testing.T) {
	svc, _ := servicetest.New(t)

	cfg := teardownConfig()
	cfg.Register = func(server *Server) {
		AddTool(server, &Tool{Name: "broken"}, func(_ context.Context, _ *CallToolRequest, _ int) (*CallToolResult, any, error) {
			return nil, nil, nil
		})
	}

	var err error
	require.NotPanics(t, func() { err = New(t.Context(), svc, cfg) })
	require.Error(t, err)
	assert.Equal(t, "Failed to build MCP server", errorstack.EntriesOf(err)[0].Message)
}

// TestNew_InvalidServerOptions pins that ServerOptions reach the SDK, and that
// one it refuses by panicking fails New.
func TestNew_InvalidServerOptions(t *testing.T) {
	svc, _ := servicetest.New(t)

	cfg := teardownConfig()
	cfg.ServerOptions = &mcpsdk.ServerOptions{PageSize: -1}

	var err error
	require.NotPanics(t, func() { err = New(t.Context(), svc, cfg) })
	assert.Error(t, err)
}

func TestMCP_Liveness_ReturnsOK(t *testing.T) {
	m := newTestMCP(t, toyConfig())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.JSONEq(t, `{"data":null}`, rw.Body.String())
}

func TestMCP_Readiness_CustomReady(t *testing.T) {
	cfg := toyConfig()
	cfg.Readiness = func(_ *http.Request) int { return http.StatusOK }
	m := newTestMCP(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.JSONEq(t, `{"data":null}`, rw.Body.String())
}

func TestMCP_Readiness_CustomUnavailable(t *testing.T) {
	cfg := toyConfig()
	cfg.Readiness = func(_ *http.Request) int { return http.StatusServiceUnavailable }
	m := newTestMCP(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusServiceUnavailable, rw.Code)
	assert.Contains(t, rw.Body.String(), `"code":"SERVICE_UNAVAILABLE"`)
}

// TestMCP_Readiness_GenericBody pins that an unhealthy Service answers the
// readiness probe without saying why: the reason a dependency failed routinely
// names internal hosts, and the probe is served to whoever asks.
func TestMCP_Readiness_GenericBody(t *testing.T) {
	m := newTestMCP(t, toyConfig())
	m.probes.CheckHealth = func(context.Context) error {
		return errorstack.New("Integration is not in a healthy state",
			errorstack.WithExtension("reason", "dial tcp db.internal:5432: connection refused"),
		)
	}
	m.buildMux()
	m.handler = m.buildHandler()

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusServiceUnavailable, rw.Code)
	assert.JSONEq(t, `{"errors":[{"message":"Service is temporarily unavailable","extensions":{"code":"SERVICE_UNAVAILABLE"}}]}`, rw.Body.String())
	assert.NotContains(t, rw.Body.String(), "db.internal")
}

func TestMCP_UnknownRoute_ReturnsNotFound(t *testing.T) {
	m := newTestMCP(t, toyConfig())

	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusNotFound, rw.Code)
	assert.Contains(t, rw.Body.String(), `"code":"NOT_FOUND"`)
}

// TestMCP_MethodNotAllowed covers the integration's own method-not-allowed
// fallback, which answers methods the mux does not route to the transport at all
// (GET, POST, and DELETE are routed; PUT is not). It is therefore the only 405
// carrying the helix response envelope.
func TestMCP_MethodNotAllowed(t *testing.T) {
	m := newTestMCP(t, toyConfig())

	req := httptest.NewRequest(http.MethodPut, "/mcp", nil)
	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rw.Code)
	assert.Contains(t, rw.Body.String(), `"code":"METHOD_NOT_ALLOWED"`)
}

// TestMCP_Stateless_GetAndDeleteNotAllowed pins the POST-only contract of the
// stateless protocol revision that Config.Stateful defaults to. GET and DELETE
// are routed to the transport by the mux, so the 405 here comes from the MCP SDK
// rather than from the fallback above: it advertises Allow: POST and carries no
// helix response envelope.
func TestMCP_Stateless_GetAndDeleteNotAllowed(t *testing.T) {
	m := newTestMCP(t, toyConfig())
	require.False(t, m.config.Stateful)

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/mcp", nil)
			rw := httptest.NewRecorder()
			m.handler.ServeHTTP(rw, req)

			assert.Equal(t, http.StatusMethodNotAllowed, rw.Code)
			assert.Equal(t, http.MethodPost, rw.Header().Get("Allow"))
			assert.NotContains(t, rw.Body.String(), `"code":"METHOD_NOT_ALLOWED"`)
		})
	}
}

// TestMCP_CrossOrigin_Rejected verifies the always-on cross-origin protection: a
// state-changing request to the transport carrying an untrusted cross-origin
// Origin header is rejected, while an originless (non-browser) request is not
// blocked by the protection.
func TestMCP_CrossOrigin_Rejected(t *testing.T) {
	m := newTestMCP(t, toyConfig())

	// A cross-origin POST (Origin does not match the request Host) is rejected.
	crossOrigin := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	crossOrigin.Header.Set("Origin", "https://evil.example")
	crossRW := httptest.NewRecorder()
	m.handler.ServeHTTP(crossRW, crossOrigin)

	assert.Equal(t, http.StatusForbidden, crossRW.Code)

	// An originless POST (no Origin header) passes the cross-origin protection and
	// reaches the MCP transport, so it is not rejected as forbidden.
	originless := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	originlessRW := httptest.NewRecorder()
	m.handler.ServeHTTP(originlessRW, originless)

	assert.NotEqual(t, http.StatusForbidden, originlessRW.Code)
}

// TestMCP_LocalhostProtection pins the SDK's DNS-rebinding defense for loopback
// listeners, and that DisableLocalhostProtection reaches it: a request arriving
// on loopback with a public Host is refused, unless the protection is disabled,
// as it must be behind a proxy on the same host.
func TestMCP_LocalhostProtection(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("disabled=%t", disabled), func(t *testing.T) {
			cfg := toyConfig()
			cfg.DisableLocalhostProtection = disabled
			m := newTestMCP(t, cfg)

			srv := httptest.NewServer(m.handler)
			defer srv.Close()

			req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader("{}"))
			require.NoError(t, err)
			req.Host = "api.example.com"
			req.Header.Set("Content-Type", "application/json")

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			if disabled {
				assert.NotEqual(t, http.StatusForbidden, resp.StatusCode)
			} else {
				assert.Equal(t, http.StatusForbidden, resp.StatusCode)
			}
		})
	}
}

// TestMCP_AllowedHosts verifies that, when set, the transport refuses a request
// for a Host it does not list, while the probes stay reachable whatever Host.
func TestMCP_AllowedHosts(t *testing.T) {
	cfg := toyConfig()
	cfg.AllowedHosts = []string{"api.example.com"}
	m := newTestMCP(t, cfg)

	rebound := httptest.NewRequest(http.MethodPost, "http://rebound.example/mcp", nil)
	reboundRW := httptest.NewRecorder()
	m.handler.ServeHTTP(reboundRW, rebound)

	assert.Equal(t, http.StatusForbidden, reboundRW.Code)
	assert.Contains(t, reboundRW.Body.String(), `"code":"FORBIDDEN"`)

	allowed := httptest.NewRequest(http.MethodPost, "http://API.example.com:8443/mcp", nil)
	allowedRW := httptest.NewRecorder()
	m.handler.ServeHTTP(allowedRW, allowed)

	assert.NotEqual(t, http.StatusForbidden, allowedRW.Code)

	probe := httptest.NewRequest(http.MethodGet, "http://10.0.0.1:8080/health", nil)
	probeRW := httptest.NewRecorder()
	m.handler.ServeHTTP(probeRW, probe)

	assert.Equal(t, http.StatusOK, probeRW.Code)
}

func TestHostAllowed(t *testing.T) {
	testcases := []struct {
		host    string
		allowed []string
		want    bool
	}{
		{host: "api.example.com", allowed: []string{"api.example.com"}, want: true},
		{host: "api.example.com:8443", allowed: []string{"api.example.com"}, want: true},
		{host: "API.EXAMPLE.COM", allowed: []string{"api.example.com"}, want: true},
		{host: "api.example.com:8443", allowed: []string{"api.example.com:8443"}, want: true},
		{host: "api.example.com:9000", allowed: []string{"api.example.com:8443"}, want: false},
		{host: "[::1]:8080", allowed: []string{"::1"}, want: true},
		{host: "[::1]:8080", allowed: []string{"[::1]"}, want: true},
		{host: "evil.example", allowed: []string{"api.example.com"}, want: false},
		{host: "api.example.com.evil.example", allowed: []string{"api.example.com"}, want: false},
	}

	for _, tc := range testcases {
		t.Run(tc.host, func(t *testing.T) {
			assert.Equal(t, tc.want, hostAllowed(tc.host, tc.allowed))
		})
	}
}

// TestMCP_MaxRequestBodyBytes verifies Config.MaxRequestBodyBytes reaches the
// transport.
func TestMCP_MaxRequestBodyBytes(t *testing.T) {
	cfg := toyConfig()
	cfg.MaxRequestBodyBytes = 64
	m := newTestMCP(t, cfg)

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"padding":"` + strings.Repeat("x", 128) + `"}}`
	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, newPost("/mcp", body))

	assert.Equal(t, http.StatusRequestEntityTooLarge, rw.Code)
}

// TestMCP_TransportOptions pins the precedence between Config and
// TransportOptions: the options the Config owns win, everything else passes
// through.
func TestMCP_TransportOptions(t *testing.T) {
	cfg := toyConfig()
	cfg.TransportOptions = &mcpsdk.StreamableHTTPOptions{
		Stateless:           false,
		JSONResponse:        true,
		SessionTimeout:      time.Second,
		MaxRequestBodyBytes: -1,
	}
	m := newTestMCP(t, cfg)

	opts := m.transportOptions()
	assert.True(t, opts.Stateless, "Stateless follows Config.Stateful")
	assert.True(t, opts.JSONResponse, "options the Config does not own pass through")
	assert.Zero(t, opts.SessionTimeout, "SessionTimeout follows Config.SessionTimeout")
	assert.Equal(t, int64(mcpsdk.DefaultMaxRequestBodyBytes), opts.MaxRequestBodyBytes)
	assert.False(t, cfg.TransportOptions.Stateless, "the caller's options are not modified")
}

// TestMCP_ListAndCallTool_RoundTrip drives the integration's actual handler with a
// real MCP client over HTTP, exercising tools/list and tools/call end-to-end.
func TestMCP_ListAndCallTool_RoundTrip(t *testing.T) {
	m := newTestMCP(t, toyConfig())

	srv := httptest.NewServer(m.handler)
	defer srv.Close()

	session := connect(t, srv.URL)

	// tools/list returns the registered tools with their annotations.
	tools, err := session.ListTools(t.Context(), &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	require.Len(t, tools.Tools, 2)

	var greet *mcpsdk.Tool
	for _, tool := range tools.Tools {
		if tool.Name == "greet" {
			greet = tool
		}
	}
	require.NotNil(t, greet)
	require.NotNil(t, greet.Annotations)
	assert.True(t, greet.Annotations.ReadOnlyHint)
	assert.Equal(t, "Greet", greet.Annotations.Title)
	assert.NotNil(t, greet.InputSchema)

	// tools/call invokes the tool and returns its content.
	res, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "Ada"},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok)
	assert.Equal(t, "Hi Ada", text.Text)
}

// TestMCP_Stateless_RegisterCalledOnce pins that the MCP server is built once,
// in New, rather than per request: building it means reflecting every tool's
// JSON schemas, and the SDK accepts one server for every request.
func TestMCP_Stateless_RegisterCalledOnce(t *testing.T) {
	cfg := toyConfig()
	register := cfg.Register

	calls := 0
	cfg.Register = func(server *Server) {
		calls++
		register(server)
	}

	m := newTestMCP(t, cfg)
	require.Equal(t, 1, calls)

	srv := httptest.NewServer(m.handler)
	defer srv.Close()

	session := connect(t, srv.URL)
	for range 3 {
		_, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{
			Name:      "greet",
			Arguments: map[string]any{"name": "Ada"},
		})
		require.NoError(t, err)
	}

	assert.Equal(t, 1, calls)
}

// TestMCP_Telemetry_ToolCall pins how a tool call is traced: one span per
// method, named for the method and never for the client-chosen tool name, which
// goes in an attribute instead; internal kind, since the HTTP span is the server
// one; and an error status when the tool's result reports an error, even though
// the SDK hands that back as a result rather than an error.
func TestMCP_Telemetry_ToolCall(t *testing.T) {
	_, rec := servicetest.New(t)
	m := newTestMCP(t, toyConfig())

	srv := httptest.NewServer(m.handler)
	defer srv.Close()

	session := connect(t, srv.URL)
	res, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "fail",
		Arguments: map[string]any{"name": "Ada"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)

	spans := rec.Spans()
	for _, span := range spans {
		assert.NotContains(t, span.Name(), "fail", "a client-chosen tool name must not mint a span name")
	}

	call := findSpan(t, spans, "MCP: tools/call")
	assert.Equal(t, "internal", call.SpanKind().String())
	assert.Equal(t, codes.Error, call.Status().Code)

	attrs := map[string]string{}
	for _, attr := range call.Attributes() {
		attrs[string(attr.Key)] = attr.Value.String()
	}
	assert.Equal(t, "fail", attrs["mcp.tool"])
	assert.Equal(t, "tools/call", attrs["mcp.rpc.method"])

	var exception bool
	for _, event := range call.Events() {
		if event.Name == "exception" {
			exception = true
		}
	}
	assert.True(t, exception, "the tool's error is recorded on the span")
}

// TestMCP_PublicEndpoint verifies Config.PublicEndpoint reaches the HTTP
// instrumentation: the caller's trace is linked to rather than continued.
func TestMCP_PublicEndpoint(t *testing.T) {
	_, rec := servicetest.New(t)

	cfg := toyConfig()
	cfg.PublicEndpoint = true
	m := newTestMCP(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	m.handler.ServeHTTP(httptest.NewRecorder(), req)

	span := findSpan(t, rec.Spans(), "MCP: GET")
	assert.False(t, span.Parent().IsValid(), "a public endpoint starts a new trace")
	require.Len(t, span.Links(), 1)
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", span.Links()[0].SpanContext.TraceID().String())
}

// TestMCP_RootPath verifies the integration serves the transport at the root path
// ("/") and only there: other paths still answer 404, and an unsupported method
// on the root answers 405, rather than the root acting as a prefix that catches
// every path.
func TestMCP_RootPath(t *testing.T) {
	cfg := toyConfig()
	cfg.Path = "/"
	m := newTestMCP(t, cfg)

	// An originless POST to the root reaches the transport (not 404 / 405).
	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, httptest.NewRequest(http.MethodPost, "/", nil))
	assert.NotEqual(t, http.StatusNotFound, rw.Code)
	assert.NotEqual(t, http.StatusMethodNotAllowed, rw.Code)

	// A POST anywhere else does not.
	elsewhereRW := httptest.NewRecorder()
	m.handler.ServeHTTP(elsewhereRW, httptest.NewRequest(http.MethodPost, "/elsewhere", nil))
	assert.Equal(t, http.StatusNotFound, elsewhereRW.Code)
	assert.Contains(t, elsewhereRW.Body.String(), `"code":"NOT_FOUND"`)

	unsupportedRW := httptest.NewRecorder()
	m.handler.ServeHTTP(unsupportedRW, httptest.NewRequest(http.MethodPut, "/", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, unsupportedRW.Code)
	assert.Contains(t, unsupportedRW.Body.String(), `"code":"METHOD_NOT_ALLOWED"`)
}

// TestMCP_RootPath_ProbesDoNotReachTransport pins that a request for a probe
// path, which bypasses Config.Middleware, cannot reach the transport. With the
// root registered as a prefix, a POST to /health did: past the consumer's
// authentication, straight into the MCP server.
func TestMCP_RootPath_ProbesDoNotReachTransport(t *testing.T) {
	cfg := toyConfig()
	cfg.Path = "/"
	cfg.OAuth = validOAuth()
	cfg.OAuth.Compose = true
	cfg.Middleware = func(http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
			rw.WriteHeader(http.StatusUnauthorized)
		})
	}
	m := newTestMCP(t, cfg)

	for _, path := range []string{"/health", "/ready", "/.well-known/oauth-protected-resource"} {
		t.Run(path, func(t *testing.T) {
			rw := httptest.NewRecorder()
			m.handler.ServeHTTP(rw, newPost(path, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))

			assert.NotEqual(t, http.StatusOK, rw.Code)
			assert.NotContains(t, rw.Body.String(), "jsonrpc")
		})
	}

	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, newPost("/", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	assert.Equal(t, http.StatusUnauthorized, rw.Code, "the transport stays behind the consumer's middleware")
}

// TestMCP_Middleware_Order pins that Config.Middleware runs inside the entry
// span, so that a response it writes itself is traced.
func TestMCP_Middleware_Order(t *testing.T) {
	_, rec := servicetest.New(t)

	cfg := toyConfig()
	cfg.Middleware = func(http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
			rw.WriteHeader(http.StatusTooManyRequests)
		})
	}
	m := newTestMCP(t, cfg)

	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	require.Equal(t, http.StatusTooManyRequests, rw.Code)

	span := findSpan(t, rec.Spans(), "MCP: POST")

	var status int64
	for _, attr := range span.Attributes() {
		if attr.Key == "mcp.status_code" {
			status = attr.Value.AsInt64()
		}
	}
	assert.Equal(t, int64(http.StatusTooManyRequests), status)
}

// readBody reads and closes resp's body.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()

	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return string(b)
}
