package mcp

import (
	"context"
	"net/http"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

/*
Ensure *mcp complies to the integration.Server type.
*/
var _ integration.Server = (*mcp)(nil)

/*
Name returns the string representation of the MCP server integration.
*/
func (m *mcp) Name() string {
	return identifier
}

/*
Start starts the HTTP server of the MCP server integration.
*/
func (m *mcp) Start(ctx context.Context) error {

	// Wrap the built-in HTTP handler with the one given by the user, if applicable.
	// Skip user middleware for the health endpoints so they always respond without
	// requiring authentication or other service-level checks. In OAuth Resource
	// Server mode, the Protected Resource Metadata endpoint is bypassed too: it is
	// the public discovery document clients fetch before authenticating, so it must
	// remain reachable without credentials even when the consumer's middleware
	// enforces them.
	var h http.Handler = m.mux
	if m.config.Middleware != nil {
		wrapped := m.config.Middleware(m.mux)
		h = http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/health" || req.URL.Path == "/ready" {
				m.mux.ServeHTTP(rw, req)
				return
			}

			if m.config.OAuth.Enabled && req.URL.Path == wellKnownProtectedResource {
				m.mux.ServeHTTP(rw, req)
				return
			}

			wrapped.ServeHTTP(rw, req)
		})
	}

	// Wrap the handler previously built with the one designed for OpenTelemetry
	// traces.
	h = otelhttp.NewHandler(h, "",
		otelhttp.WithMessageEvents(otelhttp.ReadEvents, otelhttp.WriteEvents),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + r.URL.Path
		}),
	)

	// Create the HTTP server with the given configuration and the handler built.
	m.server = &http.Server{
		Addr:    m.config.Address,
		Handler: h,
	}

	// Start the HTTP server with or without TLS depending on the Config, and catch
	// unexpected errors.
	var err error
	if m.config.TLS.Enabled {
		tlsConfig, tlsEntries := m.config.TLS.ToStandardTLS()
		if len(tlsEntries) > 0 {
			return errorstack.NewValidation(tlsEntries...)
		}

		m.server.TLSConfig = tlsConfig
		err = m.server.ListenAndServeTLS("", "")
	} else {
		err = m.server.ListenAndServe()
	}

	if err != nil && err != http.ErrServerClosed {
		return errorstack.Wrap(err, "Failed to start server")
	}

	return nil
}

/*
Stop tries to gracefully stop the HTTP server.
*/
func (m *mcp) Stop(ctx context.Context) error {
	if err := errorstack.Wrap(m.server.Shutdown(ctx), "Failed to gracefully stop server"); err != nil {
		return err
	}

	return nil
}

/*
Status always returns a `200` status.
*/
func (m *mcp) Status(ctx context.Context) (int, error) {
	return 200, nil
}
