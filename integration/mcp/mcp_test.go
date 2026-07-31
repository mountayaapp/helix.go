package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// newTestMCP builds an *mcp wired exactly like New does (transport + mux), but
// without starting an http.Server, so the mux can be exercised directly or
// mounted on an httptest.Server.
func newTestMCP(t *testing.T, cfg Config) *mcp {
	t.Helper()

	require.NoError(t, cfg.sanitize())

	m := &mcp{config: &cfg}
	m.buildMux()

	return m
}

// toyConfig returns a Config registering a single read-only "greet" tool.
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
		},
	}
}

func TestMCP_Name(t *testing.T) {
	m := &mcp{config: &Config{}}
	assert.Equal(t, "mcp", m.Name())
}

func TestMCP_StatusAlwaysOK(t *testing.T) {
	m := &mcp{config: &Config{}}

	status, err := m.Status(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 200, status)
}

func TestMCP_Liveness_ReturnsOK(t *testing.T) {
	m := newTestMCP(t, toyConfig())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rw := httptest.NewRecorder()
	m.mux.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.JSONEq(t, `{"data":null}`, rw.Body.String())
}

func TestMCP_Readiness_CustomReady(t *testing.T) {
	cfg := toyConfig()
	cfg.Readiness = func(_ *http.Request) int { return http.StatusOK }
	m := newTestMCP(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rw := httptest.NewRecorder()
	m.mux.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.JSONEq(t, `{"data":null}`, rw.Body.String())
}

func TestMCP_Readiness_CustomUnavailable(t *testing.T) {
	cfg := toyConfig()
	cfg.Readiness = func(_ *http.Request) int { return http.StatusServiceUnavailable }
	m := newTestMCP(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rw := httptest.NewRecorder()
	m.mux.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusServiceUnavailable, rw.Code)
	assert.Contains(t, rw.Body.String(), `"code":"SERVICE_UNAVAILABLE"`)
}

func TestMCP_UnknownRoute_ReturnsNotFound(t *testing.T) {
	m := newTestMCP(t, toyConfig())

	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	rw := httptest.NewRecorder()
	m.mux.ServeHTTP(rw, req)

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
	m.mux.ServeHTTP(rw, req)

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
			m.mux.ServeHTTP(rw, req)

			assert.Equal(t, http.StatusMethodNotAllowed, rw.Code)
			assert.Equal(t, http.MethodPost, rw.Header().Get("Allow"))
			assert.NotContains(t, rw.Body.String(), `"code":"METHOD_NOT_ALLOWED"`)
		})
	}
}

// TestMCP_CrossOrigin_Rejected verifies the always-on DNS-rebinding defense: a
// state-changing request to the transport carrying an untrusted cross-origin
// Origin header is rejected, while an originless (non-browser) request is not
// blocked by the protection.
func TestMCP_CrossOrigin_Rejected(t *testing.T) {
	m := newTestMCP(t, toyConfig())

	// A cross-origin POST (Origin does not match the request Host) is rejected.
	crossOrigin := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	crossOrigin.Header.Set("Origin", "https://evil.example")
	crossRW := httptest.NewRecorder()
	m.mux.ServeHTTP(crossRW, crossOrigin)

	assert.Equal(t, http.StatusForbidden, crossRW.Code)

	// An originless POST (no Origin header) passes the cross-origin protection and
	// reaches the MCP transport, so it is not rejected as forbidden.
	originless := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	originlessRW := httptest.NewRecorder()
	m.mux.ServeHTTP(originlessRW, originless)

	assert.NotEqual(t, http.StatusForbidden, originlessRW.Code)
}

// TestMCP_ListAndCallTool_RoundTrip drives the integration's actual transport
// handler with a real MCP client over HTTP, exercising initialize, tools/list,
// and tools/call end-to-end.
func TestMCP_ListAndCallTool_RoundTrip(t *testing.T) {
	m := newTestMCP(t, toyConfig())

	srv := httptest.NewServer(m.mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint: srv.URL + "/mcp",
	}, nil)
	require.NoError(t, err)
	defer session.Close()

	// tools/list returns the single registered tool with its annotations.
	tools, err := session.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	require.Len(t, tools.Tools, 1)
	assert.Equal(t, "greet", tools.Tools[0].Name)
	require.NotNil(t, tools.Tools[0].Annotations)
	assert.True(t, tools.Tools[0].Annotations.ReadOnlyHint)
	assert.Equal(t, "Greet", tools.Tools[0].Annotations.Title)
	assert.NotNil(t, tools.Tools[0].InputSchema)

	// tools/call invokes the tool and returns its content.
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
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

// TestMCP_OAuth_Unauthenticated verifies the OAuth Resource Server strategy
// rejects unauthenticated transport requests with 401 + WWW-Authenticate and
// serves the Protected Resource Metadata document publicly.
func TestMCP_OAuth_Unauthenticated(t *testing.T) {
	cfg := toyConfig()
	cfg.OAuth = OAuthResourceServer{
		Enabled:              true,
		ResourceId:           "https://api.example.com",
		AuthorizationServers: []string{"https://auth.example.com"},
		ValidateToken: func(_ context.Context, _ string) (any, time.Time, error) {
			return map[string]any{"sub": "user-1"}, time.Now().Add(time.Hour), nil
		},
	}
	m := newTestMCP(t, cfg)

	// A request to the transport without a bearer token is rejected.
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rw := httptest.NewRecorder()
	m.mux.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusUnauthorized, rw.Code)
	assert.NotEmpty(t, rw.Header().Get("WWW-Authenticate"))

	// The Protected Resource Metadata endpoint is public and advertises the
	// resource and its authorization servers.
	prmReq := httptest.NewRequest(http.MethodGet, wellKnownProtectedResource, nil)
	prmRW := httptest.NewRecorder()
	m.mux.ServeHTTP(prmRW, prmReq)

	assert.Equal(t, http.StatusOK, prmRW.Code)
	assert.Contains(t, prmRW.Body.String(), "https://api.example.com")
	assert.Contains(t, prmRW.Body.String(), "https://auth.example.com")
}

// TestMCP_OAuth_ComposeMode verifies that in compose mode the integration mounts
// the public Protected Resource Metadata endpoint but does NOT auto-gate the
// transport (the consumer composes BearerMiddleware itself), while the exported
// BearerMiddleware still enforces bearer tokens when applied.
func TestMCP_OAuth_ComposeMode(t *testing.T) {
	cfg := toyConfig()
	cfg.OAuth = OAuthResourceServer{
		Enabled:              true,
		Compose:              true,
		ResourceId:           "https://api.example.com",
		AuthorizationServers: []string{"https://auth.example.com"},
		ValidateToken: func(_ context.Context, _ string) (any, time.Time, error) {
			return map[string]any{"sub": "user-1"}, time.Now().Add(time.Hour), nil
		},
	}
	m := newTestMCP(t, cfg)

	// The Protected Resource Metadata endpoint is public.
	prmReq := httptest.NewRequest(http.MethodGet, wellKnownProtectedResource, nil)
	prmRW := httptest.NewRecorder()
	m.mux.ServeHTTP(prmRW, prmReq)
	assert.Equal(t, http.StatusOK, prmRW.Code)

	// The transport is NOT auto-gated: an originless request reaches it rather than
	// being rejected with 401 by a bearer middleware the integration did not apply.
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rw := httptest.NewRecorder()
	m.mux.ServeHTTP(rw, req)
	assert.NotEqual(t, http.StatusUnauthorized, rw.Code)

	// The exported BearerMiddleware still enforces bearer tokens when composed by
	// the consumer: a request without a token is rejected with 401 + WWW-Authenticate.
	gated := cfg.OAuth.BearerMiddleware()(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusOK)
	}))
	gatedRW := httptest.NewRecorder()
	gated.ServeHTTP(gatedRW, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	assert.Equal(t, http.StatusUnauthorized, gatedRW.Code)
	assert.NotEmpty(t, gatedRW.Header().Get("WWW-Authenticate"))
}

// TestMCP_RootPath verifies the integration serves the transport at the root path
// ("/") without the duplicate-pattern panic that registering both the
// method-not-allowed and the not-found handlers at "/" would otherwise trigger.
func TestMCP_RootPath(t *testing.T) {
	cfg := toyConfig()
	cfg.Path = "/"
	require.NoError(t, cfg.sanitize())

	m := &mcp{config: &cfg}

	// Building the mux at the root path must not panic on a duplicate "/" pattern.
	require.NotPanics(t, m.buildMux)

	// An originless POST to the root reaches the transport (not 404 / 405).
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rw := httptest.NewRecorder()
	m.mux.ServeHTTP(rw, req)
	assert.NotEqual(t, http.StatusNotFound, rw.Code)
	assert.NotEqual(t, http.StatusMethodNotAllowed, rw.Code)

	// At the root path no method-not-allowed handler is registered (it would collide
	// with the catch-all "/" pattern), so a request with an unsupported method falls
	// through to the not-found handler.
	unsupportedRW := httptest.NewRecorder()
	m.mux.ServeHTTP(unsupportedRW, httptest.NewRequest(http.MethodPut, "/", nil))
	assert.Equal(t, http.StatusNotFound, unsupportedRW.Code)
}

// TestMCP_OAuth_Authenticated verifies a valid bearer token passes the gate and
// reaches the transport. It is the regression guard for the TokenInfo.Expiration
// the MCP SDK requires: without a non-zero, future expiration the SDK rejects
// every otherwise-valid token with 401 "token missing expiration".
func TestMCP_OAuth_Authenticated(t *testing.T) {
	cfg := toyConfig()
	cfg.OAuth = OAuthResourceServer{
		Enabled:              true,
		ResourceId:           "https://api.example.com",
		AuthorizationServers: []string{"https://auth.example.com"},
		ValidateToken: func(_ context.Context, _ string) (any, time.Time, error) {
			return map[string]any{"sub": "user-1"}, time.Now().Add(time.Hour), nil
		},
	}
	m := newTestMCP(t, cfg)

	// A request carrying a bearer token passes the gate (the verifier returns a
	// future expiration) and reaches the transport, so it is not rejected as 401.
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer any-token")
	rw := httptest.NewRecorder()
	m.mux.ServeHTTP(rw, req)

	assert.NotEqual(t, http.StatusUnauthorized, rw.Code)
}
