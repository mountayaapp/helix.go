package graphql

import (
	"encoding/json"
	"net/http"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/service"
)

/*
responseSuccessBody is the JSON body returned for successful HTTP responses.
*/
type responseSuccessBody struct {
	Status string `json:"status"`
}

/*
responseErrorBody is the JSON body returned for error HTTP responses.
*/
type responseErrorBody struct {
	Status string           `json:"status"`
	Error  *errorstack.Error `json:"error,omitempty"`
}

/*
writeSuccess writes a JSON success response to the ResponseWriter.
*/
func writeSuccess(rw http.ResponseWriter, status int) {
	b, err := json.Marshal(responseSuccessBody{
		Status: http.StatusText(status),
	})

	if err != nil {
		writeError(rw, nil, http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	rw.Write(b)
}

/*
writeError writes a JSON error response to the ResponseWriter.
*/
func writeError(rw http.ResponseWriter, req *http.Request, status int) {
	b, err := json.Marshal(responseErrorBody{
		Status: http.StatusText(status),
		Error:  errorstack.New(supportedLocales[getPreferredLanguage(req)][status]),
	})

	if err != nil {
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	rw.Write(b)
}

/*
handlerHealthcheck is the default handler function for the healthcheck endpoint.
It relies on the service's aggregated status to determine the overall health.
*/
func (g *graphql) handlerHealthcheck(rw http.ResponseWriter, req *http.Request) {
	var status int = http.StatusOK
	if g.config.Healthcheck != nil {
		status = g.config.Healthcheck(req)
	} else {
		status, _ = service.Status(req.Context())
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
