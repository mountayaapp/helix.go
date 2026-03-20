package graphql

import (
	"net/http"
)

/*
handlerLiveness is the handler function for the liveness probe endpoint.
Returns 200 immediately without checking any dependencies.
*/
func (g *graphql) handlerLiveness(rw http.ResponseWriter, req *http.Request) {
	writeSuccess(rw, http.StatusOK)
}

/*
handlerReadiness is the handler function for the readiness probe endpoint.
Calls the custom function defined in the Config if applicable, otherwise
aggregates all dependency statuses via the service.
*/
func (g *graphql) handlerReadiness(rw http.ResponseWriter, req *http.Request) {
	var status int
	if g.config.Readiness != nil {
		status = g.config.Readiness(req)
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
