package rest

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
)

// thingResponses returns the responses the test description declares for
// GET /things/{id}: a 200 whose application/json body has a schema.
func thingResponses(t *testing.T) *openapi3.Responses {
	t.Helper()

	doc, err := LoadOpenAPIDescriptions(t.Context(), "./testdata/openapi.yaml")
	require.NoError(t, err)

	return doc.Paths.Value("/things/{id}").Get.Responses
}

func TestResponseWriter_Unwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	_, rw := newResponseWriter(rec, nil)

	// Unwrap must expose the wrapped writer so http.ResponseController can reach it.
	assert.Same(t, rec, rw.Unwrap())
}

func TestResponseWriter_KeepsOnlyADeclaredBody(t *testing.T) {
	testcases := []struct {
		name        string
		status      int
		contentType string
		kept        bool
	}{
		{name: "declared media type", status: http.StatusOK, contentType: "application/json", kept: true},
		{name: "declared media type with parameters", status: http.StatusOK, contentType: "application/json; charset=utf-8", kept: true},
		{name: "undeclared media type", status: http.StatusOK, contentType: "application/octet-stream", kept: false},
		{name: "undeclared status", status: http.StatusTeapot, contentType: "application/json", kept: false},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			w, rw := newResponseWriter(rec, thingResponses(t))
			w.Header().Set("Content-Type", tc.contentType)
			w.WriteHeader(tc.status)

			payload := `{"data":{"id":"42"}}`
			n, err := w.Write([]byte(payload))

			require.NoError(t, err)
			assert.Equal(t, len(payload), n)
			assert.False(t, rw.skipped)

			// Whatever is kept, the client gets the body as it was written.
			assert.Equal(t, payload, rec.Body.String())
			if tc.kept {
				require.NotNil(t, rw.body)
				assert.Equal(t, payload, rw.body.String())
			} else {
				assert.Nil(t, rw.body)
			}
		})
	}
}

func TestResponseWriter_EventStreamIsSkipped(t *testing.T) {
	rec := httptest.NewRecorder()
	w, rw := newResponseWriter(rec, thingResponses(t))
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")

	event := "data: hello\n\n"
	_, err := w.Write([]byte(event))

	require.NoError(t, err)
	assert.True(t, rw.skipped)
	assert.Nil(t, rw.body)
	assert.Equal(t, event, rec.Body.String())
}

// net/http keeps the first status a handler writes and ignores the rest, so that
// is the one the response is validated with.
func TestResponseWriter_KeepsTheFirstStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	w, rw := newResponseWriter(rec, nil)

	w.WriteHeader(http.StatusEarlyHints)
	w.WriteHeader(http.StatusCreated)
	w.WriteHeader(http.StatusInternalServerError)

	assert.Equal(t, http.StatusCreated, rw.status)
}

func TestResponseWriter_WriteImpliesOK(t *testing.T) {
	_, rw := newResponseWriter(httptest.NewRecorder(), nil)

	_, err := rw.Write([]byte("{}"))

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rw.status)
}

// failingWriter is a ResponseWriter whose connection has gone away.
type failingWriter struct {
	*httptest.ResponseRecorder
}

var errConnection = errors.New("connection reset by peer")

func (failingWriter) Write([]byte) (int, error) { return 0, errConnection }

// A handler told its write succeeded keeps writing to a connection that is gone.
func TestResponseWriter_WritePropagatesTheConnectionsError(t *testing.T) {
	w, rw := newResponseWriter(failingWriter{httptest.NewRecorder()}, thingResponses(t))
	w.Header().Set("Content-Type", "application/json")

	n, err := w.Write([]byte(`{"data":{}}`))

	assert.ErrorIs(t, err, errConnection)
	assert.Zero(t, n)
	assert.Zero(t, rw.body.Len(), "nothing that did not reach the client is validated")
}

func TestResponseWriter_GivesUpPastTheCap(t *testing.T) {
	rec := httptest.NewRecorder()
	w, rw := newResponseWriter(rec, thingResponses(t))
	w.Header().Set("Content-Type", "application/json")

	chunk := []byte(strings.Repeat("a", maxValidatedResponseBytes/2+1))
	_, _ = w.Write(chunk)
	require.False(t, rw.skipped)

	_, err := w.Write(chunk)

	require.NoError(t, err)
	assert.True(t, rw.skipped)
	assert.Nil(t, rw.body, "the copy must be released")
	assert.Equal(t, 2*len(chunk), rec.Body.Len(), "the client still gets the whole body")
}

// A handler streaming server-sent events asks the writer whether it can flush. The
// wrapper used to say no whenever OpenAPI was enabled, which broke every stream.
func TestResponseWriter_Flush(t *testing.T) {
	rec := httptest.NewRecorder()
	w, rw := newResponseWriter(rec, thingResponses(t))
	w.Header().Set("Content-Type", "application/json")

	flusher, ok := w.(http.Flusher)
	require.True(t, ok, "the wrapper must be an http.Flusher")

	_, _ = w.Write([]byte("{"))
	flusher.Flush()

	assert.True(t, rec.Flushed)
	assert.True(t, rw.skipped, "a flushed response is a stream, and is not validated")

	require.NoError(t, http.NewResponseController(w).Flush())
}

// A response that declared its length and wrote all of it is complete: flushing it
// sends it at once, and it is still kept and validated. One flushed before its
// declared length is written is a stream, and is not.
func TestResponseWriter_FlushOfACompleteBody(t *testing.T) {
	body := []byte(`{"data":{"id":"42"}}`)

	t.Run("complete", func(t *testing.T) {
		rec := httptest.NewRecorder()
		w, rw := newResponseWriter(rec, thingResponses(t))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))

		_, _ = w.Write(body)
		require.NoError(t, http.NewResponseController(w).Flush())

		assert.True(t, rec.Flushed)
		assert.False(t, rw.skipped, "a complete body flushed is still validated")
		assert.Equal(t, body, rw.body.Bytes())
	})

	t.Run("partial", func(t *testing.T) {
		rec := httptest.NewRecorder()
		w, rw := newResponseWriter(rec, thingResponses(t))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))

		_, _ = w.Write(body[:5])
		require.NoError(t, http.NewResponseController(w).Flush())

		assert.True(t, rw.skipped, "a body flushed before its declared length is written is a stream")
	})
}

func TestResponseWriter_ReadFrom(t *testing.T) {
	rec := httptest.NewRecorder()
	w, rw := newResponseWriter(rec, thingResponses(t))
	w.Header().Set("Content-Type", "application/json")

	readerFrom, ok := w.(io.ReaderFrom)
	require.True(t, ok, "the wrapper must keep net/http's zero-copy path")

	n, err := readerFrom.ReadFrom(strings.NewReader(`{"data":{"id":"42"}}`))

	require.NoError(t, err)
	assert.Equal(t, int64(len(`{"data":{"id":"42"}}`)), n)
	assert.Equal(t, `{"data":{"id":"42"}}`, rec.Body.String())
	assert.Equal(t, `{"data":{"id":"42"}}`, rw.body.String())
}

// hijackableRecorder is a ResponseRecorder over a connection that can be taken
// over, as an HTTP/1.1 one can.
type hijackableRecorder struct {
	*httptest.ResponseRecorder
}

func (hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, nil
}

func TestResponseWriter_HijackIsConditional(t *testing.T) {
	w, _ := newResponseWriter(httptest.NewRecorder(), nil)
	_, ok := w.(http.Hijacker)
	assert.False(t, ok, "a writer over one that cannot be hijacked must not claim it can")

	w, rw := newResponseWriter(hijackableRecorder{httptest.NewRecorder()}, nil)
	hijacker, ok := w.(http.Hijacker)
	require.True(t, ok, "a writer over one that can be hijacked must say so")

	_, _, err := hijacker.Hijack()
	require.NoError(t, err)
	assert.True(t, rw.skipped, "a hijacked connection is not an HTTP response")
}

// A handler that sends its answer whole, its length declared, then flushes it, as
// one does to have its client receive the answer before work it does on its way
// out, keeps its answer validated: an invalid one is still recorded, a valid one
// still passes.
func TestValidation_AFlushedCompleteAnswerIsStillValidated(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		code codes.Code
	}{
		"an invalid answer": {body: `{"data":{"id":42}}`, code: codes.Error},
		"a valid answer":    {body: `{"data":{"id":"42"}}`, code: codes.Unset},
	} {
		t.Run(name, func(t *testing.T) {
			api, rec := newValidatedREST(t)

			api.GET("/things/:id", func(rw http.ResponseWriter, req *http.Request) {
				rw.Header().Set("Content-Type", "application/json")
				rw.Header().Set("Content-Length", strconv.Itoa(len(tc.body)))
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write([]byte(tc.body))
				_ = http.NewResponseController(rw).Flush()
			})

			rw := serve(api.Handler(), http.MethodGet, "/things/42", nil)

			assert.Equal(t, http.StatusOK, rw.Code)
			assert.True(t, rw.Flushed)
			assert.JSONEq(t, tc.body, rw.Body.String())
			assert.Equal(t, tc.code, spanNamed(t, rec, "REST: OpenAPI / Response Validation").Status().Code)
		})
	}
}

// newValidatedREST builds an API validated against the test description.
func newValidatedREST(t *testing.T) (*rest, *servicetest.Recorder) {
	t.Helper()

	return newTestREST(t, Config{
		OpenAPI: ConfigOpenAPI{
			Enabled:      true,
			Descriptions: []string{"./testdata/openapi.yaml"},
		},
	})
}

func TestValidation_RecordsAnInvalidResponseAndStillSendsIt(t *testing.T) {
	api, rec := newValidatedREST(t)

	api.GET("/things/:id", func(rw http.ResponseWriter, req *http.Request) {
		NewResponseSuccess[NoMetadata, map[string]int](req).
			SetData(map[string]int{"id": 42}).
			Write(rw)
	})

	rw := serve(api.Handler(), http.MethodGet, "/things/42", nil)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.JSONEq(t, `{"data":{"id":42}}`, rw.Body.String())
	assert.Equal(t, codes.Error, spanNamed(t, rec, "REST: OpenAPI / Response Validation").Status().Code)
	assert.Equal(t, codes.Unset, spanNamed(t, rec, "REST: GET /things/:id").Status().Code)
}

// An answer with no payload writes its data as null, which an OpenAPI 3.1
// envelope declaring `data: {}` admits. It was recorded as failing validation on
// every such answer, which buried the failures that are real.
func TestValidation_AnAnswerWithNoPayloadIsValid(t *testing.T) {
	testcases := []struct {
		name   string
		handle http.HandlerFunc
		body   string
		code   codes.Code
	}{
		{
			name: "no payload, where the operation declares none",
			handle: func(rw http.ResponseWriter, req *http.Request) {
				NewResponseSuccess[NoMetadata, NoData](req).Write(rw)
			},
			body: `{"data":null}`,
			code: codes.Unset,
		},
		{
			name: "a payload, where the operation declares none",
			handle: func(rw http.ResponseWriter, req *http.Request) {
				NewResponseSuccess[NoMetadata, map[string]string](req).
					SetData(map[string]string{"id": "42"}).
					Write(rw)
			},
			body: `{"data":{"id":"42"}}`,
			code: codes.Error,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			api, rec := newTestREST(t, Config{
				OpenAPI: ConfigOpenAPI{
					Enabled:      true,
					Descriptions: []string{"./testdata/openapi.null.yaml"},
				},
			})

			api.DELETE("/things/:id", tc.handle)

			rw := serve(api.Handler(), http.MethodDelete, "/things/42", nil)

			assert.Equal(t, http.StatusOK, rw.Code)
			assert.JSONEq(t, tc.body, rw.Body.String())
			assert.Equal(t, tc.code, spanNamed(t, rec, "REST: OpenAPI / Response Validation").Status().Code)
		})
	}
}

// The SSE route is declared in the description, so its request is validated and
// its response wrapped: the stream must still flush.
func TestValidation_EventStreamStreams(t *testing.T) {
	api, rec := newValidatedREST(t)

	api.GET("/events", func(rw http.ResponseWriter, req *http.Request) {
		flusher, ok := rw.(http.Flusher)
		if !ok {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}

		rw.Header().Set("Content-Type", "text/event-stream")
		_, _ = rw.Write([]byte("data: one\n\n"))
		flusher.Flush()
		_, _ = rw.Write([]byte("data: two\n\n"))
		flusher.Flush()
	}, WithoutTimeout())

	rw := httptest.NewRecorder()
	api.Handler().ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/events", nil))

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.True(t, rw.Flushed)
	assert.Equal(t, "data: one\n\ndata: two\n\n", rw.Body.String())

	spanNamed(t, rec, "REST: OpenAPI / Request Validation")
	for _, span := range rec.Spans() {
		assert.NotEqual(t, "REST: OpenAPI / Response Validation", span.Name(), "a stream is not validated")
	}
}

// The probes are in no description. Matching them against one recorded a
// "failed to find route" error on every probe, every few seconds, forever.
func TestValidation_ProbesAreNeverValidated(t *testing.T) {
	api, rec := newValidatedREST(t)

	assert.Equal(t, http.StatusOK, serve(api.Handler(), http.MethodGet, "/health", nil).Code)
	assert.Equal(t, http.StatusOK, serve(api.Handler(), http.MethodGet, "/ready", nil).Code)

	require.Len(t, rec.Spans(), 2, "one entry span per probe, and no validation span")
	for _, span := range rec.Spans() {
		assert.Equal(t, codes.Unset, span.Status().Code, "span %q", span.Name())
		assert.Empty(t, span.Events(), "span %q", span.Name())
		assert.NotContains(t, attributesOf(span), "rest.openapi.validated")
	}
}

// A route the description does not declare is served unvalidated. That is not a
// failure of the request, and its span says it was not validated rather than
// recording an error.
func TestValidation_UndescribedRouteIsPassedThrough(t *testing.T) {
	api, rec := newValidatedREST(t)

	api.GET("/undescribed", func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusTeapot)
	})

	rw := serve(api.Handler(), http.MethodGet, "/undescribed", nil)
	assert.Equal(t, http.StatusTeapot, rw.Code)

	require.Len(t, rec.Spans(), 1, "no validation span")
	span := spanNamed(t, rec, "REST: GET /undescribed")
	assert.Equal(t, codes.Unset, span.Status().Code)
	assert.Empty(t, span.Events())
	assert.False(t, attributesOf(span)["rest.openapi.validated"].AsBool())
}

// What a pre-validation middleware answers is a response of the route like the
// handler's, and is validated as one: a status the operation declares passes, one
// it does not is recorded, and both reach the client.
func TestValidation_PreValidationResponseIsValidated(t *testing.T) {
	testcases := []struct {
		name   string
		status int
		code   codes.Code
	}{
		{name: "a declared status", status: http.StatusUnauthorized, code: codes.Unset},
		{name: "an undeclared status", status: http.StatusForbidden, code: codes.Error},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			api, rec := newValidatedREST(t)

			api.POST("/things", func(rw http.ResponseWriter, req *http.Request) {}, WithPreValidation(func(http.Handler) http.Handler {
				return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
					NewResponseError[NoMetadata](req).SetStatus(tc.status).Write(rw)
				})
			}))

			rw := postThing(api.Handler(), strings.NewReader(`{"name":"abc"}`), false)

			assert.Equal(t, tc.status, rw.Code)
			assert.Equal(t, tc.code, spanNamed(t, rec, "REST: OpenAPI / Response Validation").Status().Code)
		})
	}
}
