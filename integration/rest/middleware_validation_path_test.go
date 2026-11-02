package rest

import (
	"net/http"
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
)

func TestDecodePathParams(t *testing.T) {
	testcases := []struct {
		name        string
		matched     map[string]string
		decoded     map[string]string
		undecodable []errorstack.Entry
	}{
		{
			name: "no parameters",
		},
		{
			name:    "a segment with no escape is kept",
			matched: map[string]string{"name": "report_final,draft"},
			decoded: map[string]string{"name": "report_final,draft"},
		},
		{
			name:    "an escaped space and comma are decoded",
			matched: map[string]string{"name": "Report%20Final%2CDraft"},
			decoded: map[string]string{"name": "Report Final,Draft"},
		},
		{
			name:    "an escaped slash is decoded inside its own segment",
			matched: map[string]string{"name": "a%2Fb"},
			decoded: map[string]string{"name": "a/b"},
		},
		{
			name:    "a plus is a plus, not a space",
			matched: map[string]string{"name": "Report+Final%2BDraft"},
			decoded: map[string]string{"name": "Report+Final+Draft"},
		},
		{
			name:    "a segment is decoded once, never twice",
			matched: map[string]string{"name": "Report%2520"},
			decoded: map[string]string{"name": "Report%20"},
		},
		{
			name:        "a malformed escape is refused and kept as written",
			matched:     map[string]string{"name": "Report%zz", "folder": "Shared%20Files"},
			decoded:     map[string]string{"name": "Report%zz", "folder": "Shared Files"},
			undecodable: []errorstack.Entry{{Message: undecodablePathMessage, Path: []any{"request", "params", "name"}}},
		},
		{
			name:    "every parameter that does not decode is refused, in the order of the names",
			matched: map[string]string{"name": "Report%", "folder": "Shared%2"},
			decoded: map[string]string{"name": "Report%", "folder": "Shared%2"},
			undecodable: []errorstack.Entry{
				{Message: undecodablePathMessage, Path: []any{"request", "params", "folder"}},
				{Message: undecodablePathMessage, Path: []any{"request", "params", "name"}},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			decoded, undecodable := decodePathParams(tc.matched)

			if tc.decoded == nil {
				assert.Empty(t, decoded)
			} else {
				assert.Equal(t, tc.decoded, decoded)
			}
			assert.Equal(t, tc.undecodable, undecodable)
		})
	}
}

// The decoded parameters are a map of their own: the router's is left as it
// matched.
func TestDecodePathParams_DoesNotChangeWhatTheRouterMatched(t *testing.T) {
	matched := map[string]string{"name": "Report%20Final"}

	decodePathParams(matched)

	assert.Equal(t, map[string]string{"name": "Report%20Final"}, matched)
}

// newPathREST returns an API validated against testdata/openapi.path.yaml, with
// GET /files/:name, GET /folders/:folder/files/:name and GET /versions/:name
// counting the requests that reach them.
func newPathREST(t *testing.T, refuseUndeclaredQuery bool) (*rest, *int) {
	t.Helper()

	api, _ := newTestREST(t, Config{
		OpenAPI: ConfigOpenAPI{
			Enabled:               true,
			Descriptions:          []string{"./testdata/openapi.path.yaml"},
			RefuseUndeclaredQuery: refuseUndeclaredQuery,
		},
	})

	var called int
	file := func(rw http.ResponseWriter, req *http.Request) {
		called++
		NewResponseSuccess[NoMetadata, map[string]string](req).
			SetData(map[string]string{}).
			Write(rw)
	}

	api.GET("/files/:name", file)
	api.GET("/folders/:folder/files/:name", file)
	api.GET("/versions/:name", file)

	return api, &called
}

// A value a client cannot send without escaping, such as one holding a space,
// is admitted by a pattern that admits the space; an escape decoding to a
// character the pattern refuses is refused, whatever the escape itself spells.
func TestValidation_PathParamsAreValidatedDecoded(t *testing.T) {
	api, called := newPathREST(t, false)

	admitted := []string{
		"/files/Report%20Final",
		"/files/Report%20Final%2CDraft",
		"/files/Report%20Final,Draft",
		"/files/Report_Final,Draft",
		"/folders/Shared%20Files/files/Report%20Final",
	}

	for _, target := range admitted {
		t.Run(target, func(t *testing.T) {
			*called = 0

			rw := serve(api.Handler(), http.MethodGet, target, nil)

			assert.Equal(t, http.StatusOK, rw.Code, rw.Body.String())
			assert.Equal(t, 1, *called)
		})
	}

	refused := []struct {
		target string
		path   []any
	}{
		{target: "/files/a%2Fb", path: []any{"request", "params", "name"}},
		{target: "/files/%2e%2e", path: []any{"request", "params", "name"}},
		{target: "/files/Report%2BFinal", path: []any{"request", "params", "name"}},
		{target: "/files/Report+Final", path: []any{"request", "params", "name"}},
		{target: "/files/Report%2520Final", path: []any{"request", "params", "name"}},
		{target: "/folders/Shared%2CFiles/files/Report", path: []any{"request", "params", "folder"}},
	}

	for _, tc := range refused {
		t.Run(tc.target, func(t *testing.T) {
			*called = 0

			rw := serve(api.Handler(), http.MethodGet, tc.target, nil)

			require.Equal(t, http.StatusBadRequest, rw.Code)
			assert.Equal(t, [][]any{tc.path}, refusedPaths(t, rw.Body.Bytes()))
			assert.Zero(t, *called)
		})
	}
}

// The router matches the path as the client escaped it, so a template with a
// literal after a parameter in the same segment can end the parameter inside an
// escape: /versions/{name}1 matches /versions/Report%41 with name "Report%4". A
// segment that does not decode has no value to validate. It is refused for that
// alone, without the validator judging it as written (version=0 is not reported),
// and ahead of a query parameter the operation does not declare.
func TestValidation_PathParamThatDoesNotDecodeIsRefused(t *testing.T) {
	api, called := newPathREST(t, true)

	rw := serve(api.Handler(), http.MethodGet, "/versions/Report%41?version=0&extra=1", nil)

	require.Equal(t, http.StatusBadRequest, rw.Code)
	assert.Equal(t, [][]any{
		{"request", "params", "name"},
		{"request", "query", "extra"},
	}, refusedPaths(t, rw.Body.Bytes()))
	assert.Contains(t, rw.Body.String(), undecodablePathMessage)
	assert.Zero(t, *called)

	t.Run("a segment the router does not split is validated", func(t *testing.T) {
		*called = 0

		assert.Equal(t, http.StatusOK, serve(api.Handler(), http.MethodGet, "/versions/Report%20Final1", nil).Code)
		assert.Equal(t, 1, *called)
	})
}

func TestValidation_PathParamThatDoesNotDecodeIsRecorded(t *testing.T) {
	api, rec := newTestREST(t, Config{
		OpenAPI: ConfigOpenAPI{
			Enabled:      true,
			Descriptions: []string{"./testdata/openapi.path.yaml"},
		},
	})
	api.GET("/versions/:name", func(rw http.ResponseWriter, req *http.Request) {})

	rw := serve(api.Handler(), http.MethodGet, "/versions/Report%41", nil)

	require.Equal(t, http.StatusBadRequest, rw.Code)

	span := spanNamed(t, rec, "REST: OpenAPI / Request Validation")
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.Equal(t, "failed to validate request", span.Status().Description)

	var messages []string
	for _, event := range span.Events() {
		for _, attr := range event.Attributes {
			if attr.Key == "exception.message" {
				messages = append(messages, attr.Value.AsString())
			}
		}
	}
	assert.Equal(t, []string{errUndecodablePath.Error()}, messages)
}
