package mcp

import (
	"net/http"
)

/*
handlerLiveness is the handler function for the liveness probe endpoint at
GET /health. Returns 200 immediately without checking any dependencies.
*/
func (m *mcp) handlerLiveness(rw http.ResponseWriter, req *http.Request) {
	writeSuccess(rw, http.StatusOK)
}

/*
handlerReadiness is the handler function for the readiness probe endpoint at
GET /ready. Calls the custom function defined in the Config if applicable,
otherwise aggregates all dependency statuses via the service.
*/
func (m *mcp) handlerReadiness(rw http.ResponseWriter, req *http.Request) {
	var status int
	if m.config.Readiness != nil {
		status = m.config.Readiness(req)
	} else {
		status, _ = m.svc.Status(req.Context())
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
func (m *mcp) handlerNotFound(rw http.ResponseWriter, req *http.Request) {
	writeError(rw, req, http.StatusNotFound)
}

/*
handlerMethodNotAllowed is the default handler function if the method is not
allowed on the MCP transport path (error 405).
*/
func (m *mcp) handlerMethodNotAllowed(rw http.ResponseWriter, req *http.Request) {
	writeError(rw, req, http.StatusMethodNotAllowed)
}
