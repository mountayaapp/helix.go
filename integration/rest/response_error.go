package rest

import (
	"encoding/json"
	"maps"
	"net/http"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/internal/locales"
)

var _ json.Marshaler = (*ResponseError[any])(nil)

var fallbackErrorResponse = []byte(`{"errors":[{"message":"Internal server error","extensions":{"code":"INTERNAL_ERROR"}}]}`)

/*
ResponseError builds a GraphQL-spec error response for HTTP status codes 3xx,
4xx, and 5xx. The serialized envelope is:

	{"errors":[{"message":"…","path":[…],"extensions":{"code":"…"}}], "extensions":{…}}

Validations supersede the seeded fallback entry: calling SetValidations
replaces errors[] with one entry per validation. SetMetadata folds the typed
metadata under top-level extensions.metadata.
*/
type ResponseError[Metadata any] struct {
	request    *http.Request
	statusCode int
	err        *errorstack.Error
	metadata   *Metadata
}

/*
NewResponseError creates a new HTTP response for status codes 3xx, 4xx, and
5xx. The status code defaults to http.StatusInternalServerError so a caller
that forgets to call SetStatus still produces a valid HTTP error response
(the seeded fallback entry uses CodeInternalError, matching).
*/
func NewResponseError[Metadata any](req *http.Request) *ResponseError[Metadata] {
	return &ResponseError[Metadata]{
		request:    req,
		statusCode: http.StatusInternalServerError,
		err: errorstack.New(
			locales.Message(req, http.StatusInternalServerError),
			errorstack.WithCode(errorstack.CodeInternalError),
		),
	}
}

/*
SetStatus sets the response's status code and seeds a single fallback entry
built from the status' localized message and canonical code. SetValidations
later replaces this entry when called.
*/
func (res *ResponseError[Metadata]) SetStatus(status int) *ResponseError[Metadata] {
	res.statusCode = status
	res.err = errorstack.New(
		locales.Message(res.request, status),
		errorstack.WithCode(errorstack.HTTPStatusToCode(status)),
	)

	return res
}

/*
SetMetadata sets the typed metadata folded under top-level extensions.metadata.
*/
func (res *ResponseError[Metadata]) SetMetadata(metadata Metadata) *ResponseError[Metadata] {
	res.metadata = &metadata
	return res
}

/*
SetValidations replaces errors[] with one entry per validation. Each entry is
forced to extensions.code = VALIDATION_FAILED. The seeded fallback entry from
SetStatus is dropped per the GraphQL spec mapping rule "validations supersede
the root entry".
*/
func (res *ResponseError[Metadata]) SetValidations(entries ...errorstack.Entry) *ResponseError[Metadata] {
	res.err = errorstack.NewValidation(entries...)
	return res
}

/*
Write writes the ResponseError to the ResponseWriter. Falls back to a constant
INTERNAL_ERROR envelope if marshaling fails.
*/
func (res *ResponseError[Metadata]) Write(rw http.ResponseWriter) {
	b, err := json.Marshal(res)
	if err != nil {
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusInternalServerError)
		rw.Write(fallbackErrorResponse)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(res.statusCode)
	rw.Write(b)
}

/*
MarshalJSON serializes the response into the GraphQL-spec envelope, folding the
typed metadata into top-level extensions.metadata when present. Entries are
routed through errorstack.Error.MarshalJSON so the schema invariant
(extensions.code on every entry, errors[] non-empty) holds even for entries
appended without going through the public constructors.
*/
func (res *ResponseError[Metadata]) MarshalJSON() ([]byte, error) {
	stack := res.err
	if stack == nil {
		stack = errorstack.New(
			"Internal server error",
			errorstack.WithCode(errorstack.CodeInternalError),
		)
	}

	if res.metadata != nil {
		stack = cloneErrorWithMetadata(stack, res.metadata)
	}

	return stack.MarshalJSON()
}

/*
cloneErrorWithMetadata returns a shallow clone of err with res.metadata folded
under top-level extensions.metadata. The clone avoids mutating the caller's
*errorstack.Error (callers typically retain it via SetValidations chaining).
*/
func cloneErrorWithMetadata(err *errorstack.Error, metadata any) *errorstack.Error {
	clone := &errorstack.Error{
		Entries:    err.Entries,
		Extensions: maps.Clone(err.Extensions),
	}
	if clone.Extensions == nil {
		clone.Extensions = map[string]any{}
	}
	clone.Extensions["metadata"] = metadata
	return clone
}
