package rest

import (
	"encoding/json/v2"
	"net/http"

	"github.com/mountayaapp/helix.go/telemetry/trace"

	oteltrace "go.opentelemetry.io/otel/trace"
)

var _ json.Marshaler = (*ResponseSuccess[any, any])(nil)

// NoMetadata is the Metadata type argument of a response that carries no
// metadata, in ResponseSuccess and ResponseError alike: no "extensions" object is
// written.
type NoMetadata struct{}

// NoData is the Data type argument of a ResponseSuccess that carries no payload:
// "data" is written as null.
type NoData struct{}

// Response is the wire-format envelope for 2xx HTTP responses produced by
// ResponseSuccess[Metadata, Data].MarshalJSON. Non-generic so OpenAPI specs
// can reference it via x-go-type without specializing the generic. The "data"
// field is always present (serialized as null when no payload was set) so
// consumers can rely on its presence; "extensions" is omitted when empty.
type Response struct {
	Data       any            `json:"data"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// ResponseSuccess builds the envelope of a 2xx response:
//
//	{"data":{…},"extensions":{"metadata":{…}}}
type ResponseSuccess[Metadata any, Data any] struct {
	request    *http.Request
	statusCode int
	metadata   *Metadata
	data       *Data
}

// responseSuccessJSON is the JSON representation of ResponseSuccess when marshaled.
// Mirrors responseErrorJSON: typed metadata is folded under extensions.metadata
// so the wire shape is uniform across 2xx and non-2xx envelopes. "data" is always
// serialized (as null when no payload was set) so consumers can rely on the
// field's presence.
type responseSuccessJSON[Data any] struct {
	Data       *Data          `json:"data"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// NewResponseSuccess creates a new HTTP response for status codes 2xx. The status
// code defaults to http.StatusOK, so a caller that does not call SetStatus still
// writes a valid response.
func NewResponseSuccess[Metadata any, Data any](req *http.Request) *ResponseSuccess[Metadata, Data] {
	return &ResponseSuccess[Metadata, Data]{
		request:    req,
		statusCode: http.StatusOK,
	}
}

// SetStatus sets the response's HTTP status code, which should be a 2xx.
func (res *ResponseSuccess[Metadata, Data]) SetStatus(status int) *ResponseSuccess[Metadata, Data] {
	res.statusCode = status

	return res
}

// SetMetadata sets the typed metadata folded under top-level extensions.metadata.
func (res *ResponseSuccess[Metadata, Data]) SetMetadata(metadata Metadata) *ResponseSuccess[Metadata, Data] {
	res.metadata = &metadata

	return res
}

// SetData sets the "data" object of the response body.
func (res *ResponseSuccess[Metadata, Data]) SetData(data Data) *ResponseSuccess[Metadata, Data] {
	res.data = &data

	return res
}

// Write writes the ResponseSuccess to the ResponseWriter.
//
// A payload that cannot be marshaled (a NaN, invalid UTF-8 in a string, a value
// whose own MarshalJSON fails) is a failure of the server, not a success with
// nothing in it: it is answered as a 500 with the error envelope every 500 is
// described by, rather than a success-shaped body a client would find no error
// code in, and the marshal error is recorded on the request's span.
func (res *ResponseSuccess[Metadata, Data]) Write(rw http.ResponseWriter) {
	b, err := json.Marshal(res)
	if err != nil {
		if res.request != nil {
			trace.NewSpan(oteltrace.SpanFromContext(res.request.Context())).RecordError("failed to marshal response", err)
		}

		NewResponseError[NoMetadata](res.request).
			SetStatus(http.StatusInternalServerError).
			Write(rw)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(res.statusCode)
	rw.Write(b)
}

// MarshalJSON serializes the response into the GraphQL-spec envelope, folding
// the typed metadata under top-level extensions.metadata when present.
func (res *ResponseSuccess[Metadata, Data]) MarshalJSON() ([]byte, error) {
	envelope := responseSuccessJSON[Data]{Data: res.data}

	if res.metadata != nil {
		envelope.Extensions = map[string]any{"metadata": res.metadata}
	}

	return json.Marshal(envelope)
}
