package graphql

import (
	"encoding/json"
	"net/http"

	"github.com/mountayaapp/helix.go/errorstack"
)

var fallbackErrorResponse = []byte(`{"status":"Internal Server Error","error":{"message":"We have been notified of this unexpected internal error"}}`)

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
	Status string            `json:"status"`
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
		rw.Write(fallbackErrorResponse)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	rw.Write(b)
}
