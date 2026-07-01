package temporal

import (
	"context"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/workflow"
)

/*
dialClient creates a Temporal client from the given ConfigClient. It handles TLS,
tracer, logger, context propagator, and client.Dial. This is the shared helper
used by both Connect and New.
*/
func dialClient(svc *service.Service, cfg *ConfigClient) (client.Client, error) {
	var entries []errorstack.Entry

	// Try to build the tracer.
	t, err := buildTracer(svc, *cfg)
	if err != nil {
		entries = append(entries, errorstack.Entry{
			Message: integration.NormalizeErrorMessage(err),
			Path:    []any{"config"},
		})
	}

	// Set the default Temporal config, using custom logger, context propagator, and
	// tracer.
	var opts = client.Options{
		HostPort:  cfg.Address,
		Namespace: cfg.Namespace,
		Logger:    newCustomLogger(svc),
		ContextPropagators: []workflow.ContextPropagator{
			&custompropagator{
				cachedCtx: service.Context(svc, context.Background()),
			},
		},
		Interceptors: []interceptor.ClientInterceptor{
			interceptor.NewTracingInterceptor(customtracer{
				Tracer: t,
			}),
		},
		DataConverter: cfg.DataConverter,
	}

	// Set TLS options only if enabled in ConfigClient.
	if cfg.TLS.Enabled {
		var tlsEntries []errorstack.Entry
		opts.ConnectionOptions.TLS, tlsEntries = cfg.TLS.ToStandardTLS()
		entries = append(entries, tlsEntries...)
	}

	// Try to create the Temporal client.
	c, err := client.Dial(opts)
	if err != nil {
		entries = append(entries, errorstack.Entry{
			Message: integration.NormalizeErrorMessage(err),
			Path:    []any{"config"},
		})
	}

	// Stop here if validation entries were collected.
	if len(entries) > 0 {
		return nil, errorstack.NewValidation(entries...)
	}

	return c, nil
}
