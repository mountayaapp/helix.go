package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/mountayaapp/helix.go/telemetry/log"
	"github.com/mountayaapp/helix.go/telemetry/trace"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// wellKnownProtectedResource is the well-known URI suffix under which OAuth 2.0
// Protected Resource Metadata is served, as defined by RFC 9728.
const wellKnownProtectedResource = "/.well-known/oauth-protected-resource"

// errInvalidResourceID is returned for a ResourceID the metadata URL cannot be
// derived from.
var errInvalidResourceID = errors.New("resource identifier must be an absolute http or https URL with a clean path and no fragment")

// errNoSubject is what a token whose TokenInfo carries no Subject is recorded as.
var errNoSubject = errors.New("token info has no subject")

// errNoValidator is what a token is recorded as when BearerMiddleware is applied
// to an OAuthResourceServer without a ValidateToken.
var errNoValidator = errors.New("no token validator is configured")

// ErrTooManyRequests is the error ValidateToken wraps when it could not verify a
// token because something it verifies tokens with is limiting how often it may
// be asked. The request is answered 429 Too Many Requests, never 401: the token
// may well be valid, and a 401 sends the client to authenticate again, which
// asks the same limit once more and tells the user to reconnect for nothing.
//
// Wrapping it is opt-in. An error that does not is answered 401 as before. To
// tell the client how long to wait, return a *TooManyRequestsError instead.
var ErrTooManyRequests = errors.New("too many requests")

// TooManyRequestsError is an error ValidateToken returns to have the request
// answered 429 Too Many Requests with a Retry-After header. It matches
// ErrTooManyRequests with errors.Is.
//
// Like every ValidateToken error, it never reaches the client: the answer's
// body is generic, and Err is recorded on the request's span.
type TooManyRequestsError struct {

	// RetryAfter is how long the client should wait before it tries again,
	// answered in the Retry-After header in whole seconds, rounded up. Zero or
	// less answers no Retry-After.
	RetryAfter time.Duration

	// Err is why the token could not be verified. It is optional.
	Err error
}

// Error returns the message of Err, prefixed with the sentinel's.
func (e *TooManyRequestsError) Error() string {
	if e.Err == nil {
		return ErrTooManyRequests.Error()
	}

	return ErrTooManyRequests.Error() + ": " + e.Err.Error()
}

// Unwrap returns Err.
func (e *TooManyRequestsError) Unwrap() error {
	return e.Err
}

// Is reports whether target is ErrTooManyRequests.
func (e *TooManyRequestsError) Is(target error) bool {
	return target == ErrTooManyRequests
}

// ErrServiceUnavailable is the error ValidateToken wraps when it could not verify
// a token because something it verifies tokens with failed or could not be
// reached: an identity provider that is down, a key set it cannot fetch, a
// backend that answered with a failure of its own. The request is answered 503
// Service Unavailable, never 401: the token may well be valid, and a 401 sends the
// client to authenticate again, which meets the same failure and tells the user to
// reconnect for an outage reconnecting cannot fix.
//
// Wrapping it is opt-in. An error that does not is answered 401 as before. To
// tell the client how long to wait, return a *ServiceUnavailableError instead.
var ErrServiceUnavailable = errors.New("service unavailable")

// ServiceUnavailableError is an error ValidateToken returns to have the request
// answered 503 Service Unavailable with a Retry-After header. It matches
// ErrServiceUnavailable with errors.Is.
//
// Like every ValidateToken error, it never reaches the client: the answer's
// body is generic, and Err is recorded on the request's span and logged.
type ServiceUnavailableError struct {

	// RetryAfter is how long the client should wait before it tries again,
	// answered in the Retry-After header in whole seconds, rounded up. Zero or
	// less answers no Retry-After.
	RetryAfter time.Duration

	// Err is why the token could not be verified. It is optional.
	Err error
}

// Error returns the message of Err, prefixed with the sentinel's.
func (e *ServiceUnavailableError) Error() string {
	if e.Err == nil {
		return ErrServiceUnavailable.Error()
	}

	return ErrServiceUnavailable.Error() + ": " + e.Err.Error()
}

// Unwrap returns Err.
func (e *ServiceUnavailableError) Unwrap() error {
	return e.Err
}

// Is reports whether target is ErrServiceUnavailable.
func (e *ServiceUnavailableError) Is(target error) bool {
	return target == ErrServiceUnavailable
}

// gateAnswerKey is the context key of the gateAnswer of a request going through
// BearerMiddleware.
type gateAnswerKey struct{}

// gateAnswer is how BearerMiddleware answers a request whose token could not be
// verified for a reason that is not the token, which the SDK's gate has no
// answer for: it answers 400, 401, 403 and 500 alone. Those reasons are the
// volume of requests (429) and a failure of what verifies the token (503).
//
// The gate is handed the gateAnswer as its writer. verify sets its status
// before the gate writes its own answer, which is then discarded, and
// BearerMiddleware answers that status itself once the gate returns. A request
// the gate lets through reaches the guarded handler with the writer it arrived
// with, never this one, so nothing it writes goes through a wrapper.
type gateAnswer struct {

	// rw is the writer the request arrived with.
	rw http.ResponseWriter

	// status is set by verify for a token it could not verify for a reason that
	// is not the token, 429 or 503, and is zero otherwise; retryAfter is the wait
	// to answer with it.
	status     int
	retryAfter time.Duration

	// discarded is the header the gate writes to once status is set.
	discarded http.Header
}

// answerLater sets the status the request is answered with in place of the
// gate's own answer, and the wait the *TooManyRequestsError or
// *ServiceUnavailableError err carries for that status, if it is one.
func (g *gateAnswer) answerLater(status int, err error) {
	g.status = status

	var limited *TooManyRequestsError
	var unavailable *ServiceUnavailableError
	switch {
	case status == http.StatusTooManyRequests && errors.As(err, &limited):
		g.retryAfter = limited.RetryAfter
	case status == http.StatusServiceUnavailable && errors.As(err, &unavailable):
		g.retryAfter = unavailable.RetryAfter
	}
}

// Header returns the header of the writer the request arrived with, or, once
// the request's status is set, one that is never sent.
func (g *gateAnswer) Header() http.Header {
	if g.status == 0 {
		return g.rw.Header()
	}

	if g.discarded == nil {
		g.discarded = http.Header{}
	}

	return g.discarded
}

// Write writes to the writer the request arrived with, or discards p once the
// request's status is set.
func (g *gateAnswer) Write(p []byte) (int, error) {
	if g.status != 0 {
		return len(p), nil
	}

	return g.rw.Write(p)
}

// WriteHeader writes to the writer the request arrived with, or does nothing
// once the request's status is set.
func (g *gateAnswer) WriteHeader(code int) {
	if g.status != 0 {
		return
	}

	g.rw.WriteHeader(code)
}

// writeRetryLater answers status, 429 or 503, with a generic body, the text of
// the sentinel the status stands for, and a Retry-After of retryAfter in whole
// seconds, rounded up, when it is positive.
func writeRetryLater(rw http.ResponseWriter, status int, retryAfter time.Duration) {
	if retryAfter > 0 {
		seconds := (retryAfter + time.Second - 1) / time.Second
		rw.Header().Set("Retry-After", strconv.FormatInt(int64(seconds), 10))
	}

	body := ErrServiceUnavailable.Error()
	if status == http.StatusTooManyRequests {
		body = ErrTooManyRequests.Error()
	}

	http.Error(rw, body, status)
}

// metadataLocation returns the absolute URL of the Protected Resource Metadata
// document, derived from ResourceID, and the path this server serves it at.
//
// RFC 9728 §3.1 inserts the well-known suffix between the host and the path of
// the resource identifier, rather than appending it: the metadata of
// "https://api.example.com/mcp" is at
// "https://api.example.com/.well-known/oauth-protected-resource/mcp". A resource
// identifier whose path is only "/" loses that terminating slash first.
func (a *OAuthResourceServer) metadataLocation() (string, string, error) {
	resource, err := url.Parse(a.ResourceID)
	if err != nil {
		return "", "", err
	}

	if (resource.Scheme != "https" && resource.Scheme != "http") || resource.Host == "" || resource.Fragment != "" {
		return "", "", errInvalidResourceID
	}

	metadata := *resource
	if resource.Path == "/" {
		metadata.Path = wellKnownProtectedResource
		metadata.RawPath = ""
	} else {
		metadata.Path = wellKnownProtectedResource + resource.Path
		if resource.RawPath != "" {
			metadata.RawPath = wellKnownProtectedResource + resource.RawPath
		}
	}

	// The path is registered on the mux, so it has to be one the mux accepts.
	if !isLiteralPath(metadata.Path) {
		return "", "", errInvalidResourceID
	}

	return metadata.String(), metadata.Path, nil
}

// BearerMiddleware returns the HTTP middleware enforcing bearer token
// authentication on the MCP transport. A request without a valid token is
// answered with 401 and a WWW-Authenticate challenge pointing clients at the
// Protected Resource Metadata; a valid token lacking one of Scopes, with 403.
// What ValidateToken returned is carried to tool handlers through
// CallToolRequest.Extra.TokenInfo.
//
// When Compose is false, the integration applies this middleware automatically. It
// is exported so that, when Compose is true, the consumer can place it inside its
// own Middleware chain.
//
// A token ValidateToken could not verify for the volume of requests (an error
// wrapping ErrTooManyRequests) is answered 429 Too Many Requests, with a
// Retry-After header when the error is a *TooManyRequestsError naming a wait.
// One it could not verify because what verifies it failed (an error wrapping
// ErrServiceUnavailable) is answered 503 Service Unavailable, with a Retry-After
// header when the error is a *ServiceUnavailableError naming a wait.
//
// The 400, 401 and 403 responses are written by the Go MCP SDK as plain text, not
// in the helix error envelope, and so are the 429 and the 503.
func (a *OAuthResourceServer) BearerMiddleware() func(next http.Handler) http.Handler {

	// A ResourceID the URL cannot be derived from only reaches here when the
	// middleware is built from a Config New never validated. The challenge then
	// goes without the metadata pointer rather than with a wrong one.
	metadataURL, _, err := a.metadataLocation()
	if err != nil {
		metadataURL = ""
	}

	gate := mcpauth.RequireBearerToken(a.verify, &mcpauth.RequireBearerTokenOptions{
		ResourceMetadataURL: metadataURL,
		Scopes:              a.Scopes,
	})

	return func(next http.Handler) http.Handler {

		// A request the gate lets through reaches next with the writer it arrived
		// with, not the gateAnswer the gate was handed.
		gated := gate(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(req.Context().Value(gateAnswerKey{}).(*gateAnswer).rw, req)
		}))

		return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			answer := &gateAnswer{rw: rw}
			gated.ServeHTTP(answer, req.WithContext(context.WithValue(req.Context(), gateAnswerKey{}, answer)))

			if answer.status != 0 {
				writeRetryLater(rw, answer.status, answer.retryAfter)
			}
		})
	}
}

// verify adapts ValidateToken into the SDK's TokenVerifier.
//
// The SDK answers an error it does not recognize as a token rejection with 500
// and the error's text as the body, which hands whatever the validator's
// dependencies said — a key set URL, an identity provider's own error — to an
// unauthenticated caller, without the challenge a client needs to recover. Every
// such error is therefore turned into auth.ErrInvalidToken, whose text is
// generic, and the real one goes to the request's span instead.
//
// An error wrapping ErrTooManyRequests or ErrServiceUnavailable, and not
// auth.ErrInvalidToken, which is a verdict on the token whatever else the error
// says, sets the status of the request's gateAnswer instead: what the SDK then
// writes is discarded, and BearerMiddleware answers 429 or 503. An error wrapping
// both is answered 429. A request reaching verify without a gateAnswer, which
// BearerMiddleware always sets, fails closed with 401.
func (a *OAuthResourceServer) verify(ctx context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
	if a.ValidateToken == nil {
		return nil, rejectToken(ctx, errNoValidator)
	}

	info, err := a.ValidateToken(ctx, token)
	switch {
	case errors.Is(err, mcpauth.ErrOAuth):
		recordException(ctx, err)
		return nil, err
	case errors.Is(err, ErrTooManyRequests) && !errors.Is(err, mcpauth.ErrInvalidToken):
		answer, ok := ctx.Value(gateAnswerKey{}).(*gateAnswer)
		if !ok {
			return nil, rejectToken(ctx, err)
		}

		// Routine, like a rejected token: the span records it, and nothing is logged.
		recordException(ctx, err)
		answer.answerLater(http.StatusTooManyRequests, err)

		return nil, mcpauth.ErrInvalidToken
	case errors.Is(err, ErrServiceUnavailable) && !errors.Is(err, mcpauth.ErrInvalidToken):

		// The validator could not do its job, so rejectToken records the error on
		// the span and logs it, as it does any other such failure.
		rejected := rejectToken(ctx, err)
		if answer, ok := ctx.Value(gateAnswerKey{}).(*gateAnswer); ok {
			answer.answerLater(http.StatusServiceUnavailable, err)
		}

		return nil, rejected
	case err != nil:
		return nil, rejectToken(ctx, err)
	case info == nil || info.Subject == "":
		return nil, rejectToken(ctx, errNoSubject)
	}

	// Scopes are the ones the token grants: the SDK checks every required scope
	// against them. UserID is what a stateful session is bound to.
	return &mcpauth.TokenInfo{
		Scopes:     info.Scopes,
		Expiration: info.Expiration,
		UserID:     info.Subject,
		Extra:      map[string]any{"claims": info.Claims},
	}, nil
}

// rejectToken records why a token was rejected and returns the generic error the
// client is answered with.
//
// A token the validator refused is routine — an expired token, a forged one —
// and goes to the span only. Any other failure means the validator could not do
// its job, which turns every request into a 401, or a 503 for an error wrapping
// ErrServiceUnavailable, and is worth an operator's attention, so it is logged
// too.
func rejectToken(ctx context.Context, err error) error {
	recordException(ctx, err)
	if !errors.Is(err, mcpauth.ErrInvalidToken) {
		log.Warn(ctx, "Failed to validate bearer token", log.Err(err))
	}

	return mcpauth.ErrInvalidToken
}

// metadataHandler returns the handler serving the OAuth 2.0 Protected Resource
// Metadata document (RFC 9728). It answers GET, and OPTIONS for a cross-origin
// preflight, itself.
func (a *OAuthResourceServer) metadataHandler() http.Handler {
	return mcpauth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:             a.ResourceID,
		AuthorizationServers: a.AuthorizationServers,
		ScopesSupported:      a.Scopes,
	})
}

// recordException records err as an exception on the span ctx carries, without
// touching the span's status: a refused token is a 4xx, which leaves it unset.
// The error goes through the sanitizer an application may have registered
// (trace.SetErrorSanitizer), as every error a span records does.
func recordException(ctx context.Context, err error) {
	if err = trace.SanitizeError(err); err != nil {
		oteltrace.SpanFromContext(ctx).RecordError(err)
	}
}
