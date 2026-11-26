package rest

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mountaya/helix.go/errorstack"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
)

func TestBoundText(t *testing.T) {
	testcases := []struct {
		name  string
		text  string
		limit int
		want  string
	}{
		{name: "a text within the limit is left as it is", text: "copies", limit: 8, want: "copies"},
		{name: "a text at the limit is left as it is", text: "copies", limit: 6, want: "copies"},
		{name: "an empty text", text: "", limit: 8, want: ""},
		{name: "a longer text is cut, and the cut marked", text: "attachments", limit: 8, want: "attac…"},
		{name: "a cut never falls inside a character", text: "aaaéaaaaa", limit: 7, want: "aaa…"},
		{name: "a character that fits exactly is kept", text: "aaéaaaaa", limit: 7, want: "aaé…"},
		{name: "bytes that are not UTF-8 become one replacement", text: "\xff\xfe", limit: 8, want: "�"},
		{name: "each run is replaced on its own", text: "a\xffb\xfe\xfdc", limit: 16, want: "a�b�c"},
		{name: "a text replaced, then too long, is cut", text: "aaaaa\xff", limit: 7, want: "aaaa…"},
		{name: "a truncated character is a replacement", text: "ab\xc3", limit: 8, want: "ab�"},
		{name: "a limit shorter than the marker cuts without one", text: "copies", limit: 2, want: "co"},
		{name: "a limit of zero", text: "copies", limit: 0, want: ""},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			got := boundText(tc.text, tc.limit)

			assert.Equal(t, tc.want, got)
			assert.LessOrEqual(t, len(got), tc.limit)
			assert.True(t, utf8.ValidString(got))
		})
	}
}

func TestBoundNames(t *testing.T) {
	long := strings.Repeat("n", maxQuotedBytes)

	t.Run("names within the limits are sorted and nothing else", func(t *testing.T) {
		names, more := boundNames([]string{"extra", "colour"})

		assert.Equal(t, []string{"colour", "extra"}, names)
		assert.Zero(t, more)
	})

	t.Run("names that differ only past the cut are one", func(t *testing.T) {
		names, more := boundNames([]string{long + "b", long + "a"})

		assert.Equal(t, []string{long[:maxQuotedBytes-len(cutMarker)] + cutMarker}, names)
		assert.Zero(t, more)
	})

	t.Run("no more names than a message lists", func(t *testing.T) {
		names := make([]string, 0, 3*maxRefusalNames)
		for i := range 3 * maxRefusalNames {
			names = append(names, fmt.Sprintf("extra_%03d", 3*maxRefusalNames-i))
		}

		got, more := boundNames(names)

		require.Len(t, got, maxRefusalNames)
		assert.Equal(t, 2*maxRefusalNames, more, "how many more there were")
		assert.Equal(t, "extra_001", got[0])
		assert.Equal(t, fmt.Sprintf("extra_%03d", maxRefusalNames), got[maxRefusalNames-1])
	})
}

func TestBoundPath(t *testing.T) {
	t.Run("a path within the limits is the same path, in a slice of its own", func(t *testing.T) {
		path := []any{"request", "body", "items", 0, "name"}

		got := boundPath(path)

		assert.Equal(t, path, got)
		got[0] = "changed"
		assert.Equal(t, "request", path[0], "the path handed in was written to")
	})

	t.Run("a name is cut and an index is left alone", func(t *testing.T) {
		got := boundPath([]any{"request", "query", strings.Repeat("q", 5000), 7})

		assert.Equal(t, []any{"request", "query", strings.Repeat("q", maxQuotedBytes-len(cutMarker)) + cutMarker, 7}, got)
	})

	t.Run("a deeper path keeps its first segments, and is marked", func(t *testing.T) {
		path := make([]any, 0, 200)
		for i := range 200 {
			path = append(path, i)
		}

		got := boundPath(path)

		require.Len(t, got, maxRefusalPathSegments)
		assert.Equal(t, path[:maxRefusalPathSegments-1], got[:maxRefusalPathSegments-1])
		assert.Equal(t, cutMarker, got[maxRefusalPathSegments-1], "a cut path does not show it was cut")
	})

	t.Run("a path of exactly the limit is not marked", func(t *testing.T) {
		path := make([]any, 0, maxRefusalPathSegments)
		for i := range maxRefusalPathSegments {
			path = append(path, i)
		}

		assert.Equal(t, path, boundPath(path))
	})

	t.Run("no path stays no path", func(t *testing.T) {
		assert.Nil(t, boundPath(nil))
	})
}

// envelopeLength returns how many bytes the refusal of entries is written as.
func envelopeLength(t *testing.T, entries []errorstack.Entry) int {
	t.Helper()

	encoded, err := NewResponseError[NoMetadata](httptest.NewRequest(http.MethodGet, "/", nil)).
		SetStatus(http.StatusBadRequest).
		SetValidations(entries...).
		MarshalJSON()
	require.NoError(t, err)

	return len(encoded)
}

func TestBoundEntries(t *testing.T) {
	t.Run("an ordinary refusal is left as it is", func(t *testing.T) {
		entries := []errorstack.Entry{
			{Message: "value must be a string", Path: []any{"request", "body", "tags", 0}},
			{Message: "missing property 'name'", Path: []any{"request", "body"}},
		}

		assert.Equal(t, []errorstack.Entry{
			{Message: "value must be a string", Path: []any{"request", "body", "tags", 0}},
			{Message: "missing property 'name'", Path: []any{"request", "body"}},
		}, boundEntries(entries))
	})

	t.Run("each entry is bounded, and the entries past the budget counted", func(t *testing.T) {
		const total = 300

		entries := make([]errorstack.Entry, 0, total)
		for i := range total {
			entries = append(entries, errorstack.Entry{
				Message: strings.Repeat("m", 3*maxRefusalMessageBytes),
				Path:    []any{"request", "body", "items", i, "\xff" + strings.Repeat("n", 5000)},
			})
		}

		got := boundEntries(entries)

		listed := got[:len(got)-1]
		require.NotEmpty(t, listed)

		weight := 0
		for i, entry := range listed {
			assert.Equal(t, strings.Repeat("m", maxRefusalMessageBytes-len(cutMarker))+cutMarker, entry.Message)
			assert.Equal(t, []any{"request", "body", "items", i, "�" + strings.Repeat("n", maxQuotedBytes-len("�")-len(cutMarker)) + cutMarker}, entry.Path)
			weight += entryWeight(entry)
		}

		assert.LessOrEqual(t, weight+moreProblemsWeight, maxRefusalBytes)

		last := got[len(got)-1]
		assert.Equal(t, moreProblemsMessage(total-len(listed)), last.Message)
		assert.Nil(t, last.Path, "the count is about no value of the request")
	})

	t.Run("an entry the envelope writes is weighed as it is written", func(t *testing.T) {
		entries := []errorstack.Entry{
			{Message: "value must be a string", Path: []any{"request", "body", "tags", 0}},
			{Message: "<b>&\"quoted\"\u2028\x01é", Path: []any{"request", "query", "q<&>", 12345}},
			{Message: "missing property 'name'", Path: nil, Extensions: map[string]any{"limit": 3}},
		}

		weighed := 0
		for _, entry := range entries[1:] {
			weighed += entryWeight(entry)
		}

		assert.Equal(t, envelopeLength(t, entries)-envelopeLength(t, entries[:1]), weighed)
	})

	t.Run("ordinary entries fill the bound", func(t *testing.T) {
		const total = 5000

		entries := make([]errorstack.Entry, 0, total)
		for i := range total {
			entries = append(entries, errorstack.Entry{Message: "value must be a string", Path: []any{"request", "body", "tags", i}})
		}

		written := envelopeLength(t, boundEntries(entries))

		assert.Greater(t, written, maxRefusalBytes-1<<10, "the refusal lists far fewer entries than the bound holds")
		assert.LessOrEqual(t, written, refusalCeiling)
	})

	t.Run("an entry heavier than the bound on its own is kept, cut to fit and marked", func(t *testing.T) {
		heavy := func() errorstack.Entry {
			path := []any{"request", "body"}
			for len(path) < maxRefusalPathSegments {
				path = append(path, strings.Repeat("\x01", maxQuotedBytes))
			}

			return errorstack.Entry{Message: strings.Repeat("\x02", maxRefusalMessageBytes), Path: path}
		}

		for _, entries := range [][]errorstack.Entry{{heavy()}, {heavy(), heavy(), heavy()}} {
			got := boundEntries(entries)

			first := got[0]
			assert.NotEqual(t, moreProblemsMessage(len(entries)), first.Message, "the refusal lists nothing of the entry")
			assert.True(t, strings.HasSuffix(first.Message, cutMarker), "the entry's message does not show it was cut")
			require.NotEmpty(t, first.Path)
			assert.Equal(t, []any{"request", "body"}, first.Path[:2])
			assert.Equal(t, cutMarker, first.Path[len(first.Path)-1], "the entry's path does not show it was cut")

			budget := maxRefusalBytes
			if len(entries) > 1 {
				budget -= moreProblemsWeight
				require.Len(t, got, 2)
				assert.Equal(t, moreProblemsMessage(len(entries)-1), got[1].Message)
			} else {
				require.Len(t, got, 1)
			}

			assert.LessOrEqual(t, entryWeight(first), budget)
		}
	})

	t.Run("one more problem is said so", func(t *testing.T) {
		assert.Equal(t, "1 more problem is not listed", moreProblemsMessage(1))
		assert.Equal(t, "2 more problems are not listed", moreProblemsMessage(2))
	})
}

func TestBoundQuoted(t *testing.T) {
	long := strings.Repeat("x", 5000)

	testcases := []struct {
		name    string
		message string
		want    string
	}{
		{name: "what is quoted fits", message: `property "colour" is unsupported`, want: `property "colour" is unsupported`},
		{name: "what is quoted is cut", message: `property "` + long + `" is unsupported`, want: `property "` + long[:maxQuotedBytes-len(cutMarker)] + cutMarker + `" is unsupported`},
		{name: "a quote inside what is quoted", message: `property "a\"b" is unsupported`, want: `property "a\"b" is unsupported`},
		{name: "another message", message: `property "colour" is missing`, want: `property "colour" is missing`},
		{name: "nothing quoted", message: "property colour is unsupported", want: "property colour is unsupported"},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, boundQuoted(tc.message, "property ", " is unsupported"))
		})
	}
}

// A value that does not parse is reported in kin-openapi's own words, which is
// what a client always read: the message built here is the error's own text as
// long as what the request supplied fits.
func TestParseFailureMessage(t *testing.T) {
	t.Run("a value that fits reads as the error itself", func(t *testing.T) {
		for _, parseErr := range []*openapi3filter.ParseError{
			{Kind: openapi3filter.KindInvalidFormat, Value: "many", Reason: "an invalid integer", Cause: strconv.ErrSyntax},
			{Kind: openapi3filter.KindInvalidFormat, Value: "99999999999999999999", Reason: "an invalid integer", Cause: strconv.ErrRange},
			{Kind: openapi3filter.KindInvalidFormat, Value: "a;b", Reason: `a value must be prefixed with "."`},
			{Kind: openapi3filter.KindOther, Reason: "invalid param object", Value: map[string]any{"a": 1}},
			{Cause: &openapi3filter.ParseError{Kind: openapi3filter.KindInvalidFormat, Value: "maybe", Reason: "an invalid boolean", Cause: strconv.ErrSyntax}},
		} {
			message, ok := parseFailureMessage(parseErr)

			assert.True(t, ok)
			assert.Equal(t, parseErr.Error(), message)
		}
	})

	t.Run("a value that does not fit is cut, and the reason still follows it", func(t *testing.T) {
		message, ok := parseFailureMessage(&openapi3filter.ParseError{
			Kind:   openapi3filter.KindInvalidFormat,
			Value:  strings.Repeat("9", 5000),
			Reason: "an invalid integer",
			Cause:  strconv.ErrRange,
		})

		assert.True(t, ok)
		assert.Equal(t, "value "+strings.Repeat("9", maxQuotedBytes-len(cutMarker))+cutMarker+": an invalid integer: value out of range", message)
	})

	t.Run("a part the form does not declare is named, cut", func(t *testing.T) {
		message, ok := parseFailureMessage(&openapi3filter.ParseError{
			Kind:  openapi3filter.KindOther,
			Cause: fmt.Errorf("part %s: undefined", strings.Repeat("p", 5000)),
		})

		assert.True(t, ok)
		assert.Equal(t, "part "+strings.Repeat("p", maxQuotedBytes-len(cutMarker))+cutMarker+": undefined", message)
	})

	t.Run("a parser's error is not a message", func(t *testing.T) {
		for _, parseErr := range []*openapi3filter.ParseError{
			{Kind: openapi3filter.KindInvalidFormat, Cause: io.ErrUnexpectedEOF},
			{Kind: openapi3filter.KindInvalidFormat, Cause: fmt.Errorf("part %s: undefined", "copies")},
			{Kind: openapi3filter.KindOther, Cause: fmt.Errorf("malformed MIME header line: %q", "part x: undefined")},
			{Cause: &openapi3filter.ParseError{Kind: openapi3filter.KindInvalidFormat, Cause: io.ErrUnexpectedEOF}},
			{},
		} {
			message, ok := parseFailureMessage(parseErr)

			assert.False(t, ok, "%v", parseErr)
			assert.Empty(t, message)
		}
	})
}

// The four validator messages that quote the request are cut to what a refusal
// writes of a name or a value, and read as they always did when it fits.
func TestJSONSchemaMessageCutsWhatTheRequestSupplied(t *testing.T) {
	long := strings.Repeat("z", 5000)
	cut := long[:maxQuotedBytes-len(cutMarker)] + cutMarker

	testcases := []struct {
		name string
		kind jsonschema.ErrorKind
		want string
	}{
		{name: "a property that fits", kind: &kind.AdditionalProperties{Properties: []string{"extra"}}, want: "additional properties 'extra' not allowed"},
		{name: "a property that does not", kind: &kind.AdditionalProperties{Properties: []string{long}}, want: "additional properties '" + cut + "' not allowed"},
		{name: "a property name", kind: &kind.PropertyNames{Property: long}, want: "invalid propertyName '" + cut + "'"},
		{name: "a string a pattern refuses", kind: &kind.Pattern{Got: long, Want: "^[a-z]+$"}, want: "'" + cut + "' does not match pattern '^[a-z]+$'"},
		{name: "a string a pattern refuses, which fits", kind: &kind.Pattern{Got: "Bad", Want: "^[a-z]+$"}, want: "'Bad' does not match pattern '^[a-z]+$'"},
		{name: "a value a format refuses", kind: &kind.Format{Got: long, Want: "date", Err: fmt.Errorf("not a date")}, want: "'" + cut + "' is not valid date: not a date"},
		{name: "a number a format refuses", kind: &kind.Format{Got: 12, Want: "int32", Err: fmt.Errorf("out of range")}, want: "12 is not valid int32: out of range"},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, jsonSchemaMessage(&jsonschema.ValidationError{ErrorKind: tc.kind}))
		})
	}

	t.Run("no more names than a message lists", func(t *testing.T) {
		names := make([]string, 0, 500)
		for i := range 500 {
			names = append(names, fmt.Sprintf("extra_%03d", i))
		}

		message := jsonSchemaMessage(&jsonschema.ValidationError{ErrorKind: &kind.AdditionalProperties{Properties: names}})

		assert.Equal(t, maxRefusalNames, strings.Count(message, "extra_"))
		assert.True(t, strings.HasSuffix(message, fmt.Sprintf("'extra_%03d' and %d more not allowed", maxRefusalNames-1, 500-maxRefusalNames)), message)
	})
}

// formPart is one part of a multipart form: its name and its content.
type formPart struct {
	name  string
	value string
}

// multipartForm writes parts as a multipart form, and returns its content type
// with it.
func multipartForm(t *testing.T, parts ...formPart) (string, io.Reader) {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for _, part := range parts {
		require.NoError(t, writer.WriteField(part.name, part.value))
	}
	require.NoError(t, writer.Close())

	return writer.FormDataContentType(), body
}

// rawMultipartForm writes a multipart form by hand, one part per name, so that a
// name can hold bytes mime/multipart's own writer would escape.
func rawMultipartForm(names ...string) (string, io.Reader) {
	const boundary = "formboundary"

	body := &bytes.Buffer{}
	for _, name := range names {
		fmt.Fprintf(body, "--%s\r\nContent-Disposition: form-data; name=\"%s\"\r\n\r\nx\r\n", boundary, name)
	}
	fmt.Fprintf(body, "--%s--\r\n", boundary)

	return "multipart/form-data; boundary=" + boundary, body
}

// refusalRequest is one request a test sends through a validating router.
type refusalRequest struct {
	target      string
	contentType string
	body        io.Reader
	header      http.Header
}

// newBoundsREST returns a router validating against the events description as
// OpenAPI version (30 or 31) and the uploads description, with a handler behind
// every operation that counts the requests reaching it.
func newBoundsREST(t *testing.T, version string) (*rest, *int) {
	t.Helper()

	api, _ := newTestREST(t, Config{
		OpenAPI: ConfigOpenAPI{
			Enabled:               true,
			Descriptions:          []string{"./testdata/openapi." + version + ".yaml", "./testdata/openapi.bounds.yaml"},
			RefuseUndeclaredQuery: true,
		},
	})

	reached := new(int)
	handler := func(rw http.ResponseWriter, req *http.Request) {
		*reached++
	}

	api.POST("/events/:page", handler)
	api.POST("/uploads", handler)
	api.POST("/subscriptions", handler)
	api.POST("/folders", handler)

	return api, reached
}

// refuse sends request through api and returns what it answered.
func refuse(api *rest, request refusalRequest) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, request.target, request.body)
	maps.Copy(req.Header, request.header)

	if request.contentType != "" {
		req.Header.Set("Content-Type", request.contentType)
	}

	rw := httptest.NewRecorder()
	api.Handler().ServeHTTP(rw, req)

	return rw
}

// validEvent is a body the events description admits.
const validEvent = `{"name":"launch","tags":["a"]}`

// browserMultipart is the media type a browser sends a form with, boundary and
// all: 68 bytes.
const browserMultipart = "multipart/form-data; boundary=----WebKitFormBoundary7MA4YWxkTrZu0gW"

// TestValidation_AnOrdinaryRefusalReadsAsItAlwaysDid pins, byte for byte, what a
// well-formed client is answered for the violations the limits of a refusal sit
// next to: a value that does not parse, a part or a member the description does
// not declare, a media type it does not declare. None of them is long enough to
// be cut, and none is answered differently for the limits being there.
func TestValidation_AnOrdinaryRefusalReadsAsItAlwaysDid(t *testing.T) {
	entry := func(message string, path string) string {
		return `{"errors":[{"message":` + strconv.Quote(message) + `,"path":` + path + `,"extensions":{"code":"VALIDATION_FAILED"}}]}`
	}

	form := func(parts ...formPart) (string, io.Reader) {
		return multipartForm(t, parts...)
	}

	testcases := []struct {
		name    string
		version string
		request func() refusalRequest
		want    string
	}{
		{
			name:    "a path parameter that is no integer",
			version: "31",
			request: func() refusalRequest {
				return refusalRequest{target: "/events/abc", contentType: "application/json", body: strings.NewReader(validEvent)}
			},
			want: entry("value abc: an invalid integer: invalid syntax", `["request","params","page"]`),
		},
		{
			name:    "a query parameter that is no integer",
			version: "31",
			request: func() refusalRequest {
				return refusalRequest{target: "/events/1?limit=abc", contentType: "application/json", body: strings.NewReader(validEvent)}
			},
			want: entry("value abc: an invalid integer: invalid syntax", `["request","query","limit"]`),
		},
		{
			name:    "a query parameter the operation does not declare",
			version: "31",
			request: func() refusalRequest {
				return refusalRequest{target: "/events/1?offset=2", contentType: "application/json", body: strings.NewReader(validEvent)}
			},
			want: entry("Is not a query parameter of this operation", `["request","query","offset"]`),
		},
		{
			name:    "a member the description does not declare, OpenAPI 3.1",
			version: "31",
			request: func() refusalRequest {
				return refusalRequest{target: "/events/1", contentType: "application/json", body: strings.NewReader(`{"name":"launch","tags":[],"extra":true,"colour":"red"}`)}
			},
			want: entry("additional properties 'colour', 'extra' not allowed", `["request","body"]`),
		},
		{
			name:    "a member the description does not declare, OpenAPI 3.0",
			version: "30",
			request: func() refusalRequest {
				return refusalRequest{target: "/events/1", contentType: "application/json", body: strings.NewReader(`{"name":"launch","tags":[],"extra":true}`)}
			},
			want: entry(`property "extra" is unsupported`, `["request","body"]`),
		},
		{
			name:    "a string a pattern refuses",
			version: "31",
			request: func() refusalRequest {
				return refusalRequest{target: "/events/1", contentType: "application/json", body: strings.NewReader(`{"name":"launch","tags":["Bad"]}`)}
			},
			want: entry("'Bad' does not match pattern '^[a-z]+$'", `["request","body","tags",0]`),
		},
		{
			name:    "a media type the operation does not declare",
			version: "31",
			request: func() refusalRequest {
				return refusalRequest{target: "/events/1", contentType: "text/plain", body: strings.NewReader(validEvent)}
			},
			want: entry(`header Content-Type has unexpected value "text/plain"`, `["request","body"]`),
		},
		{
			name:    "a browser's multipart media type the operation does not declare",
			version: "31",
			request: func() refusalRequest {
				return refusalRequest{target: "/events/1", contentType: browserMultipart, body: strings.NewReader(validEvent)}
			},
			want: entry(`header Content-Type has unexpected value "`+browserMultipart+`"`, `["request","body"]`),
		},
		{
			name:    "a string a pattern refuses, one byte past a bound of 2,048",
			version: "31",
			request: func() refusalRequest {
				return refusalRequest{target: "/events/1", contentType: "application/json", body: strings.NewReader(`{"name":"launch","tags":["` + strings.Repeat("Z", 2049) + `"]}`)}
			},
			want: entry("'"+strings.Repeat("Z", 2049)+"' does not match pattern '^[a-z]+$'", `["request","body","tags",0]`),
		},
		{
			name:    "a body that is required and missing",
			version: "31",
			request: func() refusalRequest {
				return refusalRequest{target: "/events/1", contentType: "application/json", body: http.NoBody}
			},
			want: entry("value is required but missing", `["request","body"]`),
		},
		{
			name:    "a part the form does not declare",
			version: "31",
			request: func() refusalRequest {
				contentType, body := form(formPart{"kind", "report"}, formPart{"file", "content"}, formPart{"knid", "report"})
				return refusalRequest{target: "/uploads", contentType: contentType, body: body}
			},
			want: entry("part knid: undefined", `["request","body"]`),
		},
		{
			name:    "a part that is no integer",
			version: "31",
			request: func() refusalRequest {
				contentType, body := form(formPart{"kind", "report"}, formPart{"file", "content"}, formPart{"copies", "many"})
				return refusalRequest{target: "/uploads", contentType: contentType, body: body}
			},
			want: entry("path copies: value many: an invalid integer: invalid syntax", `["request","body"]`),
		},
		{
			name:    "a part outside its enum",
			version: "31",
			request: func() refusalRequest {
				contentType, body := form(formPart{"kind", "song"}, formPart{"file", "content"})
				return refusalRequest{target: "/uploads", contentType: contentType, body: body}
			},
			want: entry("value must be one of 'report', 'picture'", `["request","body","kind"]`),
		},
		{
			name:    "a form field that is no boolean",
			version: "31",
			request: func() refusalRequest {
				return refusalRequest{target: "/subscriptions", contentType: "application/x-www-form-urlencoded", body: strings.NewReader("topic=releases&weekly=sometimes")}
			},
			want: entry("path weekly: value sometimes: an invalid boolean: invalid syntax", `["request","body"]`),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			api, reached := newBoundsREST(t, tc.version)

			rw := refuse(api, tc.request())

			assert.Equal(t, http.StatusBadRequest, rw.Code)
			assert.Equal(t, tc.want, rw.Body.String())
			assert.Zero(t, *reached, "the handler ran for a refused request")
		})
	}
}

// hostileRequests are requests built to make a refusal quote them, each at a size
// its caller chooses: the size is how long the name or the value is, or how many
// there are of it.
func hostileRequests(t *testing.T) map[string]func(size int) refusalRequest {
	t.Helper()

	event := func(body string) refusalRequest {
		return refusalRequest{target: "/events/1", contentType: "application/json", body: strings.NewReader(body)}
	}

	return map[string]func(size int) refusalRequest{
		"an undeclared part with a long name": func(size int) refusalRequest {
			contentType, body := multipartForm(t, formPart{"kind", "report"}, formPart{"file", "content"}, formPart{"n" + strings.Repeat("é", size), "x"})
			return refusalRequest{target: "/uploads", contentType: contentType, body: body}
		},
		"a part header of control characters": func(size int) refusalRequest {
			contentType, body := rawMultipartForm(strings.Repeat("\x01", size))
			return refusalRequest{target: "/uploads", contentType: contentType, body: body}
		},
		"many undeclared parts": func(size int) refusalRequest {
			parts := []formPart{{"kind", "report"}, {"file", "content"}}
			for i := range size / 16 {
				parts = append(parts, formPart{fmt.Sprintf("undeclared_%06d", i), "x"})
			}

			contentType, body := multipartForm(t, parts...)
			return refusalRequest{target: "/uploads", contentType: contentType, body: body}
		},
		"an integer part with a long value": func(size int) refusalRequest {
			contentType, body := multipartForm(t, formPart{"kind", "report"}, formPart{"file", "content"}, formPart{"copies", strings.Repeat("9", size)})
			return refusalRequest{target: "/uploads", contentType: contentType, body: body}
		},
		"an undeclared member with a long name": func(size int) refusalRequest {
			return event(`{"name":"launch","tags":[],"` + strings.Repeat("k", size) + `":1}`)
		},
		"many undeclared members": func(size int) refusalRequest {
			var members strings.Builder
			for i := range size / 16 {
				fmt.Fprintf(&members, `,"extra_%06d":1`, i)
			}

			return event(`{"name":"launch","tags":[]` + members.String() + `}`)
		},
		"a long string a pattern refuses": func(size int) refusalRequest {
			return event(`{"name":"launch","tags":["` + strings.Repeat("Z", size) + `"]}`)
		},
		"many values that fail": func(size int) refusalRequest {
			return event(`{"name":"launch","tags":[` + strings.TrimSuffix(strings.Repeat("1,", size/2), ",") + `]}`)
		},
		"a long query value that is no integer": func(size int) refusalRequest {
			return refusalRequest{target: "/events/1?limit=" + strings.Repeat("9", size), contentType: "application/json", body: strings.NewReader(validEvent)}
		},
		"an undeclared query parameter with a long name": func(size int) refusalRequest {
			return refusalRequest{target: "/events/1?" + strings.Repeat("q", size) + "=1", contentType: "application/json", body: strings.NewReader(validEvent)}
		},
		"many undeclared query parameters": func(size int) refusalRequest {
			var query strings.Builder
			for i := range size / 16 {
				fmt.Fprintf(&query, "&q%06d=1", i)
			}

			return refusalRequest{target: "/events/1?limit=1" + query.String(), contentType: "application/json", body: strings.NewReader(validEvent)}
		},
		"a long media type": func(size int) refusalRequest {
			return refusalRequest{target: "/events/1", contentType: "text/" + strings.Repeat("x", size), body: strings.NewReader(validEvent)}
		},
		"a long body that does not parse": func(size int) refusalRequest {
			return event(`{"name":"` + strings.Repeat("a", size))
		},
		"a long form that does not decode": func(size int) refusalRequest {
			return refusalRequest{target: "/subscriptions", contentType: "application/x-www-form-urlencoded", body: strings.NewReader("topic=" + strings.Repeat("%zz", size/3))}
		},
	}
}

// refusalCeiling is more than a refusal weighs: its entries' budget and the
// envelope around them.
const refusalCeiling = maxRefusalBytes + 1<<10

// hostileSize is the size of a request built to be quoted back: enough that the
// problems it holds outweigh the bound, each one of them too, so that a request
// sixteen times its size is answered the same.
const hostileSize = 16 << 10

// leftOutCount matches the counts a refusal gives of what it leaves out: the
// problems it does not list and the names a message does not, the one thing of it
// that grows with the request.
var leftOutCount = regexp.MustCompile(`\d+ more (problems? (are|is) not listed|not allowed)`)

// TestValidation_ARefusalDoesNotGrowWithTheRequest pins that what a client is
// answered for a request the description refuses is bounded by the limits of a
// refusal and not by the request: each request built to be quoted back is
// answered the very same bytes at sixteen times its size, under both validators,
// but for how many problems it says are not listed. A refusal used to carry a
// part's name whole, a malformed header line quoted several times over, and an
// entry per failing value however many there were.
func TestValidation_ARefusalDoesNotGrowWithTheRequest(t *testing.T) {
	for _, version := range []string{"30", "31"} {
		for name, build := range hostileRequests(t) {
			t.Run(version+"/"+name, func(t *testing.T) {
				api, reached := newBoundsREST(t, version)

				small := refuse(api, build(hostileSize))
				large := refuse(api, build(128<<10))

				require.Equal(t, http.StatusBadRequest, small.Code, small.Body.String())
				require.Equal(t, http.StatusBadRequest, large.Code)

				masked := func(body string) string {
					return leftOutCount.ReplaceAllString(body, "N more")
				}
				assert.Equal(t, masked(small.Body.String()), masked(large.Body.String()), "the refusal changed with the size of the request")
				assert.LessOrEqual(t, large.Body.Len(), refusalCeiling)
				assert.True(t, utf8.Valid(large.Body.Bytes()))
				assert.Zero(t, *reached, "the handler ran for a refused request")
			})
		}
	}
}

// TestValidation_ARefusalCutsWhatItQuotes pins what the limits leave of the
// requests above: a name or a value cut to maxQuotedBytes on a character and
// marked, the names of one message and the entries of one refusal counted, and a
// body that does not decode answered with one fixed sentence.
func TestValidation_ARefusalCutsWhatItQuotes(t *testing.T) {
	requests := hostileRequests(t)

	entries := func(t *testing.T, version, name string) validationRefusal {
		t.Helper()

		api, _ := newBoundsREST(t, version)
		rw := refuse(api, requests[name](hostileSize))
		require.Equal(t, http.StatusBadRequest, rw.Code, rw.Body.String())

		var refusal validationRefusal
		require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &refusal), rw.Body.String())

		return refusal
	}

	one := func(t *testing.T, version, name string) (string, []any) {
		t.Helper()

		refusal := entries(t, version, name)
		require.Len(t, refusal.Errors, 1)

		return refusal.Errors[0].Message, refusal.Errors[0].Path
	}

	// counted pins a refusal of size entries, hostileSize bytes' worth: the
	// first ones listed, in order, then one entry with no path counting the rest.
	counted := func(t *testing.T, refusal validationRefusal, size int) {
		t.Helper()

		require.Greater(t, len(refusal.Errors), 1)

		last := refusal.Errors[len(refusal.Errors)-1]
		listed := len(refusal.Errors) - 1
		assert.Equal(t, moreProblemsMessage(size-listed), last.Message)
		assert.Nil(t, last.Path)
	}

	quoted := func(repeated string) string {
		return strings.Repeat(repeated, maxQuotedBytes-len(cutMarker)) + cutMarker
	}

	body := []any{"request", "body"}

	t.Run("a part's name is cut on a character, and marked", func(t *testing.T) {
		message, path := one(t, "31", "an undeclared part with a long name")

		// The name is one byte, then two-byte characters: the last one that fits
		// before the marker ends at byte 4,093.
		assert.Equal(t, "part n"+strings.Repeat("é", (maxQuotedBytes-len(cutMarker)-1)/2)+cutMarker+": undefined", message)
		assert.Equal(t, body, path)
	})

	t.Run("the first undeclared part is the one named", func(t *testing.T) {
		message, _ := one(t, "31", "many undeclared parts")

		assert.Equal(t, "part undeclared_000000: undefined", message)
	})

	t.Run("a part's value is cut, and what is wrong with it still said", func(t *testing.T) {
		message, _ := one(t, "31", "an integer part with a long value")

		assert.Equal(t, "path copies: value "+quoted("9")+": an invalid integer: value out of range", message)
	})

	t.Run("a member's name is cut, OpenAPI 3.1", func(t *testing.T) {
		message, _ := one(t, "31", "an undeclared member with a long name")

		assert.Equal(t, "additional properties '"+quoted("k")+"' not allowed", message)
	})

	t.Run("a member's name is cut, OpenAPI 3.0", func(t *testing.T) {
		message, _ := one(t, "30", "an undeclared member with a long name")

		assert.Equal(t, `property "`+quoted("k")+`" is unsupported`, message)
	})

	t.Run("one message lists so many names, and how many more, OpenAPI 3.1", func(t *testing.T) {
		message, _ := one(t, "31", "many undeclared members")

		assert.Equal(t, maxRefusalNames, strings.Count(message, "extra_"))
		assert.True(t, strings.HasSuffix(message, fmt.Sprintf(" and %d more not allowed", hostileSize/16-maxRefusalNames)), message)
	})

	t.Run("one refusal lists so many entries, and counts the rest, OpenAPI 3.0", func(t *testing.T) {
		counted(t, entries(t, "30", "many undeclared members"), hostileSize/16)
	})

	t.Run("a string a pattern refuses is cut", func(t *testing.T) {
		message, _ := one(t, "31", "a long string a pattern refuses")

		assert.Equal(t, "'"+quoted("Z")+"' does not match pattern '^[a-z]+$'", message)
	})

	t.Run("one refusal lists so many entries, in the order of their paths", func(t *testing.T) {
		refusal := entries(t, "31", "many values that fail")

		counted(t, refusal, hostileSize/2)
		assert.Equal(t, []any{"request", "body", "tags", float64(0)}, refusal.Errors[0].Path)

		listed := len(refusal.Errors) - 1
		assert.Equal(t, []any{"request", "body", "tags", float64(listed - 1)}, refusal.Errors[listed-1].Path)
	})

	t.Run("a query value is cut", func(t *testing.T) {
		message, path := one(t, "31", "a long query value that is no integer")

		assert.Equal(t, "value "+quoted("9")+": an invalid integer: value out of range", message)
		assert.Equal(t, []any{"request", "query", "limit"}, path)
	})

	t.Run("a query parameter's name is cut in its path", func(t *testing.T) {
		message, path := one(t, "31", "an undeclared query parameter with a long name")

		assert.Equal(t, undeclaredQueryMessage, message)
		assert.Equal(t, []any{"request", "query", quoted("q")}, path)
	})

	t.Run("one refusal lists so many undeclared query parameters, and counts the rest", func(t *testing.T) {
		counted(t, entries(t, "31", "many undeclared query parameters"), hostileSize/16)
	})

	t.Run("a media type is cut", func(t *testing.T) {
		message, _ := one(t, "31", "a long media type")

		assert.Equal(t, `header Content-Type has unexpected value "text/`+strings.Repeat("x", maxQuotedBytes-len(cutMarker)-len("text/"))+cutMarker+`"`, message)
	})

	for _, name := range []string{"a part header of control characters", "a long body that does not parse", "a long form that does not decode"} {
		t.Run(name+" is answered one fixed sentence", func(t *testing.T) {
			message, path := one(t, "31", name)

			assert.Equal(t, undecodableBodyMessage, message)
			assert.Equal(t, body, path)
		})
	}
}

// TestValidation_ARefusalLocatesADeepValueByItsFirstSegments pins the limit on a
// path: a value nested deeper than maxRefusalPathSegments is located by the first
// segments of its path, the value it sits in, so a body nested ten times deeper is
// answered the same bytes.
func TestValidation_ARefusalLocatesADeepValueByItsFirstSegments(t *testing.T) {
	api, reached := newBoundsREST(t, "31")

	// A folder inside a folder, depth times over, the innermost with a name that
	// is no string.
	nested := func(depth int) refusalRequest {
		body := strings.Repeat(`{"name":"folder","folders":[`, depth) + `{"name":1}` + strings.Repeat("]}", depth)
		return refusalRequest{target: "/folders", contentType: "application/json", body: strings.NewReader(body)}
	}

	shallow, deep := refuse(api, nested(100)), refuse(api, nested(1000))

	require.Equal(t, http.StatusBadRequest, shallow.Code, shallow.Body.String())
	assert.Equal(t, shallow.Body.String(), deep.Body.String(), "the refusal changed with the depth of the request")
	assert.Zero(t, *reached, "the handler ran for a refused request")

	var refusal validationRefusal
	require.NoError(t, json.Unmarshal(deep.Body.Bytes(), &refusal), deep.Body.String())
	require.Len(t, refusal.Errors, 1)

	want := []any{"request", "body"}
	for len(want) < maxRefusalPathSegments-1 {
		want = append(want, "folders", float64(0))
	}
	want = append(want[:maxRefusalPathSegments-1], cutMarker)
	assert.Equal(t, want, refusal.Errors[0].Path)
}

// TestValidation_AMalformedRequestIsNeverA500 pins that a name or a value the
// client spelled in bytes that are not UTF-8 is refused as any other request the
// description refuses. The envelope does not marshal such a string, so the refusal
// used to be answered as a failure of the server.
func TestValidation_AMalformedRequestIsNeverA500(t *testing.T) {
	form := func(name string) refusalRequest {
		contentType, body := rawMultipartForm(name)
		return refusalRequest{target: "/uploads", contentType: contentType, body: body}
	}

	testcases := []struct {
		name    string
		request refusalRequest
		message string
		path    []any
	}{
		{
			name:    "a part's name",
			request: form("\xff\xfe"),
			message: "part �: undefined",
			path:    []any{"request", "body"},
		},
		{
			name:    "a long part's name",
			request: form(strings.Repeat("\xff", 200)),
			message: "part �: undefined",
			path:    []any{"request", "body"},
		},
		{
			name:    "a part's name cut where a character would straddle the limit",
			request: form(strings.Repeat("a", maxQuotedBytes-len(cutMarker)-1) + "é" + "bbbb"),
			message: "part " + strings.Repeat("a", maxQuotedBytes-len(cutMarker)-1) + cutMarker + ": undefined",
			path:    []any{"request", "body"},
		},
		{
			name:    "an undeclared query parameter's name",
			request: refusalRequest{target: "/events/1?%ff%fe=1", contentType: "application/json", body: strings.NewReader(validEvent)},
			message: undeclaredQueryMessage,
			path:    []any{"request", "query", "�"},
		},
		{
			name:    "a path parameter's value",
			request: refusalRequest{target: "/events/%ff", contentType: "application/json", body: strings.NewReader(validEvent)},
			message: "value �: an invalid integer: invalid syntax",
			path:    []any{"request", "params", "page"},
		},
		{
			name:    "a header's value",
			request: refusalRequest{target: "/events/1", contentType: "text/\xff", body: strings.NewReader(validEvent)},
			message: `header Content-Type has unexpected value "text/�"`,
			path:    []any{"request", "body"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			api, reached := newBoundsREST(t, "31")

			rw := refuse(api, tc.request)

			require.Equal(t, http.StatusBadRequest, rw.Code, rw.Body.String())
			require.True(t, utf8.Valid(rw.Body.Bytes()))

			var refusal validationRefusal
			require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &refusal), rw.Body.String())
			require.Len(t, refusal.Errors, 1)
			assert.Equal(t, tc.message, refusal.Errors[0].Message)
			assert.Equal(t, tc.path, refusal.Errors[0].Path)
			assert.Zero(t, *reached, "the handler ran for a refused request")
		})
	}
}

// The parser's error a client is no longer told is still what the request's
// trace records the refusal with.
func TestValidation_AnUndecodableBodyKeepsItsErrorOnTheTrace(t *testing.T) {
	api, rec := newTestREST(t, Config{
		OpenAPI: ConfigOpenAPI{Enabled: true, Descriptions: []string{"./testdata/openapi.31.yaml"}},
	})
	api.POST("/events/:page", func(rw http.ResponseWriter, req *http.Request) {})

	rw := refuse(api, refusalRequest{target: "/events/1", contentType: "application/json", body: strings.NewReader(`{"name":`)})

	require.Equal(t, http.StatusBadRequest, rw.Code)
	assert.JSONEq(t, `{"errors":[{"message":"Cannot be decoded as its content type","path":["request","body"],"extensions":{"code":"VALIDATION_FAILED"}}]}`, rw.Body.String())

	span := spanNamed(t, rec, "REST: OpenAPI / Request Validation")
	assert.Equal(t, codes.Error, span.Status().Code)

	var recorded string
	for _, event := range span.Events() {
		for _, attr := range event.Attributes {
			if attr.Key == "exception.message" {
				recorded = attr.Value.AsString()
			}
		}
	}

	assert.Contains(t, recorded, "unexpected EOF")
}
