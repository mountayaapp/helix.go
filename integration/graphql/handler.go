package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/mountayaapp/helix.go/integration"

	gqlgen "github.com/99designs/gqlgen/graphql"
)

// allowedMethods is the Allow header of the GraphQL endpoint: the methods routed
// to it.
const allowedMethods = "OPTIONS, POST"

// handlerNotFound is the default handler function if the path is not found (error
// 404).
func (g *graphql) handlerNotFound(rw http.ResponseWriter, req *http.Request) {
	integration.WriteError(rw, req, http.StatusNotFound)
}

// handlerMethodNotAllowed is the default handler function if the method is not
// allowed (error 405).
func (g *graphql) handlerMethodNotAllowed(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Allow", allowedMethods)
	integration.WriteError(rw, req, http.StatusMethodNotAllowed)
}

// handleGraphQL serves a POST to the GraphQL endpoint. It reads and checks the
// request itself before handing it to gqlgen, whose POST transport would
// otherwise:
//
//   - read a body of any size into memory;
//   - answer a body that does not decode with an error message embedding the
//     whole of it, echoing up to MaxRequestBodyBytes of whatever the client sent;
//   - answer a request it does not support with an error carrying no code.
//
// The body is decoded here with the decoder the transport uses, so a body this
// accepts is one the transport decodes too, and none of its error paths is
// reachable. The cost is a second decode of a body already bounded in size.
func (g *graphql) handleGraphQL(rw http.ResponseWriter, req *http.Request) {
	if !g.post.Supports(req) {
		integration.WriteError(rw, req, http.StatusBadRequest)
		return
	}

	if req.ContentLength > g.config.MaxRequestBodyBytes {
		integration.WriteError(rw, req, http.StatusRequestEntityTooLarge)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(rw, req.Body, g.config.MaxRequestBodyBytes))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			integration.WriteError(rw, req, http.StatusRequestEntityTooLarge)
			return
		}

		integration.WriteError(rw, req, http.StatusBadRequest)
		return
	}

	var params gqlgen.RawParams
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&params); err != nil {
		integration.WriteError(rw, req, http.StatusBadRequest)
		return
	}

	req.Body = io.NopCloser(bytes.NewReader(body))

	if g.config.RequestTimeout > 0 {
		ctx, cancel := context.WithTimeout(req.Context(), g.config.RequestTimeout)
		defer cancel()

		req = req.WithContext(ctx)
	}

	g.gql.ServeHTTP(rw, req)
}
