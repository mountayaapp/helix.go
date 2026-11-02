package temporal

import (
	"context"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service"

	"go.opentelemetry.io/otel/attribute"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/workflow"
)

// dial connects a Temporal client configured by cfg, named name in telemetry. It
// is the helper Connect and New share: it wires the tracer, the logger, the
// metrics handler and the Event propagator, and dials.
//
// path is where cfg sits in the configuration the caller was given — "config" for
// Connect, "config", "client" for New — so that a TLS entry points at the field a
// caller has to fix.
//
// Nothing is dialed when the TLS configuration cannot be built: a client that
// asked for TLS and could not have it must not fall back to plaintext.
func dial(ctx context.Context, svc *service.Service, cfg *ConfigClient, name string, path ...any) (client.Client, error) {

	// The Instrument does not open the SDK's spans - the interceptor does, and that
	// is the point of keeping it - but it still owns what they are called and what
	// they carry, so that this integration does not name things its own way. It
	// also carries the meter the SDK's own metrics are reported through.
	inst, err := integration.NewInstrument(integration.InstrumentConfig{
		Identifier: identifier,
		Humanized:  humanized,
		Name:       name,
		Attributes: []attribute.KeyValue{
			attrKeyServerAddress.String(cfg.Address),
			attrKeyNamespace.String(cfg.Namespace),
		},
		TracerProvider: service.TracerProvider(svc),
		MeterProvider:  service.MeterProvider(svc),
		Propagator:     service.Propagator(svc),
	})
	if err != nil {
		return nil, err
	}

	tlsConfig, entries := cfg.TLS.ToStandardTLS()
	if len(entries) > 0 {
		return nil, errorstack.NewValidation(rebase(entries, path...)...)
	}

	tracer, err := buildTracer(inst, svc)
	if err != nil {
		return nil, errorstack.Wrap(err, "Failed to build the Temporal tracer",
			errorstack.WithExtension("reason", integration.NormalizeErrorMessage(err)),
		)
	}

	logger := newCustomLogger(svc)

	// The SDK reports its own metrics - task-queue latencies, poller counts, task
	// failures - and they are the ones that say whether a worker is keeping up.
	// Handing it the Service's meter is what makes them reach the same pipeline as
	// everything else, instead of the no-op the SDK defaults to.
	opts := client.Options{
		HostPort:  cfg.Address,
		Namespace: cfg.Namespace,
		Logger:    logger,
		MetricsHandler: opentelemetry.NewMetricsHandler(opentelemetry.MetricsHandlerOptions{
			Meter:             inst.Meter(),
			InitialAttributes: attribute.NewSet(inst.AppendIdentity(nil)...),

			// The SDK panics on a meter error by default, which turns a telemetry
			// problem into a process one. Nothing here is worth that.
			OnError: func(error) {},
		}),
		ContextPropagators: []workflow.ContextPropagator{
			newCustomPropagator(cfg, logger.logger),
		},

		// The first interceptor is the outermost. attributesInterceptor comes after
		// the tracing interceptor so that it runs inside it, once the span exists.
		Interceptors: []interceptor.ClientInterceptor{
			interceptor.NewTracingInterceptor(tracer),
			&attributesInterceptor{},
		},
		DataConverter: cfg.DataConverter,
		ConnectionOptions: client.ConnectionOptions{
			TLS: tlsConfig,
		},
	}

	if cfg.APIKey != "" {
		opts.Credentials = client.NewAPIKeyStaticCredentials(cfg.APIKey)
	}

	// DialContext connects eagerly and checks the server's health, so an address
	// that cannot be reached — or credentials it refuses — fail here rather than
	// on the first workflow.
	c, err := client.DialContext(ctx, opts)
	if err != nil {
		return nil, errorstack.Wrap(err, "Failed to connect to Temporal",
			errorstack.WithCode(errorstack.CodeServiceUnavailable),
			errorstack.WithExtension("reason", integration.NormalizeErrorMessage(err)),
		)
	}

	return c, nil
}
