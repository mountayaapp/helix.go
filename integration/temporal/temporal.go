package temporal

import (
	"github.com/mountayaapp/helix.go/errorstack"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/workflow"
)

/*
dialClient creates a Temporal client from the given ConfigClient. It handles TLS,
tracer, logger, context propagator, and client.Dial. This is the shared helper
used by both Connect and New.
*/
func dialClient(cfg *ConfigClient) (client.Client, error) {
	stack := errorstack.New("Failed to initialize integration", errorstack.WithIntegration(identifier))

	// Try to build the tracer.
	t, err := buildTracer(*cfg)
	if err != nil {
		stack.WithValidations(errorstack.Validation{
			Message: normalizeErrorMessage(err),
		})
	}

	// Set the default Temporal config, using custom logger, context propagator, and
	// tracer.
	var opts = client.Options{
		HostPort:           cfg.Address,
		Namespace:          cfg.Namespace,
		Logger:             new(customlogger),
		ContextPropagators: []workflow.ContextPropagator{new(custompropagator)},
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
			Message: normalizeErrorMessage(err),
		})
	}

	// Stop here if error validations were encountered.
	if stack.HasValidations() {
		return nil, stack
	}

	return c, nil
}
