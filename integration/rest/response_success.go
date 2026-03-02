package rest

import (
	"encoding/json"
	"net/http"
)

var _ json.Marshaler = (*ResponseSuccess[any, any])(nil)

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
*/
type responseSuccessJSON[Metadata any, Data any] struct {
	Status   string    `json:"status"`
	Metadata *Metadata `json:"metadata,omitempty"`
	Data     *Data     `json:"data,omitempty"`
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
SetStatus sets the response's status code.
*/
func (res *ResponseSuccess[Metadata, Data]) SetStatus(status int) *ResponseSuccess[Metadata, Data] {
	res.statusCode = status

	return res
}

/*
SetMetadata sets the "metadata" object of the response body.
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
		NewResponseSuccess[NoData, NoData](res.request).
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
func (res *ResponseSuccess[Metadata, Data]) MarshalJSON() ([]byte, error) {
	formatted := responseSuccessJSON[Metadata, Data]{
		Status:   http.StatusText(res.statusCode),
		Metadata: res.metadata,
		Data:     res.data,
	}

	return json.Marshal(formatted)
}
