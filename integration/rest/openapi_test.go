package rest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/service/servicetest"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsValidURL(t *testing.T) {
	testcases := []struct {
		name  string
		input string
		valid bool
	}{
		{
			name:  "valid HTTPS URL",
			input: "https://example.com/openapi.yaml",
			valid: true,
		},
		{
			name:  "valid HTTP localhost URL",
			input: "http://localhost:8080/openapi.yaml",
			valid: true,
		},
		{
			name:  "valid HTTPS URL with path segments",
			input: "https://api.example.com/v1/openapi.json",
			valid: true,
		},
		{
			name:  "relative path",
			input: "./descriptions/openapi.yaml",
			valid: false,
		},
		{
			name:  "absolute path",
			input: "/absolute/path/openapi.yaml",
			valid: false,
		},
		{
			name:  "filename only",
			input: "openapi.yaml",
			valid: false,
		},
		{
			name:  "empty string",
			input: "",
			valid: false,
		},
		{
			name:  "not a URL",
			input: "not a url at all",
			valid: false,
		},
		{
			name:  "valid FTP URL",
			input: "ftp://files.example.com/openapi.yaml",
			valid: true,
		},
		{
			name:  "scheme only",
			input: "https://",
			valid: false,
		},
		{
			name:  "URL with query params",
			input: "https://example.com/openapi.yaml?version=3",
			valid: true,
		},
		{
			name:  "URL with fragment",
			input: "https://example.com/openapi.yaml#section",
			valid: true,
		},
		{
			name:  "URL with port",
			input: "https://example.com:443/openapi.yaml",
			valid: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			u, ok := isValidURL(tc.input)

			assert.Equal(t, tc.valid, ok)
			if tc.valid {
				assert.NotNil(t, u)
			} else {
				assert.Nil(t, u)
			}
		})
	}
}

// findRoute asks router which operation describes method and target, and returns
// the route's path, or the error FindRoute returned.
func findRoute(t *testing.T, router routers.Router, method, target string) (string, error) {
	t.Helper()

	route, _, err := router.FindRoute(httptest.NewRequest(method, target, nil))
	if err != nil {
		return "", err
	}

	return route.Path, nil
}

func TestNewOpenAPIRouter_MatchesAnyHost(t *testing.T) {
	router, err := NewOpenAPIRouter(t.Context(), "./testdata/openapi.yaml")
	require.NoError(t, err)

	// The description names https://api.example.com; a server runs anywhere else.
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/things/42", nil)
	route, params, err := router.FindRoute(req)

	require.NoError(t, err)
	assert.Equal(t, "/things/{id}", route.Path)
	assert.Equal(t, "42", params["id"])
}

// A server URL's path is part of every operation's URL. Dropping it — which
// replacing every server with "/" did — left every route of a versioned API
// unmatched, and so silently unvalidated.
func TestNewOpenAPIRouter_KeepsTheServerBasePath(t *testing.T) {
	router, err := NewOpenAPIRouter(t.Context(), "./testdata/openapi.basepath.yaml")
	require.NoError(t, err)

	path, err := findRoute(t, router, http.MethodGet, "/v1/things/42")
	require.NoError(t, err)
	assert.Equal(t, "/things/{id}", path)

	_, err = findRoute(t, router, http.MethodGet, "/things/42")
	assert.ErrorIs(t, err, routers.ErrPathNotFound)
}

func TestNewOpenAPIRouter_MergesDescriptions(t *testing.T) {
	router, err := NewOpenAPIRouter(t.Context(), "./testdata/openapi.yaml", "./testdata/openapi.internal.yaml")
	require.NoError(t, err)

	// The two descriptions declare different methods on the same path.
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		path, err := findRoute(t, router, method, "/things/42")
		require.NoError(t, err, method)
		assert.Equal(t, "/things/{id}", path)
	}
}

func TestNewOpenAPIRouter_RefusesADuplicateOperation(t *testing.T) {
	_, err := NewOpenAPIRouter(t.Context(), "./testdata/openapi.yaml", "./testdata/openapi.yaml")

	require.Error(t, err)
	entries := errorstack.EntriesOf(err)
	require.Len(t, entries, 1)
	assert.Contains(t, entries[0].Message, "is declared in more than one OpenAPI description")
	assert.Equal(t, []any{1}, entries[0].Path, "the duplicate is the second description")
	assert.Equal(t, errorstack.CodeValidationFailed, entries[0].Extensions["code"])
}

// The router assumes a valid document, so an invalid one is refused before it is
// built rather than surfacing as requests that look wrong.
func TestNewOpenAPIRouter_RefusesAnInvalidDescription(t *testing.T) {
	_, err := NewOpenAPIRouter(t.Context(), "./testdata/openapi.invalid.yaml")

	require.Error(t, err)
	entries := errorstack.EntriesOf(err)
	require.Len(t, entries, 1)
	assert.Equal(t, errorstack.CodeValidationFailed, entries[0].Extensions["code"])
	assert.NotEmpty(t, entries[0].Message)
}

// An example is documentation. The validator reads one with OpenAPI 3.0's rules,
// under which a null does not satisfy an unconstrained schema ({}), though OpenAPI
// 3.1 allows it, so validating examples would refuse a valid 3.1 description.
func TestNewOpenAPIRouter_DoesNotValidateExamples(t *testing.T) {
	router, err := NewOpenAPIRouter(t.Context(), "./testdata/openapi.example.yaml")
	require.NoError(t, err)

	path, err := findRoute(t, router, http.MethodGet, "/things/42")
	require.NoError(t, err)
	assert.Equal(t, "/things/{id}", path)
}

func TestNewOpenAPIRouter_RefusesAMissingDescription(t *testing.T) {
	_, err := NewOpenAPIRouter(t.Context(), "./testdata/openapi.yaml", "./testdata/missing.yaml")

	require.Error(t, err)
	entries := errorstack.EntriesOf(err)
	require.Len(t, entries, 1)
	assert.Equal(t, []any{1}, entries[0].Path)
}

func TestLoadOpenAPIDescriptions_RefusesNone(t *testing.T) {
	_, err := LoadOpenAPIDescriptions(t.Context())

	assert.Equal(t, errorstack.New("Must be set", errorstack.WithCode(errorstack.CodeValidationFailed)), err)
}

// A description served by a host that never answers must not hold New forever:
// the loading is bounded by the context New was given.
func TestLoadOpenAPIDescriptions_HonoursTheContext(t *testing.T) {
	stalled := make(chan struct{})
	t.Cleanup(func() { close(stalled) })

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		select {
		case <-stalled:
		case <-req.Context().Done():
		}
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := LoadOpenAPIDescriptions(ctx, server.URL+"/openapi.yaml")
		done <- err
	}()

	select {
	case err := <-done:
		assert.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("LoadOpenAPIDescriptions ignored its context")
	}
}

// New refuses an OpenAPI description it cannot use, and says which one.
func TestNew_RefusesAnInvalidOpenAPIDescription(t *testing.T) {
	svc, _ := servicetest.New(t)

	_, err := New(t.Context(), svc, Config{
		Address: "127.0.0.1:0",
		OpenAPI: ConfigOpenAPI{
			Enabled:      true,
			Descriptions: []string{"./testdata/openapi.yaml", "./testdata/missing.yaml"},
		},
	})

	require.Error(t, err)
	entries := errorstack.EntriesOf(err)
	require.Len(t, entries, 1)
	assert.Equal(t, []any{"config", "openapi", "descriptions", 1}, entries[0].Path)
	assert.Equal(t, errorstack.CodeValidationFailed, entries[0].Extensions["code"])
}

// validateAgainst validates one request, and the response answered to it, against
// the operation router finds for the request: the two checks the validation
// middleware makes, with the options it makes them with. A request with no body
// is validated as one, and a response with no body is not validated at all.
func validateAgainst(t *testing.T, router routers.Router, method, target, requestBody string, status int, responseBody string) (requestErr, responseErr error) {
	t.Helper()

	var body io.Reader
	if requestBody != "" {
		body = strings.NewReader(requestBody)
	}

	req := httptest.NewRequest(method, target, body)
	if requestBody != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	route, pathParams, err := router.FindRoute(req)
	require.NoError(t, err)

	in := &openapi3filter.RequestValidationInput{
		Request:     req,
		PathParams:  pathParams,
		QueryParams: req.URL.Query(),
		Route:       route,
		Options:     requestValidationOptions,
	}

	requestErr = openapi3filter.ValidateRequest(t.Context(), in)

	if responseBody == "" {
		return requestErr, nil
	}

	responseErr = openapi3filter.ValidateResponse(t.Context(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: in,
		Status:                 status,
		Header:                 http.Header{"Content-Type": {"application/json"}},
		Body:                   io.NopCloser(strings.NewReader(responseBody)),
		Options:                responseValidationOptions,
	})

	return requestErr, responseErr
}

// Under OpenAPI 3.1 the empty schema admits every value, null included, and a
// description says "anything here" with it: an envelope leaves its payload to each
// operation to narrow, and one with no payload narrows it to null. The validator
// read the empty schema with OpenAPI 3.0's rules, refusing the null, so every
// answer with no payload was recorded as failing validation. A null is admitted
// where the empty schema stands, and only there: what an operation narrows is held
// as it narrows it.
func TestNewOpenAPIRouter_AnEmptySchemaAdmitsNullUnderOpenAPI31(t *testing.T) {
	router, err := NewOpenAPIRouter(t.Context(), "./testdata/openapi.null.yaml")
	require.NoError(t, err)

	testcases := []struct {
		name         string
		method       string
		target       string
		requestBody  string
		status       int
		responseBody string
		refused      string
	}{
		{
			name:         "an answer narrowing the payload to null, answering null",
			method:       http.MethodDelete,
			target:       "/things/42",
			status:       http.StatusOK,
			responseBody: `{"data":null}`,
		},
		{
			name:         "an answer narrowing the payload to null, answering a payload",
			method:       http.MethodDelete,
			target:       "/things/42",
			status:       http.StatusOK,
			responseBody: `{"data":{"id":"42","tags":[]}}`,
			refused:      "response",
		},
		{
			name:         "an answer narrowing the payload to null, answering none",
			method:       http.MethodDelete,
			target:       "/things/42",
			status:       http.StatusOK,
			responseBody: `{}`,
			refused:      "response",
		},
		{
			name:         "an empty schema for the items of an array, holding nulls",
			method:       http.MethodGet,
			target:       "/things/42",
			status:       http.StatusOK,
			responseBody: `{"data":{"id":"42","tags":[null,"a",1,true,{},[]]}}`,
		},
		{
			name:         "an answer narrowing the payload to an object, answering null",
			method:       http.MethodGet,
			target:       "/things/42",
			status:       http.StatusOK,
			responseBody: `{"data":null}`,
			refused:      "response",
		},
		{
			name:         "a typed member beside an empty schema, answering null",
			method:       http.MethodGet,
			target:       "/things/42",
			status:       http.StatusOK,
			responseBody: `{"data":{"id":null,"tags":[]}}`,
			refused:      "response",
		},
		{
			name:        "a request body member of the empty schema, sent null",
			method:      http.MethodPost,
			target:      "/things",
			requestBody: `{"name":"thing","attributes":null}`,
		},
		{
			name:        "a typed request body member, sent null",
			method:      http.MethodPost,
			target:      "/things",
			requestBody: `{"name":null}`,
			refused:     "request",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			requestErr, responseErr := validateAgainst(t, router, tc.method, tc.target, tc.requestBody, tc.status, tc.responseBody)

			if tc.refused == "request" {
				assert.Error(t, requestErr)
			} else {
				assert.NoError(t, requestErr)
			}

			if tc.refused == "response" {
				assert.Error(t, responseErr)
			} else {
				assert.NoError(t, responseErr)
			}
		})
	}
}

// OpenAPI 3.0 has no null type: a null is a value only where `nullable` admits
// it, and the empty schema does not. A 3.0 description is validated as written.
func TestNewOpenAPIRouter_AnEmptySchemaRefusesNullUnderOpenAPI30(t *testing.T) {
	description := writeDescription(t, `openapi: 3.0.3
info:
  title: Things
  version: 1.0.0
paths:
  /things/{id}:
    delete:
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: The thing was deleted.
          content:
            application/json:
              schema:
                type: object
                required: [data]
                properties:
                  data: {}
`)

	router, err := NewOpenAPIRouter(t.Context(), description)
	require.NoError(t, err)

	_, responseErr := validateAgainst(t, router, http.MethodDelete, "/things/42", "", http.StatusOK, `{"data":null}`)
	assert.Error(t, responseErr)
}

// Only a schema a JSON body reaches is spelled as every type. A parameter's or a
// header's schema also decides how its text is decoded, which listing types would
// change, and none of them can hold a null.
func TestNewOpenAPIRouter_OnlyAJSONBodyEmptySchemaIsRespelled(t *testing.T) {
	description := writeDescription(t, `openapi: 3.1.0
info:
  title: Things
  version: 1.0.0
paths:
  /things:
    get:
      parameters:
        - name: fields
          in: query
          schema: {}
      responses:
        "200":
          description: The things.
          headers:
            X-Cursor:
              schema: {}
          content:
            application/json:
              schema:
                type: object
                properties:
                  data: {}
            application/vnd.things+json:
              schema:
                type: object
                properties:
                  data:
                    type: array
                    items: {}
            text/plain:
              schema: {}
`)

	router, err := NewOpenAPIRouter(t.Context(), description)
	require.NoError(t, err)

	route, _, err := router.FindRoute(httptest.NewRequest(http.MethodGet, "/things", nil))
	require.NoError(t, err)

	operation := route.Spec.Paths.Value("/things").Get
	answer := operation.Responses.Status(http.StatusOK).Value

	assert.True(t, answer.Content.Get("application/json").Schema.Value.Properties["data"].Value.Type.Includes(openapi3.TypeNull))
	assert.True(t, answer.Content.Get("application/vnd.things+json").Schema.Value.Properties["data"].Value.Items.Value.Type.Includes(openapi3.TypeNull))
	assert.True(t, operation.Parameters.GetByInAndName(openapi3.ParameterInQuery, "fields").Schema.Value.IsEmpty())
	assert.True(t, answer.Headers["X-Cursor"].Value.Schema.Value.IsEmpty())
	assert.True(t, answer.Content.Get("text/plain").Schema.Value.IsEmpty())
}

func TestIsJSONMediaType(t *testing.T) {
	testcases := []struct {
		mediaType string
		json      bool
	}{
		{mediaType: "application/json", json: true},
		{mediaType: "application/json; charset=utf-8", json: true},
		{mediaType: "Application/JSON", json: true},
		{mediaType: "application/problem+json", json: true},
		{mediaType: "application/vnd.things+json; version=2", json: true},
		{mediaType: "application/x-www-form-urlencoded", json: false},
		{mediaType: "multipart/form-data", json: false},
		{mediaType: "text/plain", json: false},
		{mediaType: "application/jsonl", json: false},
		{mediaType: "application/*", json: false},
		{mediaType: "*/*", json: false},
		{mediaType: "", json: false},
		{mediaType: "not a media type", json: false},
	}

	for _, tc := range testcases {
		t.Run(tc.mediaType, func(t *testing.T) {
			assert.Equal(t, tc.json, isJSONMediaType(tc.mediaType))
		})
	}
}
