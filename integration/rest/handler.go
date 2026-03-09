package rest

import (
	"net/http"

	"github.com/uptrace/bunrouter"
)

/*
NoMetadata allows to set no "metadata" object in the HTTP response.
*/
type NoMetadata struct{}

/*
NoData allows to set no "data" object in the HTTP response.
*/
type NoData struct{}

/*
handlerHealthcheck is the default handler function for the healthcheck endpoint.
Call the custom function defined in the Config if applicable.
*/
func (r *rest) handlerHealthcheck(rw http.ResponseWriter, req bunrouter.Request) error {
	var status int
	if r.config.Healthcheck != nil {
		status = r.config.Healthcheck(req.Request)
	} else {
		status, _ = r.svc.Status(req.Context())
	}

	if status >= 300 {
		NewResponseError[NoMetadata](req.Request).
			SetStatus(status).
			Write(rw)
	} else {
		NewResponseSuccess[NoMetadata, NoData](req.Request).
			SetStatus(status).
			Write(rw)
	}

	return nil
}

/*
handlerNotFound is the default handler function if the path is not found (error
404).
*/
func (r *rest) handlerNotFound(rw http.ResponseWriter, req bunrouter.Request) error {
	NewResponseError[NoMetadata](req.Request).
		SetStatus(http.StatusNotFound).
		Write(rw)
	return nil
}

/*
handlerMethodNotAllowed is the default handler function if the method is not
allowed (error 405).
*/
func (r *rest) handlerMethodNotAllowed(rw http.ResponseWriter, req bunrouter.Request) error {
	NewResponseError[NoMetadata](req.Request).
		SetStatus(http.StatusMethodNotAllowed).
		Write(rw)
	return nil
}
