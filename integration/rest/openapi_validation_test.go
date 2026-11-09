package rest

import (
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bodySchema builds the schema the body tests validate against: an object with a
// list of objects, each requiring a name of at least three characters.
//
// Built by unmarshalling rather than by hand so the SchemaErrors it produces carry
// the real reverse paths kin-openapi records. Those paths are unexported, so a
// hand-assembled SchemaError would report no path at all and every path assertion
// below would pass while proving nothing.
func bodySchema(t *testing.T) *openapi3.Schema {
	t.Helper()

	const raw = `{
		"type": "object",
		"properties": {
			"items": {
				"type": "array",
				"items": {
					"type": "object",
					"required": ["name"],
					"properties": {
						"name": {"type": "string", "minLength": 3}
					}
				}
			}
		}
	}`

	var schema openapi3.Schema
	require.NoError(t, json.Unmarshal([]byte(raw), &schema), "building the test schema")

	return &schema
}

// TestRequestValidationEntriesKeepsEveryProblem is the property the flattening
// exists for.
//
// A body with two bad elements must produce two entries with two different paths.
// Both failure modes it guards are silent: errors.As on a MultiError matches the
// first contained error and discards the rest, and appending a per-entry suffix to a
// shared prefix slice lets the last entry overwrite the paths of the ones before it.
// Either one leaves a test that asserts only a count passing.
func TestRequestValidationEntriesKeepsEveryProblem(t *testing.T) {
	value := map[string]any{
		"items": []any{
			map[string]any{"name": "ok"},
			map[string]any{"name": "fine"},
			map[string]any{},
		},
	}

	err := bodySchema(t).VisitJSON(value, openapi3.MultiErrors())
	require.Error(t, err, "the test schema accepted a body it should have refused")

	entries := requestValidationEntries(&openapi3filter.RequestError{
		RequestBody: &openapi3.RequestBody{},
		Err:         err,
	})

	require.Len(t, entries, 2, "one per bad element")

	// The second element is missing `name` entirely rather than carrying a short
	// one, and it still reports the property rather than stopping at the object —
	// which is what lets a client mark the empty field instead of the whole row.
	wantPaths := [][]any{
		{"request", "body", "items", 0, "name"},
		{"request", "body", "items", 2, "name"},
	}

	for i, want := range wantPaths {
		assert.Equal(t, want, entries[i].Path, "entries[%d].Path", i)
		assert.NotEmpty(t, entries[i].Message, "entries[%d] carries no message", i)
	}
}

// TestRequestValidationEntriesLocatesEveryParameterKind pins the wire segment each
// of the four parameter locations reads back under, plus the body. A client puts a
// message beside the input that caused it by matching this path, so a segment that
// moves silently relocates somebody's error message.
func TestRequestValidationEntriesLocatesEveryParameterKind(t *testing.T) {
	tests := []struct {
		name  string
		param *openapi3.Parameter
		want  []any
	}{
		{
			name:  "path",
			param: &openapi3.Parameter{Name: "thing_id", In: openapi3.ParameterInPath},
			want:  []any{"request", "params", "thing_id"},
		},
		{
			name:  "query",
			param: &openapi3.Parameter{Name: "page_size", In: openapi3.ParameterInQuery},
			want:  []any{"request", "query", "page_size"},
		},
		{
			name:  "header",
			param: &openapi3.Parameter{Name: "X-User-Id", In: openapi3.ParameterInHeader},
			want:  []any{"request", "headers", "X-User-Id"},
		},
		{
			name:  "cookie",
			param: &openapi3.Parameter{Name: "session", In: openapi3.ParameterInCookie},
			want:  []any{"request", "cookies", "session"},
		},
		{
			name:  "body",
			param: nil,
			want:  []any{"request", "body"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entries := requestValidationEntries(&openapi3filter.RequestError{
				Parameter: tc.param,
				Reason:    "value does not conform",
			})

			require.Len(t, entries, 1)
			assert.Equal(t, tc.want, entries[0].Path)
		})
	}
}

// TestRequestValidationEntriesFlattensATopLevelMultiError covers the other place a
// MultiError appears: ValidateRequest returns one holding a RequestError per
// parameter that failed, before any single body is looked at.
func TestRequestValidationEntriesFlattensATopLevelMultiError(t *testing.T) {
	entries := requestValidationEntries(openapi3.MultiError{
		&openapi3filter.RequestError{
			Parameter: &openapi3.Parameter{Name: "page_size", In: openapi3.ParameterInQuery},
			Reason:    "must be an integer",
		},
		&openapi3filter.RequestError{
			Parameter: &openapi3.Parameter{Name: "thing_id", In: openapi3.ParameterInPath},
			Reason:    "must be a uuid",
		},
	})

	require.Len(t, entries, 2, "one per failing parameter")
	assert.Equal(t, []any{"request", "query", "page_size"}, entries[0].Path)
	assert.Equal(t, []any{"request", "params", "thing_id"}, entries[1].Path)
}

// TestRequestValidationEntriesReportsAnUnrecognisedFailure holds the fallback. A
// shape the walk does not know still has to leave with something a caller can read,
// because a refusal nobody can act on is the worst outcome available here.
func TestRequestValidationEntriesReportsAnUnrecognisedFailure(t *testing.T) {
	entries := requestValidationEntries(errors.New("something the walk has no case for"))

	require.Len(t, entries, 1)
	assert.Equal(t, "something the walk has no case for", entries[0].Message, "the original text")
	assert.Nil(t, entries[0].Path, "an unlocatable failure claims no location")
}

// TestRequestValidationEntriesNeverEmitsAnEmptyMessage covers the entry a
// RequestError with neither reason nor cause would otherwise produce. An envelope
// with an empty sentence in it reads as a bug in the client.
func TestRequestValidationEntriesNeverEmitsAnEmptyMessage(t *testing.T) {
	entries := requestValidationEntries(&openapi3filter.RequestError{
		RequestBody: &openapi3.RequestBody{},
	})

	require.Len(t, entries, 1)
	assert.Equal(t, requestValidationFallback, entries[0].Message, "the fallback sentence")
}

// TestRequestValidationEntriesIgnoresANilError guards the caller's shape: the
// middleware only reaches the flattening when validation failed, and a nil here
// would mean the refusal path ran over a request that was fine.
func TestRequestValidationEntriesIgnoresANilError(t *testing.T) {
	assert.Nil(t, requestValidationEntries(nil))
}

// TestPointerSegmentsTypesListIndexes holds the int/string split. A path is compared
// segment by segment by whoever consumes it, and "0" is not 0.
func TestPointerSegmentsTypesListIndexes(t *testing.T) {
	assert.Equal(t, []any{"items", 0, "name"}, pointerSegments([]string{"items", "0", "name"}))
}

// TestJoinPathDoesNotAliasThePrefix is the unit-level statement of what
// TestRequestValidationEntriesKeepsEveryProblem proves end to end. It is kept
// separately because the aliasing is reintroduced by a one-word edit (`append(prefix,
// …)`) and this fails on that edit alone, with no schema in the way.
func TestJoinPathDoesNotAliasThePrefix(t *testing.T) {
	prefix := make([]any, 2, 8) // Spare capacity is what makes aliasing possible.
	prefix[0], prefix[1] = "request", "body"

	first := joinPath(prefix, []any{"name"})
	second := joinPath(prefix, []any{"email"})

	assert.Equal(t, []any{"request", "body", "name"}, first, "the first path was overwritten by the second")
	assert.Equal(t, []any{"request", "body", "email"}, second)
}

// validationRefusal is a request-validation refusal as a client reads it.
type validationRefusal struct {
	Errors []struct {
		Message string `json:"message"`
		Path    []any  `json:"path"`
	} `json:"errors"`
}

// refuseEvent sends one request failing its description everywhere it can, a path
// parameter, a query parameter and five problems in its body, through a router
// validating it against description, and returns the refusal.
func refuseEvent(t *testing.T, description string) validationRefusal {
	t.Helper()

	api, _ := newTestREST(t, Config{
		OpenAPI: ConfigOpenAPI{
			Enabled:      true,
			Descriptions: []string{description},
		},
	})

	var called bool
	api.POST("/events/:page", func(rw http.ResponseWriter, req *http.Request) {
		called = true
	})

	req := httptest.NewRequest(http.MethodPost, "/events/99?limit=20", strings.NewReader(`{"name":"ab","tags":["ok","Bad",1],"count":-1,"extra":true}`))
	req.Header.Set("Content-Type", "application/json")

	rw := httptest.NewRecorder()
	api.Handler().ServeHTTP(rw, req)

	require.False(t, called, "the handler ran for a refused request")
	require.Equal(t, http.StatusBadRequest, rw.Code, rw.Body.String())

	var refusal validationRefusal
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &refusal), rw.Body.String())

	return refusal
}

// TestValidation_JSONSchema2020ReportsOneEntryPerProblem pins how an OpenAPI 3.1
// description's refusal reads. Such a description is validated as JSON Schema
// 2020-12, whose validator reports a failure as one text: a header naming the schema
// by the placeholder URL it was compiled under, then every problem under it. Each
// problem is an entry of its own instead, at the location of the value it concerns,
// with the problem's own message and nothing of the header: no URL, no line breaks.
// The entries come in the order of their locations, whatever order the validator
// found them in.
func TestValidation_JSONSchema2020ReportsOneEntryPerProblem(t *testing.T) {
	refusal := refuseEvent(t, "./testdata/openapi.31.yaml")

	type entry struct {
		Message string
		Path    []any
	}
	want := []entry{
		{Message: "maximum: got 99, want 50", Path: []any{"request", "params", "page"}},
		{Message: "maximum: got 20, want 10", Path: []any{"request", "query", "limit"}},
		{Message: "additional properties 'extra' not allowed", Path: []any{"request", "body"}},
		{Message: "minimum: got -1, want 0", Path: []any{"request", "body", "count"}},
		{Message: "minLength: got 2, want 3", Path: []any{"request", "body", "name"}},
		{Message: "'Bad' does not match pattern '^[a-z]+$'", Path: []any{"request", "body", "tags", float64(1)}},
		{Message: "got number, want string", Path: []any{"request", "body", "tags", float64(2)}},
	}

	got := make([]entry, 0, len(refusal.Errors))
	for _, e := range refusal.Errors {
		got = append(got, entry{Message: e.Message, Path: e.Path})

		assert.NotContains(t, e.Message, "example.com", "an entry names the validator's placeholder URL")
		assert.NotContains(t, e.Message, "jsonschema validation failed", "an entry carries the validator's report header")
		assert.NotContains(t, e.Message, "\n", "an entry spans several lines")
	}

	assert.Equal(t, want, got)
}

// TestValidation_OpenAPI30EntriesAreUnchanged pins the same refusal of the same
// description written as OpenAPI 3.0, which kin-openapi validates itself, one
// SchemaError per problem: its entries are its own messages, as they always were.
func TestValidation_OpenAPI30EntriesAreUnchanged(t *testing.T) {
	refusal := refuseEvent(t, "./testdata/openapi.30.yaml")

	got := make(map[string]string, len(refusal.Errors))
	for _, e := range refusal.Errors {
		path, err := json.Marshal(e.Path)
		require.NoError(t, err)
		got[string(path)] = e.Message
	}

	assert.Equal(t, map[string]string{
		`["request","params","page"]`: "number must be at most 50",
		`["request","query","limit"]`: "number must be at most 10",
		`["request","body","count"]`:  "number must be at least 0",
		`["request","body"]`:          `property "extra" is unsupported`,
		`["request","body","name"]`:   "minimum string length is 3",
		`["request","body","tags",1]`: `string doesn't match the regular expression "^[a-z]+$"`,
		`["request","body","tags",2]`: "value must be a string",
	}, got)
	assert.Len(t, refusal.Errors, 7, "one entry per problem")
}

// TestJSONSchemaMessageNeverNamesASchema pins the guard on the two validator error
// kinds whose message names a schema by its URL: were one to reach an entry, the
// fixed sentence is used in its place.
func TestJSONSchemaMessageNeverNamesASchema(t *testing.T) {
	for _, leaf := range []*jsonschema.ValidationError{
		{ErrorKind: &kind.Schema{Location: "https://schemas.example/event.json#"}},
		{ErrorKind: &kind.RefCycle{URL: "https://schemas.example/event.json#/$defs/a", KeywordLocation1: "/a", KeywordLocation2: "/b"}},
	} {
		assert.Equal(t, requestValidationFallback, jsonSchemaMessage(leaf))
	}

	assert.Equal(t, "minLength: got 2, want 3", jsonSchemaMessage(&jsonschema.ValidationError{ErrorKind: &kind.MinLength{Got: 2, Want: 3}}))
}

// TestCompareLocationsOrdersAsAReaderWould pins the order of the entries: an index
// before a greater one, numerically, a name before a later one, and a location before
// the ones inside it.
func TestCompareLocationsOrdersAsAReaderWould(t *testing.T) {
	locations := [][]string{
		{"tags", "10"},
		{"tags", "2"},
		{"name"},
		{},
		{"tags"},
	}

	slices.SortStableFunc(locations, compareLocations)

	assert.Equal(t, [][]string{{}, {"name"}, {"tags"}, {"tags", "2"}, {"tags", "10"}}, locations)
}

// postShape sends body to POST /shapes through a router validating it against the
// OpenAPI 3.1 shapes description, and returns the answer.
func postShape(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()

	api, _ := newTestREST(t, Config{
		OpenAPI: ConfigOpenAPI{
			Enabled:      true,
			Descriptions: []string{"./testdata/openapi.31.shapes.yaml"},
		},
	})
	api.POST("/shapes", func(rw http.ResponseWriter, req *http.Request) {})

	req := httptest.NewRequest(http.MethodPost, "/shapes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rw := httptest.NewRecorder()
	api.Handler().ServeHTTP(rw, req)

	require.Equal(t, http.StatusBadRequest, rw.Code, rw.Body.String())

	return rw
}

// shapeEntries reads a refusal's entries as "path message", in order.
func shapeEntries(t *testing.T, rw *httptest.ResponseRecorder) []string {
	t.Helper()

	var refusal validationRefusal
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &refusal), rw.Body.String())

	entries := make([]string, 0, len(refusal.Errors))
	for _, e := range refusal.Errors {
		path, err := json.Marshal(e.Path)
		require.NoError(t, err)
		entries = append(entries, string(path)+" "+e.Message)
	}

	return entries
}

// TestValidation_JSONSchema2020AnswersEachProblemOnce pins the problems a JSON
// Schema 2020-12 refusal is made of, for the keywords whose failure is made of other
// failures.
//
// A oneOf or an anyOf that matched none of its branches is one entry at the value,
// with the validator's own message for it: the failures of its branches contradict
// each other (one wants "Point", the next "LineString") and repeat each other, and
// the value had only to match one branch. A oneOf that matched two branches says
// which. A problem two branches of an allOf both report is one entry. The names an
// entry lists come sorted, the properties a body adds included.
func TestValidation_JSONSchema2020AnswersEachProblemOnce(t *testing.T) {
	t.Run("alternatives, an allOf, extra properties", func(t *testing.T) {
		rw := postShape(t, `{"name":"a","geometry":{"type":"Polygon","coordinates":[[1,2]],"x":1},"style":true,"size":3,"meta":{},"zzz":2,"extra":1}`)

		assert.Equal(t, []string{
			`["request","body"] additional properties 'extra', 'zzz' not allowed`,
			`["request","body","geometry"] 'oneOf' failed, none matched`,
			`["request","body","meta"] missing property 'label'`,
			`["request","body","size"] 'oneOf' failed, subschemas 0, 1 matched`,
			`["request","body","style"] 'anyOf' failed`,
		}, shapeEntries(t, rw))
	})

	t.Run("a oneOf that is the body's one problem", func(t *testing.T) {
		rw := postShape(t, `{"name":"a","geometry":{"type":"Polygon","coordinates":[[1,2]]}}`)

		assert.Equal(t, []string{
			`["request","body","geometry"] 'oneOf' failed, none matched`,
		}, shapeEntries(t, rw))
	})

	t.Run("a oneOf whose matching branch has a problem of its own", func(t *testing.T) {
		rw := postShape(t, `{"name":"a","geometry":{"type":"Point","coordinates":[1]}}`)

		assert.Equal(t, []string{
			`["request","body","geometry"] 'oneOf' failed, none matched`,
		}, shapeEntries(t, rw))
	})
}

// TestValidation_JSONSchema2020AnswersTheSameEveryTime sends one refused request 200
// times and requires 200 byte-identical answers. The validator meets the properties
// of an object in no fixed order, so an answer that followed it would list two added
// properties, or order two entries at one location, one way on one request and the
// other way on the next.
func TestValidation_JSONSchema2020AnswersTheSameEveryTime(t *testing.T) {
	const body = `{"name":"a","geometry":{"type":"Polygon","coordinates":[[1,2]],"x":1},"style":true,"meta":{},"zzz":2,"extra":1,"yyy":3}`

	first := postShape(t, body).Body.String()
	for range 199 {
		require.Equal(t, first, postShape(t, body).Body.String())
	}
	assert.Contains(t, first, `additional properties 'extra', 'yyy', 'zzz' not allowed`)
}

// TestJSONSchemaMessageSortsTheNamesItLists pins the names a message lists sorted,
// whatever order the validator met them in, and the validator's own list left as it
// was.
func TestJSONSchemaMessageSortsTheNamesItLists(t *testing.T) {
	added := &kind.AdditionalProperties{Properties: []string{"zzz", "extra"}}
	assert.Equal(t, "additional properties 'extra', 'zzz' not allowed", jsonSchemaMessage(&jsonschema.ValidationError{ErrorKind: added}))
	assert.Equal(t, []string{"zzz", "extra"}, added.Properties)

	assert.Equal(t, "missing properties 'a', 'b'", jsonSchemaMessage(&jsonschema.ValidationError{ErrorKind: &kind.Required{Missing: []string{"b", "a"}}}))
	assert.Equal(t, "properties 'a', 'b' required, if 'c' exists", jsonSchemaMessage(&jsonschema.ValidationError{ErrorKind: &kind.DependentRequired{Prop: "c", Missing: []string{"b", "a"}}}))
	assert.Equal(t, "properties 'a', 'b' required, if 'c' exists", jsonSchemaMessage(&jsonschema.ValidationError{ErrorKind: &kind.Dependency{Prop: "c", Missing: []string{"b", "a"}}}))
}
