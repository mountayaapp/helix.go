package rest

import (
	"net/http"

	"github.com/uptrace/bunrouter"
)

// handlerNotFound is the default handler function if the path is not found (error
// 404).
func (r *rest) handlerNotFound(rw http.ResponseWriter, req bunrouter.Request) error {
	NewResponseError[NoMetadata](req.Request).
		SetStatus(http.StatusNotFound).
		Write(rw)

	return nil
}

// handlerMethodNotAllowed is the default handler function if the method is not
// allowed (error 405). Its Allow header lists the methods the route takes.
func (r *rest) handlerMethodNotAllowed(rw http.ResponseWriter, req bunrouter.Request) error {
	if allow := r.methods.allow(req.Route()); allow != "" {
		rw.Header().Set("Allow", allow)
	}

	NewResponseError[NoMetadata](req.Request).
		SetStatus(http.StatusMethodNotAllowed).
		Write(rw)

	return nil
}
