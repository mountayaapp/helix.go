package rest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
)

// countingBody is a request body that counts how many times it is read.
type countingBody struct {
	reader io.Reader
	reads  int
}

func (b *countingBody) Read(p []byte) (int, error) {
	b.reads++
	return b.reader.Read(p)
}

// requireAuthorization refuses a request without an Authorization header, the
// way an application's authentication would.
func requireAuthorization(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") == "" {
			NewResponseError[NoMetadata](req).SetStatus(http.StatusUnauthorized).Write(rw)
			return
		}

		next.ServeHTTP(rw, req)
	})
}

// recordStep returns a middleware appending name to steps before handing the
// request on.
func recordStep(steps *[]string, name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			*steps = append(*steps, name)
			next.ServeHTTP(rw, req)
		})
	}
}

// postThing sends POST /things with body as JSON through h.
func postThing(h http.Handler, body io.Reader, authorized bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/things", body)
	req.Header.Set("Content-Type", "application/json")
	if authorized {
		req.Header.Set("Authorization", "Bearer token")
	}

	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	return rw
}

func TestValidation_RefusesAnInvalidRequest(t *testing.T) {
	api, rec := newValidatedREST(t)

	var called bool
	api.POST("/things", func(rw http.ResponseWriter, req *http.Request) {
		called = true
	})

	req := httptest.NewRequest(http.MethodPost, "/things", strings.NewReader(`{"name":"ab"}`))
	req.Header.Set("Content-Type", "application/json")

	rw := httptest.NewRecorder()
	api.Handler().ServeHTTP(rw, req)

	assert.False(t, called)
	assert.Equal(t, http.StatusBadRequest, rw.Code)
	assert.Contains(t, rw.Body.String(), `"path":["request","body","name"]`)
	assert.Contains(t, rw.Body.String(), `"code":"VALIDATION_FAILED"`)

	// The refusal is this package's own envelope: there is no response to validate.
	assert.Equal(t, codes.Error, spanNamed(t, rec, "REST: OpenAPI / Request Validation").Status().Code)
	for _, span := range rec.Spans() {
		assert.NotEqual(t, "REST: OpenAPI / Response Validation", span.Name())
	}
}

// A body sent without a length is cut off at the limit while the validator reads
// it, which is a body too large rather than a malformed one.
func TestValidation_BodyOverTheLimitIs413(t *testing.T) {
	api, _ := newTestREST(t, Config{
		MaxRequestBodyBytes: 8,
		OpenAPI: ConfigOpenAPI{
			Enabled:      true,
			Descriptions: []string{"./testdata/openapi.yaml"},
		},
	})

	api.POST("/things", func(rw http.ResponseWriter, req *http.Request) {})

	req := httptest.NewRequest(http.MethodPost, "/things", strings.NewReader(`{"name":"a long enough name"}`))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = -1

	rw := httptest.NewRecorder()
	api.Handler().ServeHTTP(rw, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rw.Code)
	assert.JSONEq(t, `{"errors":[{"message":"Payload exceeds size limit","extensions":{"code":"PAYLOAD_TOO_LARGE"}}]}`, rw.Body.String())
}

// A request refused by a pre-validation middleware is answered before the
// description has judged it: its body, invalid as it is, is never read, and the
// caller learns nothing of what the route accepts.
func TestPreValidation_RefusesWithoutReadingTheBody(t *testing.T) {
	api, rec := newValidatedREST(t)

	var called bool
	api.POST("/things", func(rw http.ResponseWriter, req *http.Request) {
		called = true
	}, WithPreValidation(requireAuthorization))

	body := &countingBody{reader: strings.NewReader(`{"name":"ab"}`)}
	rw := postThing(api.Handler(), body, false)

	assert.Equal(t, http.StatusUnauthorized, rw.Code)
	assert.JSONEq(t, `{"errors":[{"message":"Authentication is required","extensions":{"code":"UNAUTHORIZED"}}]}`, rw.Body.String())
	assert.Zero(t, body.reads, "the body must not be read")
	assert.False(t, called)

	for _, span := range rec.Spans() {
		assert.NotEqual(t, "REST: OpenAPI / Request Validation", span.Name(), "the request must not be validated")
	}
}

// The pre-validation middlewares run in order, a group's before its route's, and
// the request is validated only once they have all let it through.
func TestPreValidation_RunsBeforeTheRequestValidation(t *testing.T) {
	api, _ := newValidatedREST(t)

	var steps []string
	api.Group("", WithPreValidation(recordStep(&steps, "group"))).
		POST("/things", func(rw http.ResponseWriter, req *http.Request) {
			steps = append(steps, "handler")
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(http.StatusCreated)
			_, _ = rw.Write([]byte(`{"data":{}}`))
		}, WithPreValidation(recordStep(&steps, "route"), requireAuthorization))

	t.Run("an invalid body is refused once they let it through", func(t *testing.T) {
		steps = nil
		rw := postThing(api.Handler(), strings.NewReader(`{"name":"ab"}`), true)

		assert.Equal(t, http.StatusBadRequest, rw.Code)
		assert.Contains(t, rw.Body.String(), `"path":["request","body","name"]`)
		assert.Equal(t, []string{"group", "route"}, steps)
	})

	t.Run("a valid body reaches the handler after them", func(t *testing.T) {
		steps = nil
		rw := postThing(api.Handler(), strings.NewReader(`{"name":"abc"}`), true)

		assert.Equal(t, http.StatusCreated, rw.Code)
		assert.Equal(t, []string{"group", "route", "handler"}, steps)
	})
}

// The route has matched by the time its pre-validation middlewares run, so they
// can read its parameters, and run under its budget.
func TestPreValidation_SeesTheRoute(t *testing.T) {
	api, _ := newTestREST(t, Config{})

	var id string
	var bounded bool
	api.GET("/things/:id", func(rw http.ResponseWriter, req *http.Request) {}, WithTimeout(time.Second), WithPreValidation(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			id, _ = ParamFromContext(req.Context(), "id")
			_, bounded = req.Context().Deadline()
			next.ServeHTTP(rw, req)
		})
	}))

	serve(api.Handler(), http.MethodGet, "/things/42", nil)

	assert.Equal(t, "42", id)
	assert.True(t, bounded, "the middleware must run under the route's budget")
}

// Without a description there is no validation to run ahead of: the middlewares
// run ahead of the handler.
func TestPreValidation_RunsWithoutADescription(t *testing.T) {
	api, _ := newTestREST(t, Config{})

	var steps []string
	api.POST("/things", func(rw http.ResponseWriter, req *http.Request) {
		steps = append(steps, "handler")
	}, WithPreValidation(recordStep(&steps, "first"), recordStep(&steps, "second")))

	serve(api.Handler(), http.MethodPost, "/things", nil)

	assert.Equal(t, []string{"first", "second", "handler"}, steps)
}

func TestPreValidation_RefusesANilMiddleware(t *testing.T) {
	api, _ := newTestREST(t, Config{})

	assert.PanicsWithValue(t, "rest: nil pre-validation middleware for POST /things", func() {
		api.POST("/things", func(rw http.ResponseWriter, req *http.Request) {}, WithPreValidation(nil))
	})
}

// A GET route answers HEAD with its handler, which may rely on its parameters
// having been validated: a HEAD is validated as the GET it mirrors.
func TestValidation_HEADIsValidatedAsItsGET(t *testing.T) {
	api, _ := newValidatedREST(t)

	var called int
	api.GET("/things/:id", func(rw http.ResponseWriter, req *http.Request) {
		called++
		NewResponseSuccess[NoMetadata, map[string]string](req).
			SetData(map[string]string{"id": "42"}).
			Write(rw)
	})

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			called = 0

			refused := serve(api.Handler(), method, "/things/a", nil)
			require.Equal(t, http.StatusBadRequest, refused.Code)
			assert.Contains(t, refused.Body.String(), `"path":["request","params","id"]`)
			assert.Zero(t, called)

			assert.Equal(t, http.StatusOK, serve(api.Handler(), method, "/things/42", nil).Code)
			assert.Equal(t, 1, called)
		})
	}
}
