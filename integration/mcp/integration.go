package mcp

import (
	"net/http"

	"github.com/mountaya/helix.go/integration"
)

// buildHandler builds the handler chain the HTTP server serves, outermost first:
//
//   - the entry span, so that a response either middleware writes itself — a 401,
//     a 429, a CORS preflight — is traced and measured like any other;
//   - Config.EdgeMiddleware, which every request crosses;
//   - the answer to a panic in anything further in (recoverPanics);
//   - Config.Middleware, which the health probes and the protected resource
//     metadata bypass;
//   - event stamping, innermost, so that it runs after the application's
//     middleware has had its chance to put an Event in the request context;
//   - the mux.
func (m *mcp) buildHandler() http.Handler {
	inner := integration.StampEventMiddleware(m.mux)

	h := inner
	if m.config.Middleware != nil {
		wrapped := m.config.Middleware(inner)
		h = http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if m.bypassesMiddleware(req.URL.Path) {
				inner.ServeHTTP(rw, req)
				return
			}

			wrapped.ServeHTTP(rw, req)
		})
	}

	h = recoverPanics(h)
	if m.config.EdgeMiddleware != nil {
		h = m.config.EdgeMiddleware(h)
	}

	// This integration's own instrumentation speaks the same vocabulary as the
	// other integrations rather than the semantic conventions otelhttp emits — see
	// the package overview of service.
	return m.http.Middleware(h)
}

// bypassesMiddleware reports whether a request for path skips Config.Middleware.
//
// The health probes always respond, without the authentication or other
// service-level checks the middleware may enforce. In OAuth Resource Server mode
// so does the Protected Resource Metadata: it is the public discovery document
// clients fetch before authenticating, so it must remain reachable without
// credentials even when the consumer's middleware enforces them.
func (m *mcp) bypassesMiddleware(path string) bool {
	switch path {
	case integration.PathLiveness, integration.PathReadiness:
		return true
	default:
		return m.metadataPath != "" && path == m.metadataPath
	}
}
