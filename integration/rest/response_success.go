package rest

import (
	"encoding/json"
	"net/http"
)

var _ json.Marshaler = (*ResponseSuccess[any, any])(nil)

var fallbackSuccessResponse = []byte(`{"data":null}`)

/*
Response is the wire-format envelope for 2xx HTTP responses produced by
ResponseSuccess[Metadata, Data].MarshalJSON. Non-generic so OpenAPI specs
can reference it via x-go-type without specializing the generic. The "data"
field is always present (serialized as null when no payload was set) so
consumers can rely on its presence; "extensions" is omitted when empty.
*/
type Response struct {
	Data       any            `json:"data"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

/*
ResponseSuccess is the JSON object every HTTP responses shall return.
*/
type ResponseSuccess[Metadata any, Data any] struct {
	request    *http.Request
	statusCode int
	metadata   *Metadata
	data       *Data
}

/*
responseSuccessJSON is the JSON representation of ResponseSuccess when marshaled.
Mirrors responseErrorJSON: typed metadata is folded under extensions.metadata
so the wire shape is uniform across 2xx and non-2xx envelopes. "data" is always
serialized (as null when no payload was set) so consumers can rely on the
field's presence.
*/
type responseSuccessJSON[Data any] struct {
	Data       *Data          `json:"data"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

/*
NewResponseSuccess creates a new HTTP response for status codes 2xx.
*/
func NewResponseSuccess[Metadata any, Data any](req *http.Request) *ResponseSuccess[Metadata, Data] {
	return &ResponseSuccess[Metadata, Data]{
		request: req,
	}
}

/*
SetStatus sets the response's HTTP status code.
*/
func (res *ResponseSuccess[Metadata, Data]) SetStatus(status int) *ResponseSuccess[Metadata, Data] {
	res.statusCode = status

	return res
}

/*
SetMetadata sets the typed metadata folded under top-level extensions.metadata.
*/
func (res *ResponseSuccess[Metadata, Data]) SetMetadata(metadata Metadata) *ResponseSuccess[Metadata, Data] {
	res.metadata = &metadata

	return res
}

/*
SetData sets the "data" object of the response body.
*/
func (res *ResponseSuccess[Metadata, Data]) SetData(data Data) *ResponseSuccess[Metadata, Data] {
	res.data = &data

	return res
}

/*
Write writes the ResponseSuccess to the ResponseWriter.
*/
func (res *ResponseSuccess[Metadata, Data]) Write(rw http.ResponseWriter) {
	b, err := json.Marshal(res)
	if err != nil {
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusInternalServerError)
		rw.Write(fallbackSuccessResponse)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(res.statusCode)
	rw.Write(b)
}

/*
MarshalJSON serializes the response into the GraphQL-spec envelope, folding
the typed metadata under top-level extensions.metadata when present.
*/
func (res *ResponseSuccess[Metadata, Data]) MarshalJSON() ([]byte, error) {
	envelope := responseSuccessJSON[Data]{Data: res.data}

	if res.metadata != nil {
		envelope.Extensions = map[string]any{"metadata": res.metadata}
	}

	return json.Marshal(envelope)
}
