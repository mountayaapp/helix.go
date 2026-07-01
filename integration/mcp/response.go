package mcp

import (
	"encoding/json"
	"net/http"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/internal/locales"
)

var fallbackErrorResponse = []byte(`{"errors":[{"message":"Internal server error","extensions":{"code":"INTERNAL_ERROR"}}]}`)

/*
successResponse is the constant 2xx envelope returned by the HTTP-layer
liveness/readiness probes. It mirrors the REST and GraphQL integrations' success
shape ({"data":null}) so a service running multiple transports surfaces identical
health-probe bodies. The HTTP status code carries the OK/Created/etc.
distinction; the body stays deliberately empty.
*/
var successResponse = []byte(`{"data":null}`)

/*
writeSuccess writes the canonical 2xx envelope to the ResponseWriter. The
body is always {"data":null} — matching REST and GraphQL — and the status code
carries the actual signal.
*/
func writeSuccess(rw http.ResponseWriter, status int) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	rw.Write(successResponse)
}

/*
writeError writes a GraphQL-spec error envelope to the ResponseWriter for
HTTP-layer errors that occur before the MCP transport layer (404, 405, …) or
when the readiness probe reports an unavailable dependency. The envelope shape
is:

	{"errors":[{"message":"…","extensions":{"code":"…"}}]}
*/
func writeError(rw http.ResponseWriter, req *http.Request, status int) {
	body := errorstack.New(
		locales.Message(req, status),
		errorstack.WithCode(errorstack.HTTPStatusToCode(status)),
	)

	b, err := json.Marshal(body)
	if err != nil {
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusInternalServerError)
		rw.Write(fallbackErrorResponse)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	rw.Write(b)
}
