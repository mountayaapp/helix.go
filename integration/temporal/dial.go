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
	stack := errorstack.New("Failed to initialize integration", errorstack.WithIntegration(identifier))

	// Try to build the tracer.
	t, err := buildTracer(svc, *cfg)
	if err != nil {
		stack.WithValidations(errorstack.Validation{
			Message: integration.NormalizeErrorMessage(err),
		})
	}

	// Set the default Temporal config, using custom logger, context propagator, and
	// tracer.
	var opts = client.Options{
		HostPort:           cfg.Address,
		Namespace:          cfg.Namespace,
		Logger:             newCustomLogger(svc),
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
		var validations []errorstack.Validation

		opts.ConnectionOptions.TLS, validations = cfg.TLS.ToStandardTLS()
		if len(validations) > 0 {
			stack.WithValidations(validations...)
		}
	}

	// Try to create the Temporal client.
	c, err := client.Dial(opts)
	if err != nil {
		stack.WithValidations(errorstack.Validation{
			Message: integration.NormalizeErrorMessage(err),
		})
	}

	// Stop here if error validations were encountered.
	if stack.HasValidations() {
		return nil, stack
	}

	return c, nil
}
