package rest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/event"
	"github.com/mountaya/helix.go/integration"
	"github.com/mountaya/helix.go/service"
	"github.com/mountaya/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// newTestREST builds a REST API on a Service that records its telemetry. The
// Service is never started: the API is driven through its Handler, which is the
// whole chain the server would serve.
func newTestREST(t *testing.T, cfg Config) (*rest, *servicetest.Recorder) {
	t.Helper()

	svc, rec := servicetest.New(t)
	if cfg.Address == "" {
		cfg.Address = "127.0.0.1:0"
	}

	api, err := New(t.Context(), svc, cfg)
	require.NoError(t, err)

	return api.(*rest), rec
}

// serve sends one request through h and returns what it answered.
func serve(h http.Handler, method, target string, body io.Reader) *httptest.ResponseRecorder {
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, httptest.NewRequest(method, target, body))

	return rw
}

// spanNamed returns the one recorded span called name.
func spanNamed(t *testing.T, rec *servicetest.Recorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()

	var found []sdktrace.ReadOnlySpan
	for _, span := range rec.Spans() {
		if span.Name() == name {
			found = append(found, span)
		}
	}

	require.Len(t, found, 1, "spans named %q", name)
	return found[0]
}

// attributesOf returns the attributes of span keyed by name.
func attributesOf(span sdktrace.ReadOnlySpan) map[string]attribute.Value {
	out := make(map[string]attribute.Value, len(span.Attributes()))
	for _, attr := range span.Attributes() {
		out[string(attr.Key)] = attr.Value
	}

	return out
}

func TestNew_RefusesANilService(t *testing.T) {
	api, err := New(t.Context(), nil, Config{})

	assert.Nil(t, api)
	assert.Equal(t, errorstack.NewValidation(errorstack.Entry{
		Message: "Must be set",
		Path:    []any{"service"},
	}), err)
}

// A server told to use TLS without a certificate would otherwise listen in
// plaintext, or not at all, after the process reported itself healthy.
func TestNew_RefusesTLSWithoutACertificate(t *testing.T) {
	svc, _ := servicetest.New(t)

	api, err := New(t.Context(), svc, Config{
		Address: "127.0.0.1:0",
		TLS:     integration.ConfigTLS{Enabled: true},
	})

	assert.Nil(t, api)
	assert.Equal(t, errorstack.NewValidation(errorstack.Entry{
		Message: "Must be set; a server requires cert_pem and key_pem",
		Path:    []any{"config", "tls"},
	}), err)
}

func TestNew_AcceptsTLSWithACertificate(t *testing.T) {
	svc, _ := servicetest.New(t)
	certPEM, keyPEM := selfSignedCertificate(t)

	_, err := New(t.Context(), svc, Config{
		Address: "127.0.0.1:0",
		TLS: integration.ConfigTLS{
			Enabled: true,
			CertPEM: certPEM,
			KeyPEM:  keyPEM,
		},
	})

	assert.NoError(t, err)
}

func TestRouter_Liveness_ReturnsOK(t *testing.T) {
	api, _ := newTestREST(t, Config{})

	rw := serve(api.Handler(), http.MethodGet, "/health", nil)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"data":null}`, rw.Body.String())
}

func TestRouter_Readiness_CustomReady(t *testing.T) {
	api, _ := newTestREST(t, Config{
		Readiness: func(req *http.Request) int {
			return http.StatusOK
		},
	})

	rw := serve(api.Handler(), http.MethodGet, "/ready", nil)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"data":null}`, rw.Body.String())
}

func TestRouter_Readiness_WithCustomReadiness(t *testing.T) {
	api, _ := newTestREST(t, Config{
		Readiness: func(req *http.Request) int {
			return http.StatusServiceUnavailable
		},
	})

	rw := serve(api.Handler(), http.MethodGet, "/ready", nil)

	assert.Equal(t, http.StatusServiceUnavailable, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"errors":[{"message":"Service is temporarily unavailable","extensions":{"code":"SERVICE_UNAVAILABLE"}}]}`, rw.Body.String())
}

// unhealthy is a Dependency that always reports the reason a real driver error
// would: an internal host name, which must never reach a probe's body.
type unhealthy struct{}

func (unhealthy) CheckHealth(context.Context) error {
	return integration.Unhealthy(io.ErrUnexpectedEOF)
}

func (unhealthy) Close(context.Context) error { return nil }

// The readiness probe is answered on the same listener as the API, to whoever
// asks: it says that the service is not ready, and nothing about why.
func TestRouter_Readiness_UnhealthyDependencyExposesNoDetail(t *testing.T) {
	svc, _ := servicetest.New(t)
	require.NoError(t, service.Attach(svc, "primary", unhealthy{}))

	api, err := New(t.Context(), svc, Config{Address: "127.0.0.1:0"})
	require.NoError(t, err)

	rw := serve(api.Handler(), http.MethodGet, "/ready", nil)

	assert.Equal(t, http.StatusServiceUnavailable, rw.Code)
	assert.JSONEq(t, `{"errors":[{"message":"Service is temporarily unavailable","extensions":{"code":"SERVICE_UNAVAILABLE"}}]}`, rw.Body.String())
}

func TestRouter_UnknownRoute_ReturnsNotFound(t *testing.T) {
	routes := []string{
		"/",
		"/unknown",
		"/api/test",
	}

	api, _ := newTestREST(t, Config{})

	for _, route := range routes {
		t.Run("GET "+route, func(t *testing.T) {
			rw := serve(api.Handler(), http.MethodGet, route, nil)

			assert.Equal(t, http.StatusNotFound, rw.Code)
			assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
			assert.JSONEq(t, `{"errors":[{"message":"Resource does not exist","extensions":{"code":"NOT_FOUND"}}]}`, rw.Body.String())
		})
	}
}

func TestRouter_NonAllowedMethod_ReturnsMethodNotAllowed(t *testing.T) {
	methods := []string{
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
	}

	api, _ := newTestREST(t, Config{})

	for _, method := range methods {
		t.Run(method+" /health", func(t *testing.T) {
			rw := serve(api.Handler(), method, "/health", nil)

			assert.Equal(t, http.StatusMethodNotAllowed, rw.Code)
			assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
			assert.JSONEq(t, `{"errors":[{"message":"Method is not allowed for this resource","extensions":{"code":"METHOD_NOT_ALLOWED"}}]}`, rw.Body.String())
		})
	}
}

// The probes answer whatever the application's middleware would have done with
// them: a Kubernetes probe carries no credentials.
func TestRouter_ProbesBypassMiddleware(t *testing.T) {
	api, _ := newTestREST(t, Config{
		Middleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
				rw.WriteHeader(http.StatusUnauthorized)
			})
		},
	})

	assert.Equal(t, http.StatusOK, serve(api.Handler(), http.MethodGet, "/health", nil).Code)
	assert.Equal(t, http.StatusOK, serve(api.Handler(), http.MethodGet, "/ready", nil).Code)
	assert.Equal(t, http.StatusUnauthorized, serve(api.Handler(), http.MethodGet, "/thing", nil).Code)
}

func TestRouter_EveryMethodRegisters(t *testing.T) {
	testcases := []struct {
		method   string
		register func(r Router, path string, handler http.HandlerFunc)
	}{
		{http.MethodGet, func(r Router, p string, h http.HandlerFunc) { r.GET(p, h) }},
		{http.MethodHead, func(r Router, p string, h http.HandlerFunc) { r.HEAD(p, h) }},
		{http.MethodPost, func(r Router, p string, h http.HandlerFunc) { r.POST(p, h) }},
		{http.MethodPut, func(r Router, p string, h http.HandlerFunc) { r.PUT(p, h) }},
		{http.MethodPatch, func(r Router, p string, h http.HandlerFunc) { r.PATCH(p, h) }},
		{http.MethodDelete, func(r Router, p string, h http.HandlerFunc) { r.DELETE(p, h) }},
		{http.MethodOptions, func(r Router, p string, h http.HandlerFunc) { r.OPTIONS(p, h) }},
	}

	for _, tc := range testcases {
		t.Run(tc.method, func(t *testing.T) {
			api, _ := newTestREST(t, Config{})

			var method string
			tc.register(api, "/verb", func(rw http.ResponseWriter, req *http.Request) {
				method = req.Method
				rw.WriteHeader(http.StatusAccepted)
			})

			rw := serve(api.Handler(), tc.method, "/verb", nil)

			assert.Equal(t, http.StatusAccepted, rw.Code)
			assert.Equal(t, tc.method, method)
		})
	}
}

func TestRouter_HandleTakesAnHTTPHandler(t *testing.T) {
	api, _ := newTestREST(t, Config{})

	api.Handle(http.MethodPost, "/handled", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusCreated)
	}))

	assert.Equal(t, http.StatusCreated, serve(api.Handler(), http.MethodPost, "/handled", nil).Code)
	assert.Equal(t, http.StatusMethodNotAllowed, serve(api.Handler(), http.MethodGet, "/handled", nil).Code)
}

func TestRouter_HandleRefusesANilHandler(t *testing.T) {
	api, _ := newTestREST(t, Config{})

	assert.Panics(t, func() { api.Handle(http.MethodGet, "/nil", nil) })
	assert.Panics(t, func() { api.GET("/nil", nil) })
}

func TestRouter_GroupPrefixesAndNests(t *testing.T) {
	api, rec := newTestREST(t, Config{})

	v1 := api.Group("/v1")
	v1.Group("/things").GET("/:id", func(rw http.ResponseWriter, req *http.Request) {
		id, _ := ParamFromContext(req.Context(), "id")
		_, _ = rw.Write([]byte(id))
	})

	rw := serve(api.Handler(), http.MethodGet, "/v1/things/42", nil)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "42", rw.Body.String())
	spanNamed(t, rec, "REST: GET /v1/things/:id")
}

func TestRouter_GroupOptionsApplyBeforeTheRoutesOwn(t *testing.T) {
	api, _ := newTestREST(t, Config{RequestTimeout: time.Minute})

	stream := api.Group("/stream", WithoutTimeout())

	var unbounded, bounded bool
	stream.GET("/unbounded", func(_ http.ResponseWriter, req *http.Request) {
		_, ok := req.Context().Deadline()
		unbounded = !ok
	})
	stream.GET("/bounded", func(_ http.ResponseWriter, req *http.Request) {
		_, bounded = req.Context().Deadline()
	}, WithTimeout(time.Second))

	serve(api.Handler(), http.MethodGet, "/stream/unbounded", nil)
	serve(api.Handler(), http.MethodGet, "/stream/bounded", nil)

	assert.True(t, unbounded, "the group's WithoutTimeout must waive Config.RequestTimeout")
	assert.True(t, bounded, "the route's own WithTimeout must win over the group's")
}

// A response the application's middleware writes itself — authentication
// refusing a request — used to run outside the span, and was neither traced nor
// measured.
func TestTelemetry_MiddlewareResponseIsTraced(t *testing.T) {
	api, rec := newTestREST(t, Config{
		Middleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
				NewResponseError[NoMetadata](req).SetStatus(http.StatusUnauthorized).Write(rw)
			})
		},
	})

	api.GET("/thing", func(rw http.ResponseWriter, req *http.Request) {})

	rw := serve(api.Handler(), http.MethodGet, "/thing", nil)
	require.Equal(t, http.StatusUnauthorized, rw.Code)

	// The router never ran, so the route was never known: the span is named after
	// the method alone.
	span := spanNamed(t, rec, "REST: GET")
	assert.Equal(t, int64(http.StatusUnauthorized), attributesOf(span)["rest.status_code"].AsInt64())
	assert.Equal(t, codes.Unset, span.Status().Code, "a 4xx is the client's error, not the server's")
}

// The Event the application's middleware puts in the context is stamped on the
// entry span, even though that span was opened before the middleware ran.
func TestTelemetry_MiddlewareEventIsStamped(t *testing.T) {
	api, rec := newTestREST(t, Config{
		Middleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
				ctx := event.NewContext(req.Context(), event.Event{Name: "thing.read", UserID: "usr_1"})
				next.ServeHTTP(rw, req.WithContext(ctx))
			})
		},
	})

	api.GET("/thing", func(rw http.ResponseWriter, req *http.Request) {})
	serve(api.Handler(), http.MethodGet, "/thing", nil)

	attrs := attributesOf(spanNamed(t, rec, "REST: GET /thing"))
	assert.Equal(t, "thing.read", attrs["event.name"].AsString())
}

// A panicking handler used to be recovered by the router's own adapter, which
// turned it into an error nothing read: the client got an empty 200, and the
// span said 200 too. Then it reached net/http, which dropped the connection with
// no answer at all. It is answered with the 500 error envelope now, and the span
// says 500 and carries the panic.
func TestTelemetry_PanickingHandlerIsA500(t *testing.T) {
	api, rec := newTestREST(t, Config{})

	api.GET("/panic", func(rw http.ResponseWriter, req *http.Request) {
		panic("boom")
	})

	var rw *httptest.ResponseRecorder
	assert.NotPanics(t, func() {
		rw = serve(api.Handler(), http.MethodGet, "/panic", nil)
	}, "a panic before anything was written is answered, never handed to net/http")

	assert.Equal(t, http.StatusInternalServerError, rw.Code)
	assert.JSONEq(t, `{"errors":[{"message":"Internal server error","extensions":{"code":"INTERNAL_ERROR"}}]}`, rw.Body.String())

	span := spanNamed(t, rec, "REST: GET /panic")
	assert.Equal(t, int64(http.StatusInternalServerError), attributesOf(span)["rest.status_code"].AsInt64())
	assert.Equal(t, codes.Error, span.Status().Code)

	var recorded bool
	for _, event := range span.Events() {
		for _, attr := range event.Attributes {
			if attr.Key == "exception.message" && strings.Contains(attr.Value.AsString(), "boom") {
				recorded = true
			}
		}
	}
	assert.True(t, recorded, "the panic is recorded on the entry span as an exception")
}

// Path parameters routinely carry identifiers a trace backend should not keep, so
// their values are recorded only when asked for.
func TestTelemetry_RouteParams(t *testing.T) {
	testcases := []struct {
		name     string
		record   bool
		recorded bool
	}{
		{name: "not recorded by default", record: false, recorded: false},
		{name: "recorded when asked for", record: true, recorded: true},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			api, rec := newTestREST(t, Config{RecordRouteParams: tc.record})
			api.GET("/users/:email", func(rw http.ResponseWriter, req *http.Request) {})

			serve(api.Handler(), http.MethodGet, "/users/someone@example.com", nil)

			value, ok := attributesOf(spanNamed(t, rec, "REST: GET /users/:email"))["rest.route.param.email"]
			assert.Equal(t, tc.recorded, ok)
			if tc.recorded {
				assert.Equal(t, "someone@example.com", value.AsString())
			}
		})
	}
}

// A route whose path carries a secret records its template as the path, and none
// of its parameters, whatever the method and wherever it is registered.
func TestTelemetry_RedactedPath(t *testing.T) {
	testcases := []struct {
		name   string
		method string
		target string
		route  string
		status int
	}{
		{name: "the route's own method", method: http.MethodGet, target: "/things/secret-value", route: "/things/:token", status: http.StatusOK},
		{name: "a method the path does not serve", method: http.MethodPost, target: "/things/secret-value", route: "/things/:token", status: http.StatusMethodNotAllowed},
		{name: "a route registered in a group", method: http.MethodGet, target: "/api/things/secret-value", route: "/api/things/:token", status: http.StatusOK},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			api, rec := newTestREST(t, Config{RecordRouteParams: true})
			api.GET("/things/:token", func(rw http.ResponseWriter, req *http.Request) {}, WithRedactedPath())
			api.Group("/api/").GET("/things/:token", func(rw http.ResponseWriter, req *http.Request) {}, WithRedactedPath())

			rw := serve(api.Handler(), tc.method, tc.target, nil)
			require.Equal(t, tc.status, rw.Code)

			span := spanNamed(t, rec, "REST: "+tc.method+" "+tc.route)
			attrs := attributesOf(span)
			assert.Equal(t, tc.route, attrs["rest.path"].AsString())
			assert.NotContains(t, attrs, "rest.route.param.token")
			for key, value := range attrs {
				assert.NotContains(t, value.String(), "secret-value", "attribute %s", key)
			}
		})
	}
}

// Redacting one route leaves every other route's path and parameters as they are.
func TestTelemetry_RedactedPathIsPerRoute(t *testing.T) {
	api, rec := newTestREST(t, Config{RecordRouteParams: true})
	api.GET("/things/:token", func(rw http.ResponseWriter, req *http.Request) {}, WithRedactedPath())
	api.GET("/others/:id", func(rw http.ResponseWriter, req *http.Request) {})

	serve(api.Handler(), http.MethodGet, "/others/42", nil)

	attrs := attributesOf(spanNamed(t, rec, "REST: GET /others/:id"))
	assert.Equal(t, "/others/42", attrs["rest.path"].AsString())
	assert.Equal(t, "42", attrs["rest.route.param.id"].AsString())
}

// A public server starts a trace of its own, linked to the caller's, rather than
// letting a client decide which trace its spans join and whether they are
// sampled.
func TestTelemetry_PublicEndpointStartsANewTrace(t *testing.T) {
	const traceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"

	testcases := []struct {
		name      string
		public    bool
		continued bool
	}{
		{name: "internal server continues the caller's trace", public: false, continued: true},
		{name: "public server starts a new one", public: true, continued: false},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			api, rec := newTestREST(t, Config{PublicEndpoint: tc.public})
			api.GET("/thing", func(rw http.ResponseWriter, req *http.Request) {})

			req := httptest.NewRequest(http.MethodGet, "/thing", nil)
			req.Header.Set("traceparent", traceparent)
			api.Handler().ServeHTTP(httptest.NewRecorder(), req)

			span := spanNamed(t, rec, "REST: GET /thing")
			caller, err := oteltrace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
			require.NoError(t, err)

			assert.Equal(t, tc.continued, span.SpanContext().TraceID() == caller)
			if !tc.continued {
				require.Len(t, span.Links(), 1)
				assert.Equal(t, caller, span.Links()[0].SpanContext.TraceID())
			}
		})
	}
}

func TestMaxRequestBodyBytes(t *testing.T) {
	api, _ := newTestREST(t, Config{MaxRequestBodyBytes: 8})

	var readErr error
	api.POST("/upload", func(rw http.ResponseWriter, req *http.Request) {
		_, readErr = io.ReadAll(req.Body)
		if _, ok := errors.AsType[*http.MaxBytesError](readErr); ok {
			NewResponseError[NoMetadata](req).SetStatus(http.StatusRequestEntityTooLarge).Write(rw)
		}
	})

	t.Run("a declared length over the limit is refused unread", func(t *testing.T) {
		readErr = nil
		rw := serve(api.Handler(), http.MethodPost, "/upload", strings.NewReader("123456789"))

		assert.Equal(t, http.StatusRequestEntityTooLarge, rw.Code)
		assert.JSONEq(t, `{"errors":[{"message":"Payload exceeds size limit","extensions":{"code":"PAYLOAD_TOO_LARGE"}}]}`, rw.Body.String())
		assert.NoError(t, readErr, "the handler must not have run")
	})

	t.Run("an undeclared length is cut off at the limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("123456789"))
		req.ContentLength = -1

		rw := httptest.NewRecorder()
		api.Handler().ServeHTTP(rw, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, rw.Code)
		assert.Error(t, readErr)
	})

	t.Run("a body within the limit is read whole", func(t *testing.T) {
		rw := serve(api.Handler(), http.MethodPost, "/upload", strings.NewReader("12345678"))

		assert.Equal(t, http.StatusOK, rw.Code)
		assert.NoError(t, readErr)
	})
}

func TestCrossOriginProtection(t *testing.T) {
	api, _ := newTestREST(t, Config{CrossOriginProtection: http.NewCrossOriginProtection()})
	api.POST("/form", func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusNoContent)
	})

	crossSite := httptest.NewRequest(http.MethodPost, "/form", nil)
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	rw := httptest.NewRecorder()
	api.Handler().ServeHTTP(rw, crossSite)

	assert.Equal(t, http.StatusForbidden, rw.Code)
	assert.JSONEq(t, `{"errors":[{"message":"Access is forbidden","extensions":{"code":"FORBIDDEN"}}]}`, rw.Body.String())

	sameOrigin := httptest.NewRequest(http.MethodPost, "/form", nil)
	sameOrigin.Header.Set("Sec-Fetch-Site", "same-origin")
	rw = httptest.NewRecorder()
	api.Handler().ServeHTTP(rw, sameOrigin)

	assert.Equal(t, http.StatusNoContent, rw.Code)
}
