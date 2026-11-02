package rest

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/uptrace/bunrouter"
)

// canonicalPaths answers a request whose path names a route only once it is
// cleaned up ("/things//42", "/things/./42"), or once a trailing slash is dropped
// or added: with a 308 to the route's own path for a GET or a HEAD, and with the
// 404 error envelope for any other method.
//
// The underlying router answers such a request with a 301 instead, and a client
// follows a 301 to a POST, a PUT or a DELETE by sending a GET: a write would land
// on a read of the same path and be answered as though it had succeeded. A 308
// keeps the method, which is harmless for a read; for a write, the path the client
// sent is one no route has, and it is told so rather than redirected into
// repeating the write somewhere it did not name.
//
// It runs before Config.Middleware, so no credential, quota or other work of the
// application is spent on a request whose path no route serves as written, and
// inside Config.EdgeMiddleware, so its answer carries what every answer does. A
// path a route serves as written, a wildcard route's above all, goes on untouched.
func (r *rest) canonicalPaths(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		location, ok := r.canonicalLocation(req)
		if !ok {
			next.ServeHTTP(rw, req)
			return
		}

		switch req.Method {
		case http.MethodGet, http.MethodHead:
			rw.Header().Set("Location", location)
			rw.WriteHeader(http.StatusPermanentRedirect)
		default:
			NewResponseError[NoMetadata](req).
				SetStatus(http.StatusNotFound).
				Write(rw)
		}
	})
}

// canonicalLocation returns where the router would redirect req, and false when
// it would not: its path names a route as written, or names none at all.
//
// The router is asked rather than second-guessed. It is served the request with
// a routeProbe as the writer, which middlewareRoute recognises: a request that
// matched a route, or that the router answers 404 or 405, stops there without
// reaching any handler. Only the router's redirect bypasses middlewareRoute, and
// it is the one answer the probe keeps. Asking is skipped for a path no redirect
// can concern: a clean one without a trailing slash, while no route's template
// ends in one.
func (r *rest) canonicalLocation(req *http.Request) (string, bool) {
	path := req.URL.RawPath
	if path == "" {
		path = req.URL.Path
	}

	if path == "" || path == "/" {
		return "", false
	}

	if !strings.HasSuffix(path, "/") && bunrouter.CleanPath(path) == path && !r.slashRoutes.Load() {
		return "", false
	}

	probe := &routeProbe{header: make(http.Header)}
	r.bun.ServeHTTP(probe, req)

	if probe.matched || probe.status != http.StatusMovedPermanently {
		return "", false
	}

	redirected, err := url.Parse(probe.header.Get("Location"))
	if err != nil {
		return "", false
	}

	// The router builds its location from the path it matched, which is the
	// escaped one whenever the request's path needed escaping, and escapes it a
	// second time on the way out. Parsing undoes that second escaping, and leaves
	// either an escaped path to send as it is or a decoded one to escape.
	location := redirected.Path
	if req.URL.RawPath == "" {
		location = (&url.URL{Path: location}).EscapedPath()
	}

	if redirected.RawQuery != "" {
		location += "?" + redirected.RawQuery
	}

	return location, true
}

// routeProbe is the writer the router is asked about a request with, rather than
// made to serve it. It keeps the status and headers of what the router answers on
// its own, which is only ever a redirect, and discards the body.
type routeProbe struct {
	header http.Header
	status int

	// matched reports that middlewareRoute saw the request: the router matched a
	// route for it as written, or answered it 404 or 405.
	matched bool
}

// Header returns the headers the router set.
func (p *routeProbe) Header() http.Header {
	return p.header
}

// Write discards b, reporting it written.
func (p *routeProbe) Write(b []byte) (int, error) {
	return len(b), nil
}

// WriteHeader records the first status the router answered with.
func (p *routeProbe) WriteHeader(status int) {
	if p.status == 0 {
		p.status = status
	}
}
