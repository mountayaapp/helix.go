package rest

import (
	"encoding/json/v2"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// internalErrorBody is the envelope every 500 is answered with.
const internalErrorBody = `{"errors":[{"message":"Internal server error","extensions":{"code":"INTERNAL_ERROR"}}]}`

// stampRequestID is an EdgeMiddleware that puts a header on every answer before
// handing the request on, as an API stamping a request id does.
func stampRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Trace-Ref", "ref-42")
		next.ServeHTTP(rw, req)
	})
}

// A panic in Config.Middleware, which wraps the whole router, is answered like a
// panic in a handler, and the answer carries what EdgeMiddleware set.
func TestRecover_PanicInMiddlewareIsA500CarryingTheEdgeHeaders(t *testing.T) {
	api, _ := newTestREST(t, Config{
		EdgeMiddleware: stampRequestID,
		Middleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
				panic("middleware bug")
			})
		},
	})
	api.GET("/things", func(rw http.ResponseWriter, req *http.Request) {})

	rw := serve(api.Handler(), http.MethodGet, "/things", nil)

	assert.Equal(t, http.StatusInternalServerError, rw.Code)
	assert.JSONEq(t, internalErrorBody, rw.Body.String())
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.Equal(t, "ref-42", rw.Header().Get("X-Trace-Ref"))
}

// What a handler set for the answer it never wrote does not describe the 500: a
// cache directive or a validator on an error would let an intermediary keep it.
// What the edge set before the handler ran stays.
func TestRecover_HeadersTheHandlerSetAreDropped(t *testing.T) {
	api, _ := newTestREST(t, Config{EdgeMiddleware: stampRequestID})
	api.GET("/things", func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("Cache-Control", "public, max-age=86400")
		rw.Header().Set("ETag", `"v1"`)
		rw.Header().Set("X-Trace-Ref", "overwritten")
		panic(errors.New("handler bug"))
	})

	rw := serve(api.Handler(), http.MethodGet, "/things", nil)

	assert.Equal(t, http.StatusInternalServerError, rw.Code)
	assert.JSONEq(t, internalErrorBody, rw.Body.String())
	assert.Empty(t, rw.Header().Get("Cache-Control"))
	assert.Empty(t, rw.Header().Get("ETag"))
	assert.Equal(t, "ref-42", rw.Header().Get("X-Trace-Ref"))
}

// A panic once the response has started cannot be answered: a second status line
// is not something HTTP has. The response is aborted the way net/http aborts one
// quietly, and nothing is appended to what the client already has.
func TestRecover_PanicAfterTheResponseStartedAborts(t *testing.T) {
	testcases := []struct {
		name  string
		write func(rw http.ResponseWriter)
	}{
		{
			name: "after a flush",
			write: func(rw http.ResponseWriter) {
				rw.Header().Set("Content-Type", "text/event-stream")
				rw.(http.Flusher).Flush()
			},
		},
		{
			name: "after a partial body",
			write: func(rw http.ResponseWriter) {
				rw.Write([]byte(`{"data":`))
			},
		},
		{
			name: "after a final status",
			write: func(rw http.ResponseWriter) {
				rw.WriteHeader(http.StatusAccepted)
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			api, _ := newTestREST(t, Config{})
			api.GET("/things", func(rw http.ResponseWriter, req *http.Request) {
				tc.write(rw)
				panic("late bug")
			})

			rw := httptest.NewRecorder()
			assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
				api.Handler().ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/things", nil))
			})
			assert.NotContains(t, rw.Body.String(), "INTERNAL_ERROR")
		})
	}
}

// An informational status is not the response: the final one can still follow
// it, so a panic after a 103 is answered.
func TestRecover_PanicAfterAnInformationalStatusIsAnswered(t *testing.T) {
	api, _ := newTestREST(t, Config{})
	api.GET("/things", func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusEarlyHints)
		panic("bug")
	})

	// Through a real server: a ResponseRecorder keeps the 103 as its code, where a
	// connection sends it ahead of the final status.
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)

	res, err := server.Client().Get(server.URL + "/things")
	require.NoError(t, err)
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, res.StatusCode)
	assert.JSONEq(t, internalErrorBody, string(body))
}

// http.ErrAbortHandler is how a handler asks net/http to abort its response, and
// it is passed on as it is, never turned into an answer.
func TestRecover_ErrAbortHandlerGoesOn(t *testing.T) {
	api, _ := newTestREST(t, Config{})
	api.GET("/things", func(rw http.ResponseWriter, req *http.Request) {
		panic(http.ErrAbortHandler)
	})

	rw := httptest.NewRecorder()
	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		api.Handler().ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/things", nil))
	})
	assert.Empty(t, rw.Body.String())
}

// A panic in EdgeMiddleware itself is outside what is recovered: there is no edge
// left to carry its headers.
func TestRecover_PanicInEdgeMiddlewareIsNotAnswered(t *testing.T) {
	api, _ := newTestREST(t, Config{
		EdgeMiddleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
				panic("edge bug")
			})
		},
	})
	api.GET("/things", func(rw http.ResponseWriter, req *http.Request) {})

	assert.PanicsWithValue(t, "edge bug", func() {
		serve(api.Handler(), http.MethodGet, "/things", nil)
	})
}

// The writer handed on keeps what the one it wraps can do: a streaming handler
// can still flush, and a hijackable connection is still hijackable.
func TestRecover_WriterKeepsFlushAndHijack(t *testing.T) {
	plain, _ := newPanicAwareWriter(httptest.NewRecorder())
	_, flushes := plain.(http.Flusher)
	_, hijacks := plain.(http.Hijacker)
	assert.True(t, flushes)
	assert.False(t, hijacks, "a writer that cannot be hijacked must not claim it can")

	hijackable, state := newPanicAwareWriter(hijackableRecorder{httptest.NewRecorder()})
	_, hijacks = hijackable.(http.Hijacker)
	assert.True(t, hijacks)
	assert.False(t, state.started)

	require.NoError(t, http.NewResponseController(hijackable).Flush())
	assert.True(t, state.started)
}

// A payload that cannot be marshaled is the server failing, and is answered with
// the 500 error envelope rather than with a success-shaped body.
func TestResponseSuccess_Write_UnmarshalablePayloadIsTheErrorEnvelope(t *testing.T) {
	type reading struct {
		Value float64 `json:"value"`
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()

	NewResponseSuccess[NoMetadata, reading](req).
		SetStatus(http.StatusOK).
		SetData(reading{Value: math.NaN()}).
		Write(rw)

	assert.Equal(t, http.StatusInternalServerError, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t, internalErrorBody, rw.Body.String())

	var envelope map[string]any
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &envelope))
	assert.NotContains(t, envelope, "data")
}

// Invalid UTF-8 is refused by encoding/json/v2, where v1 substituted U+FFFD. It is
// answered as the 500 it is, with the request's own language.
func TestResponseSuccess_Write_InvalidUTF8IsTheErrorEnvelope(t *testing.T) {
	type place struct {
		Name string `json:"name"`
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()

	NewResponseSuccess[NoMetadata, place](req).
		SetData(place{Name: "Gr\xe4chen"}).
		Write(rw)

	assert.Equal(t, http.StatusInternalServerError, rw.Code)
	assert.JSONEq(t, internalErrorBody, rw.Body.String())
}

// A ResponseSuccess built without a request still answers its marshal failure.
func TestResponseSuccess_Write_UnmarshalablePayloadWithoutARequest(t *testing.T) {
	rw := httptest.NewRecorder()

	NewResponseSuccess[NoMetadata, float64](nil).
		SetData(math.Inf(1)).
		Write(rw)

	assert.Equal(t, http.StatusInternalServerError, rw.Code)
	assert.JSONEq(t, internalErrorBody, rw.Body.String())
}
