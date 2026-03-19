package rest

import (
	"context"
	"net/http"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

/*
Ensure *rest complies to the integration.Server type.
*/
var _ integration.Server = (*rest)(nil)

/*
Name returns the string representation of the HTTP REST integration.
*/
func (r *rest) Name() string {
	return identifier
}

/*
Start starts the HTTP server of the HTTP REST integration.
*/
func (r *rest) Start(ctx context.Context) error {
	stack := errorstack.New("Failed to start HTTP server", errorstack.WithIntegration(identifier))

	// Wrap the built-in HTTP handler with the one given by the user, if applicable.
	// Skip user middleware for the health endpoint so it always responds without
	// requiring authentication or other service-level checks.
	var h http.Handler = r.bun
	if r.config.Middleware != nil {
		wrapped := r.config.Middleware(r.bun)
		h = http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/health" {
				r.bun.ServeHTTP(rw, req)
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
	r.server = &http.Server{
		Addr:    r.config.Address,
		Handler: h,
	}

	// Start the HTTP server with or without TLS depending on the Config, and catch
	// unexpected errors.
	var err error
	if r.config.TLS.Enabled {
		err = r.server.ListenAndServeTLS(r.config.TLS.CertFile, r.config.TLS.KeyFile)
	} else {
		err = r.server.ListenAndServe()
	}

	if err != nil && err != http.ErrServerClosed {
		stack.WithValidations(errorstack.Validation{
			Message: err.Error(),
		})

		return stack
	}

	return nil
}

/*
Stop tries to gracefully stop the HTTP server.
*/
func (r *rest) Stop(ctx context.Context) error {
	stack := errorstack.New("Failed to gracefully stop HTTP server", errorstack.WithIntegration(identifier))

	err := r.server.Shutdown(ctx)
	if err != nil {
		stack.WithValidations(errorstack.Validation{
			Message: err.Error(),
		})

		return stack
	}

	return nil
}

/*
Status always returns a `200` status.
*/
func (r *rest) Status(ctx context.Context) (int, error) {
	return 200, nil
}
