package rest

import (
	"encoding/json"
	"net/http"

	"github.com/mountayaapp/helix.go/errorstack"
)

var _ json.Marshaler = (*ResponseError[any])(nil)

/*
ResponseError is the JSON object every HTTP responses shall return.
*/
type ResponseError[Metadata any] struct {
	request    *http.Request
	statusCode int
	metadata   *Metadata
	errorstack *errorstack.Error
}

/*
responseErrorJSON is the JSON representation of ResponseError when marshaled.
*/
type responseErrorJSON[Metadata any] struct {
	Status   string            `json:"status"`
	Error    *errorstack.Error `json:"error,omitempty"`
	Metadata *Metadata         `json:"metadata,omitempty"`
}

/*
NewResponseError creates a new HTTP response for status codes 3xx, 4xx, and 5xx.
*/
func NewResponseError[Metadata any](req *http.Request) *ResponseError[Metadata] {
	return &ResponseError[Metadata]{
		request:    req,
		errorstack: &errorstack.Error{},
	}
}

/*
SetStatus sets the response's status code.
*/
func (res *ResponseError[Metadata]) SetStatus(status int) *ResponseError[Metadata] {
	res.statusCode = status

	if res.errorstack != nil && res.errorstack.Message == "" {
		res.errorstack = errorstack.New(supportedLocales[getPreferredLanguage(res.request)][status])
	}

	return res
}

/*
SetMetadata sets the "metadata" object of the response body.
*/
func (res *ResponseError[Metadata]) SetMetadata(metadata Metadata) *ResponseError[Metadata] {
	res.metadata = &metadata

	return res
}

/*
SetErrorValidations sets error validations in the "error" object of the response.
*/
func (res *ResponseError[Metadata]) SetErrorValidations(validations []errorstack.Validation) *ResponseError[Metadata] {
	if res.errorstack != nil {
		res.errorstack.Validations = validations
	}

	return res
}

/*
Write writes the ResponseError to the ResponseWriter.
*/
func (res *ResponseError[Metadata]) Write(rw http.ResponseWriter) {
	b, err := json.Marshal(res)
	if err != nil {
		NewResponseError[NoMetadata](res.request).
			SetStatus(http.StatusInternalServerError).
			Write(rw)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(res.statusCode)
	rw.Write(b)
}

/*
MarshalJSON is an implementation of json.Marshaler to properly marshal the response
body.
*/
func (res *ResponseError[Metadata]) MarshalJSON() ([]byte, error) {
	formatted := responseErrorJSON[Metadata]{
		Status:   http.StatusText(res.statusCode),
		Error:    res.errorstack,
		Metadata: res.metadata,
	}

	return json.Marshal(formatted)
}
