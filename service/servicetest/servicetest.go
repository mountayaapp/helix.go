package servicetest

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mountaya/helix.go/internal/telemetry/log"
	"github.com/mountaya/helix.go/internal/telemetry/trace"
	"github.com/mountaya/helix.go/service"

	"go.opentelemetry.io/otel"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// New returns a Service whose three signals are recorded in memory, along with
// the Recorder holding them. Stopping the Service is registered on tb, so a test
// never has to remember to do it.
//
// The providers are supplied through service.Config, which means the Service does
// not own them and Stop will not shut them down - so the Recorder is still
// readable after the Service has stopped. All three record synchronously, so
// there is nothing to flush before asserting.
//
// Globals are deliberately installed. Instrumentation helix does not wire itself
// reaches for the global provider, and a recorder that missed it would give a
// false picture of what a service emits: a golden test is supposed to catch a
// third-party package writing to a span, which is what it was written to catch.
// The cost is that a test using this must not call t.Parallel, which matches the
// house convention of not using it at all.
//
// It also means a test helper that talks to the network while the Recorder is
// live shows up in the recording. testcontainers-go instruments its own Docker
// API calls, so the integrations that use it start their container before calling
// New.
func New(tb testing.TB) (*service.Service, *Recorder) {
	tb.Helper()

	spans := tracetest.NewSpanRecorder()
	logs := &logRecorder{}
	reader := sdkmetric.NewManualReader()

	// The Service installs its providers as the process-wide globals. Put back
	// whatever was there before, so that the next test — or the next package's —
	// does not inherit providers recording into a Recorder nobody reads.
	tracerProvider := otel.GetTracerProvider()
	loggerProvider := otel.GetLoggerProvider()
	meterProvider := otel.GetMeterProvider()
	propagator := otel.GetTextMapPropagator()
	logger := log.SetDefault(nil)
	tracer := trace.SetDefault(nil)
	log.SetDefault(logger)
	trace.SetDefault(tracer)

	tb.Cleanup(func() {
		otel.SetTracerProvider(tracerProvider)
		otel.SetLoggerProvider(loggerProvider)
		otel.SetMeterProvider(meterProvider)
		otel.SetTextMapPropagator(propagator)
		log.SetDefault(logger)
		trace.SetDefault(tracer)
	})

	svc, err := service.New(tb.Context(), service.Config{
		TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)),
		LoggerProvider: sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(logs))),
		MeterProvider:  sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
	})
	if err != nil {
		tb.Fatalf("servicetest.New() failed unexpectedly; err=%v", err)
	}

	tb.Cleanup(func() {
		// tb.Context is already cancelled by the time cleanups run, and a cancelled
		// context would skip the very teardown this is here to exercise.
		if err := svc.Stop(context.Background()); err != nil {
			tb.Errorf("servicetest: Stop(ctx) failed unexpectedly; err=%v", err)
		}
	})

	return svc, &Recorder{tb: tb, spans: spans, logs: logs, reader: reader}
}

// Recorder holds the telemetry a Service produced. Every method returns a
// snapshot: the caller may read and sort it freely.
type Recorder struct {
	tb     testing.TB
	spans  *tracetest.SpanRecorder
	logs   *logRecorder
	reader *sdkmetric.ManualReader
}

// Spans returns the spans that have ended, in the order they ended. A span that
// is still open is not included, so a test asserting on one must let the code
// under test finish with it first.
func (r *Recorder) Spans() []sdktrace.ReadOnlySpan {
	return r.spans.Ended()
}

// Records returns the log records emitted so far.
func (r *Recorder) Records() []sdklog.Record {
	return r.logs.snapshot()
}

// Metrics collects from the reader and returns every metric across all
// instrumentation scopes, flattened. An empty result means nothing was recorded.
//
// A failed collection fails the test rather than being returned, so that an empty
// result has exactly one meaning. A Recorder that reported "no metrics" when it
// meant "could not read them" would turn a broken assertion into a passing one.
func (r *Recorder) Metrics() []metricdata.Metrics {
	r.tb.Helper()

	var rm metricdata.ResourceMetrics
	if err := r.reader.Collect(context.Background(), &rm); err != nil {
		r.tb.Fatalf("servicetest: Recorder.Metrics() failed to collect; err=%v", err)
		return nil
	}

	out := make([]metricdata.Metrics, 0)
	for _, scope := range rm.ScopeMetrics {
		out = append(out, scope.Metrics...)
	}

	return out
}

// logRecorder is an sdklog.Exporter that keeps what it is given.
type logRecorder struct {
	mu      sync.Mutex
	records []sdklog.Record
}

// Export stores a clone of each record. The SDK contract forbids retaining the
// slice, and requires Clone before a record is held onto.
func (e *logRecorder) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for i := range records {
		e.records = append(e.records, records[i].Clone())
	}

	return nil
}

// Shutdown satisfies sdklog.Exporter. There is nothing to release.
func (e *logRecorder) Shutdown(_ context.Context) error { return nil }

// ForceFlush satisfies sdklog.Exporter. Records are stored synchronously, so
// there is never anything buffered.
func (e *logRecorder) ForceFlush(_ context.Context) error { return nil }

// snapshot returns a copy of the records held so far.
func (e *logRecorder) snapshot() []sdklog.Record {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := make([]sdklog.Record, len(e.records))
	copy(out, e.records)

	return out
}

// SpanSummary is a span reduced to the parts worth pinning in a golden test: what
// it is called, who emitted it, and which attribute keys it carries.
//
// Values, timings and IDs are excluded on purpose. They differ on every run - a
// temporary directory, an ephemeral port, a duration - and a golden that includes
// them tests the environment rather than the instrumentation.
type SpanSummary struct {
	Name          string
	Scope         string
	Kind          string
	AttributeKeys []string
}

// String renders the summary as one stable line, so that a failing golden diff
// reads as a list of spans rather than a struct dump.
func (s SpanSummary) String() string {
	return s.Scope + " | " + s.Kind + " | " + s.Name + " | " + strings.Join(s.AttributeKeys, ",")
}

// Summarize reduces spans to their comparable shape, with attribute keys sorted
// so that a change in the order the instrumentation sets them does not read as a
// change in what it emits.
func Summarize(spans []sdktrace.ReadOnlySpan) []SpanSummary {
	out := make([]SpanSummary, 0, len(spans))
	for _, span := range spans {
		keys := make([]string, 0, len(span.Attributes()))
		for _, attr := range span.Attributes() {
			keys = append(keys, string(attr.Key))
		}
		slices.Sort(keys)

		out = append(out, SpanSummary{
			Name:          span.Name(),
			Scope:         span.InstrumentationScope().Name,
			Kind:          span.SpanKind().String(),
			AttributeKeys: keys,
		})
	}

	return out
}
