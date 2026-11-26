package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mountaya/helix.go/service/servicetest"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Bodies of the two requests that open a session on the 2025-11-25 revision a
// stateful server speaks.
const (
	initializeBody  = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test-client","version":"v1.0.0"}}}`
	initializedBody = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
)

// tokenFor returns a TokenInfo for subject, granting scopes and valid for an
// hour.
func tokenFor(subject string, scopes ...string) *TokenInfo {
	return &TokenInfo{
		Subject:    subject,
		Scopes:     scopes,
		Expiration: time.Now().Add(time.Hour),
	}
}

// oauthConfig returns toyConfig protected by OAuth, with validate as its token
// validator.
func oauthConfig(validate func(ctx context.Context, token string) (*TokenInfo, error)) Config {
	cfg := toyConfig()
	cfg.OAuth = validOAuth()
	cfg.OAuth.ValidateToken = validate

	return cfg
}

// bearer returns a POST to the transport carrying token as a bearer token.
func bearer(token string) *http.Request {
	req := newPost("/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	req.Header.Set("Authorization", "Bearer "+token)

	return req
}

func TestOAuthResourceServer_MetadataLocation(t *testing.T) {
	testcases := []struct {
		resourceID string
		wantURL    string
		wantPath   string
	}{
		{
			resourceID: "https://api.example.com",
			wantURL:    "https://api.example.com/.well-known/oauth-protected-resource",
			wantPath:   "/.well-known/oauth-protected-resource",
		},
		{
			resourceID: "https://api.example.com/",
			wantURL:    "https://api.example.com/.well-known/oauth-protected-resource",
			wantPath:   "/.well-known/oauth-protected-resource",
		},
		{
			resourceID: "https://api.example.com/mcp",
			wantURL:    "https://api.example.com/.well-known/oauth-protected-resource/mcp",
			wantPath:   "/.well-known/oauth-protected-resource/mcp",
		},
		{
			resourceID: "https://api.example.com:8443/tenant/mcp/",
			wantURL:    "https://api.example.com:8443/.well-known/oauth-protected-resource/tenant/mcp/",
			wantPath:   "/.well-known/oauth-protected-resource/tenant/mcp/",
		},
		{
			resourceID: "http://localhost:8080/mcp?v=1",
			wantURL:    "http://localhost:8080/.well-known/oauth-protected-resource/mcp?v=1",
			wantPath:   "/.well-known/oauth-protected-resource/mcp",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.resourceID, func(t *testing.T) {
			a := OAuthResourceServer{ResourceID: tc.resourceID}

			gotURL, gotPath, err := a.metadataLocation()
			require.NoError(t, err)
			assert.Equal(t, tc.wantURL, gotURL)
			assert.Equal(t, tc.wantPath, gotPath)
		})
	}
}

// TestMCP_OAuth_Unauthenticated verifies the OAuth Resource Server strategy
// rejects unauthenticated transport requests with 401 + WWW-Authenticate and
// serves the Protected Resource Metadata document publicly.
func TestMCP_OAuth_Unauthenticated(t *testing.T) {
	m := newTestMCP(t, oauthConfig(func(_ context.Context, _ string) (*TokenInfo, error) {
		return tokenFor("user-1"), nil
	}))

	// A request to the transport without a bearer token is rejected.
	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, httptest.NewRequest(http.MethodPost, "/mcp", nil))

	assert.Equal(t, http.StatusUnauthorized, rw.Code)
	assert.Equal(t, `Bearer resource_metadata="https://api.example.com/.well-known/oauth-protected-resource"`, rw.Header().Get("WWW-Authenticate"))

	// The Protected Resource Metadata endpoint is public and advertises the
	// resource and its authorization servers.
	prmRW := httptest.NewRecorder()
	m.handler.ServeHTTP(prmRW, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil))

	assert.Equal(t, http.StatusOK, prmRW.Code)
	assert.Contains(t, prmRW.Body.String(), "https://api.example.com")
	assert.Contains(t, prmRW.Body.String(), "https://auth.example.com")
}

// TestMCP_OAuth_PathfulResourceID pins RFC 9728 §3.1 for a resource identifier
// with a path: the well-known suffix goes between the host and the path, both in
// the challenge and in where the document is served. The metadata answers the
// CORS preflight a browser-based client sends before fetching it.
func TestMCP_OAuth_PathfulResourceID(t *testing.T) {
	cfg := oauthConfig(func(_ context.Context, _ string) (*TokenInfo, error) {
		return tokenFor("user-1"), nil
	})
	cfg.OAuth.ResourceID = "https://api.example.com/mcp"
	m := newTestMCP(t, cfg)

	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	require.Equal(t, http.StatusUnauthorized, rw.Code)
	assert.Contains(t, rw.Header().Get("WWW-Authenticate"),
		`resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp"`)

	prmRW := httptest.NewRecorder()
	m.handler.ServeHTTP(prmRW, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource/mcp", nil))
	assert.Equal(t, http.StatusOK, prmRW.Code)
	assert.Contains(t, prmRW.Body.String(), `"resource":"https://api.example.com/mcp"`)

	preflightRW := httptest.NewRecorder()
	m.handler.ServeHTTP(preflightRW, httptest.NewRequest(http.MethodOptions, "/.well-known/oauth-protected-resource/mcp", nil))
	assert.Equal(t, http.StatusNoContent, preflightRW.Code)
	assert.Equal(t, "*", preflightRW.Header().Get("Access-Control-Allow-Origin"))

	appendedRW := httptest.NewRecorder()
	m.handler.ServeHTTP(appendedRW, httptest.NewRequest(http.MethodGet, "/mcp/.well-known/oauth-protected-resource", nil))
	assert.Equal(t, http.StatusNotFound, appendedRW.Code)
}

// TestMCP_OAuth_Authenticated verifies a valid bearer token passes the gate and
// reaches the transport. It is the regression guard for the TokenInfo.Expiration
// the MCP SDK requires: without a non-zero, future expiration the SDK rejects
// every otherwise-valid token with 401 "token missing expiration".
func TestMCP_OAuth_Authenticated(t *testing.T) {
	m := newTestMCP(t, oauthConfig(func(_ context.Context, _ string) (*TokenInfo, error) {
		return tokenFor("user-1"), nil
	}))

	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, bearer("any-token"))

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Contains(t, rw.Body.String(), `"tools"`)
}

// TestMCP_OAuth_Scopes is the regression guard for scopes never being enforced:
// the required scopes used to be handed to the SDK as the token's granted ones,
// so its check compared the list against itself and any valid token passed. A
// token lacking a required scope now gets 403; one granting it gets through.
func TestMCP_OAuth_Scopes(t *testing.T) {
	cfg := oauthConfig(func(_ context.Context, token string) (*TokenInfo, error) {
		return tokenFor("user-1", strings.Fields(token)...), nil
	})
	cfg.OAuth.Scopes = []string{"mcp:tools"}
	m := newTestMCP(t, cfg)

	lackingRW := httptest.NewRecorder()
	m.handler.ServeHTTP(lackingRW, bearer("profile"))

	assert.Equal(t, http.StatusForbidden, lackingRW.Code)
	assert.Contains(t, lackingRW.Header().Get("WWW-Authenticate"), `scope="mcp:tools"`)

	grantedRW := httptest.NewRecorder()
	m.handler.ServeHTTP(grantedRW, bearer("mcp:tools"))

	assert.Equal(t, http.StatusOK, grantedRW.Code)
}

// TestMCP_OAuth_ValidateTokenError is the regression guard for validator errors
// reaching the client: the SDK answers an error it does not recognize with 500
// and the error's text, which named whatever the validator depended on, and no
// challenge. Every rejection is now a generic 401 with WWW-Authenticate, and the
// real error is recorded on the request's span.
func TestMCP_OAuth_ValidateTokenError(t *testing.T) {
	testcases := []struct {
		name string
		err  error
	}{
		{name: "validator failure", err: errors.New("fetch https://idp.internal/jwks: connection refused")},
		{name: "rejected token", err: fmt.Errorf("%w: signature does not match key idp.internal/k1", mcpauth.ErrInvalidToken)},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			_, rec := servicetest.New(t)
			m := newTestMCP(t, oauthConfig(func(_ context.Context, _ string) (*TokenInfo, error) {
				return nil, tc.err
			}))

			rw := httptest.NewRecorder()
			m.handler.ServeHTTP(rw, bearer("any-token"))

			assert.Equal(t, http.StatusUnauthorized, rw.Code)
			assert.NotEmpty(t, rw.Header().Get("WWW-Authenticate"))
			assert.NotContains(t, rw.Body.String(), "idp.internal")

			span := findSpan(t, rec.Spans(), "MCP: POST")
			var recorded bool
			for _, event := range span.Events() {
				for _, attr := range event.Attributes {
					if attr.Key == "exception.message" && attr.Value.AsString() == tc.err.Error() {
						recorded = true
					}
				}
			}
			assert.True(t, recorded, "the real error is recorded on the request's span")
		})
	}
}

// TestMCP_OAuth_TooManyRequests pins the answer to a token the validator could
// not verify for the volume of requests: 429, never the 401 that sends a client
// to authenticate again, with a Retry-After rounded up to whole seconds when the
// error names a wait, no challenge, a generic body, and the real error recorded
// on the request's span. An error that is also a verdict on the token stays 401.
func TestMCP_OAuth_TooManyRequests(t *testing.T) {
	testcases := []struct {
		name           string
		err            error
		wantStatus     int
		wantRetryAfter string
	}{
		{
			name:           "a wait named in fractions of a second",
			err:            &TooManyRequestsError{RetryAfter: 1500 * time.Millisecond, Err: errors.New("provider idp.internal answered 429")},
			wantStatus:     http.StatusTooManyRequests,
			wantRetryAfter: "2",
		},
		{
			name:           "a wait of whole seconds",
			err:            fmt.Errorf("verify: %w", &TooManyRequestsError{RetryAfter: 42 * time.Second}),
			wantStatus:     http.StatusTooManyRequests,
			wantRetryAfter: "42",
		},
		{
			name:       "a wait of zero",
			err:        &TooManyRequestsError{Err: errors.New("provider idp.internal answered 429")},
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name:       "the sentinel alone",
			err:        fmt.Errorf("%w: quota of idp.internal spent", ErrTooManyRequests),
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name:       "a verdict on the token as well",
			err:        fmt.Errorf("%w: %w: idp.internal", mcpauth.ErrInvalidToken, ErrTooManyRequests),
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			_, rec := servicetest.New(t)
			m := newTestMCP(t, oauthConfig(func(_ context.Context, _ string) (*TokenInfo, error) {
				return nil, tc.err
			}))

			rw := httptest.NewRecorder()
			m.handler.ServeHTTP(rw, bearer("any-token"))

			assert.Equal(t, tc.wantStatus, rw.Code)
			assert.Equal(t, tc.wantRetryAfter, rw.Header().Get("Retry-After"))
			assert.NotContains(t, rw.Body.String(), "idp.internal")

			if tc.wantStatus == http.StatusUnauthorized {
				assert.NotEmpty(t, rw.Header().Get("WWW-Authenticate"))
				return
			}

			assert.Empty(t, rw.Header().Get("WWW-Authenticate"))
			assert.Equal(t, "too many requests\n", rw.Body.String())

			span := findSpan(t, rec.Spans(), "MCP: POST")
			var recorded bool
			for _, event := range span.Events() {
				for _, attr := range event.Attributes {
					if attr.Key == "exception.message" && attr.Value.AsString() == tc.err.Error() {
						recorded = true
					}
				}
			}
			assert.True(t, recorded, "the real error is recorded on the request's span")
		})
	}
}

// TestOAuthResourceServer_BearerMiddleware_TooManyRequests pins the 429 through
// the exported middleware a consumer composes, and that a request the gate lets
// through reaches the guarded handler with the writer it arrived with: nothing
// the handler writes, a stream it flushes included, goes through a wrapper.
func TestOAuthResourceServer_BearerMiddleware_TooManyRequests(t *testing.T) {
	a := validOAuth()
	a.ValidateToken = func(_ context.Context, token string) (*TokenInfo, error) {
		if token == "limited" {
			return nil, &TooManyRequestsError{RetryAfter: 30 * time.Second}
		}

		return tokenFor("user-1"), nil
	}

	var reached http.ResponseWriter
	gated := a.BearerMiddleware()(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		reached = rw
		rw.WriteHeader(http.StatusOK)
	}))

	limitedRW := httptest.NewRecorder()
	gated.ServeHTTP(limitedRW, bearer("limited"))
	assert.Equal(t, http.StatusTooManyRequests, limitedRW.Code)
	assert.Equal(t, "30", limitedRW.Header().Get("Retry-After"))
	assert.Empty(t, limitedRW.Header().Get("WWW-Authenticate"))
	assert.Nil(t, reached, "a throttled request never reaches the guarded handler")

	acceptedRW := httptest.NewRecorder()
	gated.ServeHTTP(acceptedRW, bearer("accepted"))
	assert.Equal(t, http.StatusOK, acceptedRW.Code)
	assert.Same(t, acceptedRW, reached, "the guarded handler writes to the writer the request arrived with")

	missingRW := httptest.NewRecorder()
	gated.ServeHTTP(missingRW, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	assert.Equal(t, http.StatusUnauthorized, missingRW.Code)
	assert.NotEmpty(t, missingRW.Header().Get("WWW-Authenticate"))
}

// TestTooManyRequestsError pins the error's chain: it is ErrTooManyRequests, it
// unwraps to its cause, and its message names both.
func TestTooManyRequestsError(t *testing.T) {
	cause := errors.New("quota spent")
	err := error(&TooManyRequestsError{RetryAfter: time.Second, Err: cause})

	assert.ErrorIs(t, err, ErrTooManyRequests)
	assert.ErrorIs(t, err, cause)
	assert.NotErrorIs(t, err, mcpauth.ErrInvalidToken)
	assert.Equal(t, "too many requests: quota spent", err.Error())
	assert.Equal(t, "too many requests", (&TooManyRequestsError{}).Error())
}

// TestOAuthResourceServer_Verify_TooManyRequestsOutsideTheMiddleware pins that
// verify fails closed when it is reached without the middleware that answers
// 429: the throttled token is rejected as invalid rather than let through.
func TestOAuthResourceServer_Verify_TooManyRequestsOutsideTheMiddleware(t *testing.T) {
	a := validOAuth()
	a.ValidateToken = func(_ context.Context, _ string) (*TokenInfo, error) {
		return nil, &TooManyRequestsError{RetryAfter: time.Second}
	}

	info, err := a.verify(context.Background(), "any-token", nil)
	assert.Nil(t, info)
	assert.ErrorIs(t, err, mcpauth.ErrInvalidToken)
}

// TestMCP_OAuth_ServiceUnavailable pins the answer to a token the validator could
// not verify because what verifies it failed: 503, never the 401 that sends a
// client to authenticate again into the same failure, with a Retry-After rounded
// up to whole seconds when the error names a wait, no challenge, a generic body,
// the real error recorded on the request's span, and a warning logged. An error
// that is also a verdict on the token stays 401, and one that is also a rate
// limit is answered 429.
func TestMCP_OAuth_ServiceUnavailable(t *testing.T) {
	testcases := []struct {
		name           string
		err            error
		wantStatus     int
		wantRetryAfter string
		wantBody       string
	}{
		{
			name:           "a wait named in fractions of a second",
			err:            &ServiceUnavailableError{RetryAfter: 2500 * time.Millisecond, Err: errors.New("provider idp.internal answered 502")},
			wantStatus:     http.StatusServiceUnavailable,
			wantRetryAfter: "3",
			wantBody:       "service unavailable\n",
		},
		{
			name:           "a wait of whole seconds",
			err:            fmt.Errorf("verify: %w", &ServiceUnavailableError{RetryAfter: 5 * time.Second}),
			wantStatus:     http.StatusServiceUnavailable,
			wantRetryAfter: "5",
			wantBody:       "service unavailable\n",
		},
		{
			name:       "a wait of zero",
			err:        &ServiceUnavailableError{Err: errors.New("dial tcp idp.internal: connection refused")},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "service unavailable\n",
		},
		{
			name:       "the sentinel alone",
			err:        fmt.Errorf("%w: idp.internal is down", ErrServiceUnavailable),
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "service unavailable\n",
		},
		{
			name:       "a verdict on the token as well",
			err:        fmt.Errorf("%w: %w: idp.internal", mcpauth.ErrInvalidToken, ErrServiceUnavailable),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:           "a rate limit as well",
			err:            &ServiceUnavailableError{RetryAfter: 5 * time.Second, Err: &TooManyRequestsError{RetryAfter: 42 * time.Second}},
			wantStatus:     http.StatusTooManyRequests,
			wantRetryAfter: "42",
			wantBody:       "too many requests\n",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			_, rec := servicetest.New(t)
			m := newTestMCP(t, oauthConfig(func(_ context.Context, _ string) (*TokenInfo, error) {
				return nil, tc.err
			}))

			rw := httptest.NewRecorder()
			m.handler.ServeHTTP(rw, bearer("any-token"))

			assert.Equal(t, tc.wantStatus, rw.Code)
			assert.Equal(t, tc.wantRetryAfter, rw.Header().Get("Retry-After"))
			assert.NotContains(t, rw.Body.String(), "idp.internal")

			if tc.wantStatus == http.StatusUnauthorized {
				assert.NotEmpty(t, rw.Header().Get("WWW-Authenticate"))
				return
			}

			assert.Empty(t, rw.Header().Get("WWW-Authenticate"))
			assert.Equal(t, tc.wantBody, rw.Body.String())

			span := findSpan(t, rec.Spans(), "MCP: POST")
			var recorded bool
			for _, event := range span.Events() {
				for _, attr := range event.Attributes {
					if attr.Key == "exception.message" && attr.Value.AsString() == tc.err.Error() {
						recorded = true
					}
				}
			}
			assert.True(t, recorded, "the real error is recorded on the request's span")

			var logged bool
			for _, record := range rec.Records() {
				if record.Body().AsString() == "Failed to validate bearer token" {
					logged = true
				}
			}
			assert.Equal(t, tc.wantStatus == http.StatusServiceUnavailable, logged, "a failure of what verifies the token is logged, a rate limit is not")
		})
	}
}

// TestOAuthResourceServer_BearerMiddleware_ServiceUnavailable pins the 503
// through the exported middleware a consumer composes: the request never reaches
// the guarded handler, and the next one, verified, does.
func TestOAuthResourceServer_BearerMiddleware_ServiceUnavailable(t *testing.T) {
	a := validOAuth()
	a.ValidateToken = func(_ context.Context, token string) (*TokenInfo, error) {
		if token == "unverifiable" {
			return nil, &ServiceUnavailableError{RetryAfter: 5 * time.Second}
		}

		return tokenFor("user-1"), nil
	}

	var reached http.ResponseWriter
	gated := a.BearerMiddleware()(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		reached = rw
		rw.WriteHeader(http.StatusOK)
	}))

	unavailableRW := httptest.NewRecorder()
	gated.ServeHTTP(unavailableRW, bearer("unverifiable"))
	assert.Equal(t, http.StatusServiceUnavailable, unavailableRW.Code)
	assert.Equal(t, "5", unavailableRW.Header().Get("Retry-After"))
	assert.Empty(t, unavailableRW.Header().Get("WWW-Authenticate"))
	assert.Equal(t, "service unavailable\n", unavailableRW.Body.String())
	assert.Nil(t, reached, "a request whose token could not be verified never reaches the guarded handler")

	acceptedRW := httptest.NewRecorder()
	gated.ServeHTTP(acceptedRW, bearer("accepted"))
	assert.Equal(t, http.StatusOK, acceptedRW.Code)
	assert.Same(t, acceptedRW, reached, "the guarded handler writes to the writer the request arrived with")
	assert.Empty(t, acceptedRW.Header().Get("Retry-After"))
}

// TestServiceUnavailableError pins the error's chain: it is
// ErrServiceUnavailable, it unwraps to its cause, it is neither a rejected token
// nor a rate limit, and its message names both.
func TestServiceUnavailableError(t *testing.T) {
	cause := errors.New("provider down")
	err := error(&ServiceUnavailableError{RetryAfter: time.Second, Err: cause})

	assert.ErrorIs(t, err, ErrServiceUnavailable)
	assert.ErrorIs(t, err, cause)
	assert.NotErrorIs(t, err, mcpauth.ErrInvalidToken)
	assert.NotErrorIs(t, err, ErrTooManyRequests)
	assert.Equal(t, "service unavailable: provider down", err.Error())
	assert.Equal(t, "service unavailable", (&ServiceUnavailableError{}).Error())
}

// TestOAuthResourceServer_Verify_ServiceUnavailableOutsideTheMiddleware pins that
// verify fails closed when it is reached without the middleware that answers
// 503: the token is rejected as invalid rather than let through.
func TestOAuthResourceServer_Verify_ServiceUnavailableOutsideTheMiddleware(t *testing.T) {
	a := validOAuth()
	a.ValidateToken = func(_ context.Context, _ string) (*TokenInfo, error) {
		return nil, &ServiceUnavailableError{RetryAfter: time.Second}
	}

	info, err := a.verify(context.Background(), "any-token", nil)
	assert.Nil(t, info)
	assert.ErrorIs(t, err, mcpauth.ErrInvalidToken)
}

// TestMCP_OAuth_OAuthError verifies the one error passed through on purpose: an
// OAuth protocol error the validator reports is answered 400 with its message.
func TestMCP_OAuth_OAuthError(t *testing.T) {
	m := newTestMCP(t, oauthConfig(func(_ context.Context, _ string) (*TokenInfo, error) {
		return nil, fmt.Errorf("%w: invalid_request", mcpauth.ErrOAuth)
	}))

	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, bearer("any-token"))

	assert.Equal(t, http.StatusBadRequest, rw.Code)
	assert.Contains(t, rw.Body.String(), "invalid_request")
}

// TestMCP_OAuth_SubjectRequired pins that a token is rejected when its
// TokenInfo is missing or names no subject: a stateful session is bound to the
// subject, so a token without one could take over any session.
func TestMCP_OAuth_SubjectRequired(t *testing.T) {
	for name, info := range map[string]*TokenInfo{
		"nil token info": nil,
		"empty subject":  tokenFor(""),
	} {
		t.Run(name, func(t *testing.T) {
			m := newTestMCP(t, oauthConfig(func(_ context.Context, _ string) (*TokenInfo, error) {
				return info, nil
			}))

			rw := httptest.NewRecorder()
			m.handler.ServeHTTP(rw, bearer("any-token"))

			assert.Equal(t, http.StatusUnauthorized, rw.Code)
		})
	}
}

// TestMCP_OAuth_SessionBoundToSubject is the regression guard for session
// hijacking in stateful mode: TokenInfo.UserID was never set, so the SDK bound
// sessions to nobody and any valid token could drive a session whose identifier
// it learned. A session now answers only the subject that created it.
func TestMCP_OAuth_SessionBoundToSubject(t *testing.T) {
	cfg := oauthConfig(func(_ context.Context, token string) (*TokenInfo, error) {
		return tokenFor(token), nil
	})
	cfg.Stateful = true
	m := newTestMCP(t, cfg)

	srv := httptest.NewServer(m.handler)
	defer srv.Close()

	post := func(token, sessionID, body string) *http.Response {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+token)
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		return resp
	}

	initialized := post("alice", "", initializeBody)
	sessionID := initialized.Header.Get("Mcp-Session-Id")
	assert.Contains(t, readBody(t, initialized), `"protocolVersion"`)
	require.NotEmpty(t, sessionID)

	hijacked := post("mallory", sessionID, initializedBody)
	readBody(t, hijacked)
	assert.Equal(t, http.StatusForbidden, hijacked.StatusCode)

	owned := post("alice", sessionID, initializedBody)
	readBody(t, owned)
	assert.Equal(t, http.StatusAccepted, owned.StatusCode)
}

// TestMCP_OAuth_ComposeMode verifies that in compose mode the integration mounts
// the public Protected Resource Metadata endpoint but does NOT auto-gate the
// transport (the consumer composes BearerMiddleware itself), while the exported
// BearerMiddleware still enforces bearer tokens when applied.
func TestMCP_OAuth_ComposeMode(t *testing.T) {
	cfg := oauthConfig(func(_ context.Context, _ string) (*TokenInfo, error) {
		return tokenFor("user-1"), nil
	})
	cfg.OAuth.Compose = true
	cfg.Middleware = passthrough
	m := newTestMCP(t, cfg)

	// The Protected Resource Metadata endpoint is public.
	prmRW := httptest.NewRecorder()
	m.handler.ServeHTTP(prmRW, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil))
	assert.Equal(t, http.StatusOK, prmRW.Code)

	// The transport is NOT auto-gated: an originless request reaches it rather than
	// being rejected with 401 by a bearer middleware the integration did not apply.
	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, httptest.NewRequest(http.MethodPost, "/mcp", nil))
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

// TestOAuthResourceServer_BearerMiddleware_WithoutValidator pins that the
// exported middleware fails closed, rather than panicking, when it is built from
// an OAuthResourceServer without a ValidateToken.
func TestOAuthResourceServer_BearerMiddleware_WithoutValidator(t *testing.T) {
	a := validOAuth()
	a.ValidateToken = nil

	gated := a.BearerMiddleware()(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusOK)
	}))

	rw := httptest.NewRecorder()
	require.NotPanics(t, func() { gated.ServeHTTP(rw, bearer("any-token")) })
	assert.Equal(t, http.StatusUnauthorized, rw.Code)
}

// TestOAuthResourceServer_TokenInfo verifies what a tool handler sees of an
// accepted token.
func TestOAuthResourceServer_TokenInfo(t *testing.T) {
	expiration := time.Now().Add(time.Hour)
	a := validOAuth()
	a.ValidateToken = func(_ context.Context, _ string) (*TokenInfo, error) {
		return &TokenInfo{
			Subject:    "user-1",
			Scopes:     []string{"mcp:tools"},
			Expiration: expiration,
			Claims:     map[string]any{"tenant": "t-1"},
		}, nil
	}

	var got *mcpauth.TokenInfo
	gated := a.BearerMiddleware()(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		got = mcpauth.TokenInfoFromContext(req.Context())
	}))
	gated.ServeHTTP(httptest.NewRecorder(), bearer("any-token"))

	require.NotNil(t, got)
	assert.Equal(t, &mcpauth.TokenInfo{
		Scopes:     []string{"mcp:tools"},
		Expiration: expiration,
		UserID:     "user-1",
		Extra:      map[string]any{"claims": map[string]any{"tenant": "t-1"}},
	}, got)
}
