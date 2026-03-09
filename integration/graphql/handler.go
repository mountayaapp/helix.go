package graphql

import (
	"net/http"
)

/*
handlerHealthcheck is the default handler function for the healthcheck endpoint.
It relies on the service's aggregated status to determine the overall health.
*/
func (g *graphql) handlerHealthcheck(rw http.ResponseWriter, req *http.Request) {
	var status int
	if g.config.Healthcheck != nil {
		status = g.config.Healthcheck(req)
	} else {
		status, _ = g.svc.Status(req.Context())
	}

	if status >= 300 {
		writeError(rw, req, status)
	} else {
		writeSuccess(rw, status)
	}
}

/*
handlerNotFound is the default handler function if the path is not found (error
404).
*/
func (g *graphql) handlerNotFound(rw http.ResponseWriter, req *http.Request) {
	writeError(rw, req, http.StatusNotFound)
}

/*
handlerMethodNotAllowed is the default handler function if the method is not
allowed (error 405).
*/
func (g *graphql) handlerMethodNotAllowed(rw http.ResponseWriter, req *http.Request) {
	writeError(rw, req, http.StatusMethodNotAllowed)
}
