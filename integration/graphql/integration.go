package graphql

import (
	"context"
	"net/http"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/internal/cloudprovider"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

/*
Ensure *graphql complies to the integration.Server type.
*/
var _ integration.Server = (*graphql)(nil)

/*
String returns the string representation of the GraphQL integration.
*/
func (g *graphql) String() string {
	return identifier
}

/*
Start starts the HTTP server of the GraphQL integration.
*/
func (g *graphql) Start(ctx context.Context) error {
	stack := errorstack.New("Failed to start HTTP server", errorstack.WithIntegration(identifier))

	// Wrap the built-in HTTP handler with the one given by the user, if applicable.
	var h http.Handler = g.mux
	if g.config.Middleware != nil {
		h = g.config.Middleware(g.mux)
	}

	// Wrap the handler previously built with the one designed for OpenTelemetry
	// traces.
	h = otelhttp.NewHandler(h, cloudprovider.Detected.Service(),
		otelhttp.WithMessageEvents(otelhttp.ReadEvents, otelhttp.WriteEvents),
	)

	// Create the HTTP server with the given configuration and the handler built.
	g.server = &http.Server{
		Addr:    g.config.Address,
		Handler: h,
	}

	// Start the HTTP server with or without TLS depending on the Config, and catch
	// unexpected errors.
	var err error
	if g.config.TLS.Enabled {
		err = g.server.ListenAndServeTLS(g.config.TLS.CertFile, g.config.TLS.KeyFile)
	} else {
		err = g.server.ListenAndServe()
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
func (g *graphql) Stop(ctx context.Context) error {
	stack := errorstack.New("Failed to gracefully stop HTTP server", errorstack.WithIntegration(identifier))

	err := g.server.Shutdown(ctx)
	if err != nil {
		stack.WithValidations(errorstack.Validation{
			Message: err.Error(),
		})

		return stack
	}

	return nil
}

/*
Status returns the health status of the GraphQL integration. When APQ cache is
enabled, it also checks the Valkey connection status. Returns a 200 status if
healthy, or the status returned by the Valkey integration otherwise.
*/
func (g *graphql) Status(ctx context.Context) (int, error) {
	if g.config.APQ.Enabled {
		if dep, ok := g.config.APQ.Valkey.(integration.Dependency); ok {
			return dep.Status(ctx)
		}
	}

	return 200, nil
}
