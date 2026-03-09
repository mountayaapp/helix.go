package temporal

import (
	"context"
	"strings"

	"github.com/mountayaapp/helix.go/event"
	"github.com/mountayaapp/helix.go/service"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
)

/*
spanKeyIdentifier is the unique internal type for storing an OTEL Span in
Temporal workflow and activity contexts.
*/
type spanKeyIdentifier struct{}

/*
spanCtxKey is the context key used to store the current OTEL Span.
*/
var spanCtxKey spanKeyIdentifier

/*
tagRenames maps default Temporal tag names to their normalized equivalents.
*/
var tagRenames = map[string]string{
	"temporalWorkflowID": "temporal.workflow.id",
	"temporalRunID":      "temporal.workflow.run_id",
	"temporalActivityID": "temporal.activity.id",
	"temporalUpdateID":   "temporal.update.id",
}

/*
buildTracer tries to build the Temporal custom tracer from ConfigClient.
*/
func buildTracer(svc *service.Service, cfg ConfigClient) (interceptor.Tracer, error) {
	otelTracer := service.TracerProvider(svc).Tracer("github.com/mountayaapp/helix.go/integration/temporal")

	return opentelemetry.NewTracer(opentelemetry.TracerOptions{
		Tracer:            otelTracer,
		TextMapPropagator: otel.GetTextMapPropagator(),
		SpanContextKey:    spanCtxKey,
		SpanStarter: func(ctx context.Context, t oteltrace.Tracer, spanName string, opts ...oteltrace.SpanStartOption) oteltrace.Span {

			// Compute the flat map once and reuse it for both baggage creation and
			// span attribute population, avoiding redundant reflection-based traversals.
			// First try the standard event context key. If not found, fall back to the
			// Temporal-specific key used by ExtractToWorkflow (which only has access
			// to workflow.Context and cannot set the standard context key).
			e, ok := event.EventFromContext(ctx)
			if !ok {
				e, ok = ctx.Value(eventCtxKey).(event.Event)
			}

			var mapped map[string]string
			if ok {
				mapped = event.ToFlatMap(e)
				members := make([]baggage.Member, 0, len(mapped))
				for k, v := range mapped {
					m, err := baggage.NewMember(k, v)
					if err == nil {
						members = append(members, m)
					}
				}

				if len(members) > 0 {
					b, err := baggage.New(members...)
					if err == nil {
						ctx = baggage.ContextWithBaggage(ctx, b)
					}
				}
			}

			// By default, the Temporal Go client includes the name of the workflow or
			// activity in the traces, such as "RunWorkflow:myworkflow". Only keep
			// the action name.
			split := strings.Split(spanName, ":")
			name := split[0]

			// Populate the Span attributes retrieved from the event in context.
			_, span := otelTracer.Start(ctx, humanized+": "+name, opts...)
			if ok {
				setEventSpanAttributes(span, mapped)
			}

			span.SetAttributes(
				attrKeyServerAddress.String(cfg.Address),
				attrKeyNamespace.String(cfg.Namespace),
			)

			return span
		},
	})
}

/*
customtracer embeds interceptor.Tracer and override the StartSpan method, allowing
to override/rename default trace attributes set by the Temporal client.
*/
type customtracer struct {
	interceptor.Tracer
}

/*
StartSpan starts and returns a span with the given options, but with default
trace attributes renamed.
*/
func (m customtracer) StartSpan(opts *interceptor.TracerStartSpanOptions) (interceptor.TracerSpan, error) {
	for old, renamed := range tagRenames {
		if v := opts.Tags[old]; v != "" {
			opts.Tags[renamed] = v
			delete(opts.Tags, old)
		}
	}

	return m.Tracer.StartSpan(opts)
}
