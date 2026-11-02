package mcp

import (
	"net/http"

	"github.com/mountayaapp/helix.go/integration"
)

// handlerNotFound is the default handler function if the path is not found (error
// 404).
func (m *mcp) handlerNotFound(rw http.ResponseWriter, req *http.Request) {
	integration.WriteError(rw, req, http.StatusNotFound)
}

// handlerMethodNotAllowed is the default handler function if the method is not
// allowed on the MCP transport path (error 405).
func (m *mcp) handlerMethodNotAllowed(rw http.ResponseWriter, req *http.Request) {
	integration.WriteError(rw, req, http.StatusMethodNotAllowed)
}
