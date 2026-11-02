package rest

import (
	"encoding/json/v2"
	"errors"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
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
