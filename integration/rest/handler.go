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
handlerLiveness is the handler function for the liveness probe endpoint.
Returns 200 immediately without checking any dependencies.
*/
func (r *rest) handlerLiveness(rw http.ResponseWriter, req bunrouter.Request) error {
	NewResponseSuccess[NoMetadata, NoData](req.Request).
		SetStatus(http.StatusOK).
		Write(rw)

	return nil
}

/*
handlerReadiness is the handler function for the readiness probe endpoint.
Calls the custom function defined in the Config if applicable, otherwise
aggregates all dependency statuses via the service.
*/
func (r *rest) handlerReadiness(rw http.ResponseWriter, req bunrouter.Request) error {
	var status int
	if r.config.Readiness != nil {
		status = r.config.Readiness(req.Request)
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
