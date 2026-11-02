package rest

import (
	"encoding/json/v2"
	"maps"
	"net/http"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/internal/locales"
)

var _ json.Marshaler = (*ResponseError[any])(nil)

var fallbackErrorResponse = []byte(`{"errors":[{"message":"Internal server error","extensions":{"code":"INTERNAL_ERROR"}}]}`)

// ResponseError builds a GraphQL-spec error response for HTTP status codes 4xx and
// 5xx. The serialized envelope is:
//
//	{"errors":[{"message":"…","path":[…],"extensions":{"code":"…"}}], "extensions":{…}}
//
// SetStatus seeds a single fallback entry from the status. SetValidations and
// SetEntries supersede it, replacing errors[] with the entries they are given.
// SetMetadata folds the typed metadata under top-level extensions.metadata.
type ResponseError[Metadata any] struct {
	request    *http.Request
	statusCode int
	err        *errorstack.Error
	metadata   *Metadata
}

// NewResponseError creates a new HTTP response for status codes 4xx and 5xx. The
// status code defaults to http.StatusInternalServerError so a caller that forgets
// to call SetStatus still produces a valid HTTP error response (the seeded
// fallback entry uses CodeInternalError, matching).
//
// A 3xx is not an error and has no envelope: answer a redirect with
// http.Redirect.
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

// SetStatus sets the response's status code and seeds a single fallback entry
// built from the status' localized message and canonical code. SetValidations or
// SetEntries later replaces this entry when called, so call SetStatus first.
//
// The statuses with a localized message and a code of their own are the ones
// AddOrEditLanguage lists. Any other is answered with net/http's English text for
// it, under INTERNAL_ERROR; use SetEntries to give it a message and code of its
// own.
func (res *ResponseError[Metadata]) SetStatus(status int) *ResponseError[Metadata] {
	message := locales.Message(res.request, status)
	if message == "" {
		message = http.StatusText(status)
	}

	if message == "" {
		message = locales.Message(res.request, http.StatusInternalServerError)
	}

	res.statusCode = status
	res.err = errorstack.New(message, errorstack.WithCode(errorstack.HTTPStatusToCode(status)))

	return res
}

// SetMetadata sets the typed metadata folded under top-level extensions.metadata.
func (res *ResponseError[Metadata]) SetMetadata(metadata Metadata) *ResponseError[Metadata] {
	res.metadata = &metadata
	return res
}

// SetValidations replaces errors[] with one entry per validation. Each entry is
// forced to extensions.code = VALIDATION_FAILED. The seeded fallback entry from
// SetStatus is dropped per the GraphQL spec mapping rule "validations supersede
// the root entry".
func (res *ResponseError[Metadata]) SetValidations(entries ...errorstack.Entry) *ResponseError[Metadata] {
	res.err = errorstack.NewValidation(entries...)
	return res
}

// SetEntries replaces errors[] with the given entries, each keeping its own
// message, code and path. An entry without a code gets the one the response's
// status maps to, so call SetStatus first. Without entries it does nothing, and
// the seeded fallback entry stands.
//
// It is the counterpart to SetValidations for entries that are not validations: a
// message more specific than the status' own, under the status' code or a code of
// the application's. Forcing them to VALIDATION_FAILED would say the caller sent
// something malformed.
//
//	rest.NewResponseError[rest.NoMetadata](req).
//	  SetStatus(http.StatusConflict).
//	  SetEntries(errorstack.Entry{
//	    Message: "Email address is already registered",
//	    Path:    []any{"request", "body", "email"},
//	  }).
//	  Write(rw)
func (res *ResponseError[Metadata]) SetEntries(entries ...errorstack.Entry) *ResponseError[Metadata] {
	if len(entries) == 0 {
		return res
	}

	code := errorstack.HTTPStatusToCode(res.statusCode)
	out := make([]errorstack.Entry, len(entries))
	for i, entry := range entries {
		// A new map rather than a write into the entry's own: the map is shared with
		// the caller, who may be reusing it across entries or responses.
		if _, ok := entry.Extensions["code"]; !ok {
			extensions := make(map[string]any, len(entry.Extensions)+1)
			maps.Copy(extensions, entry.Extensions)
			extensions["code"] = code
			entry.Extensions = extensions
		}

		out[i] = entry
	}

	res.err = &errorstack.Error{Entries: out}
	return res
}

// Write writes the ResponseError to the ResponseWriter. Falls back to a constant
// INTERNAL_ERROR envelope if marshaling fails.
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

// MarshalJSON serializes the response into the GraphQL-spec envelope, folding the
// typed metadata into top-level extensions.metadata when present. Entries are
// routed through errorstack.Error.MarshalJSON so the schema invariant
// (extensions.code on every entry, errors[] non-empty) holds even for entries
// appended without going through the public constructors.
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

// cloneErrorWithMetadata returns a shallow clone of err with res.metadata folded
// under top-level extensions.metadata. The clone avoids mutating the caller's
// *errorstack.Error (callers typically retain it via SetValidations chaining).
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
