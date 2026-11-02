package service

import (
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/internal/telemetry/log"
	"github.com/mountayaapp/helix.go/internal/telemetry/trace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	lognoop "go.opentelemetry.io/otel/log/noop"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Exporter selection lives here now: the telemetry packages hold only API types,
// so building an exporter from the standard OTEL_* environment is the Service's
// job, and so is shutting the resulting provider down.

func TestNew_ExporterSelection(t *testing.T) {
	for _, exporter := range []string{"none", "console", "otlp"} {
		t.Run(exporter, func(t *testing.T) {
			t.Setenv("OTEL_TRACES_EXPORTER", exporter)
			t.Setenv("OTEL_LOGS_EXPORTER", exporter)
			t.Setenv("OTEL_METRICS_EXPORTER", exporter)

			svc, err := New(t.Context(), Config{SkipGlobals: true})
			require.NoError(t, err)

			assert.NotNil(t, svc.tracer.Provider())
			assert.NotNil(t, svc.logger.Provider())
			assert.NotNil(t, svc.meterProvider)
			assert.NotNil(t, svc.sdkTracerProvider, "a Service that built the provider owns it")
			assert.NotNil(t, svc.sdkLoggerProvider)
			assert.NotNil(t, svc.sdkMeterProvider)

			assert.NoError(t, svc.Stop(t.Context()))
		})
	}
}

func TestNew_OTLPProtocolAndEndpoint(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:9999")

	svc, err := New(t.Context(), Config{SkipGlobals: true})
	require.NoError(t, err)

	assert.NoError(t, svc.Stop(t.Context()))
}

// TestNew_SuppliedProvidersAreNotOwned pins the ownership rule: a provider handed
// in through Config belongs to whoever created it, so Stop must not shut it down.
func TestNew_SuppliedProvidersAreNotOwned(t *testing.T) {
	svc, err := New(t.Context(), Config{
		SkipGlobals:    true,
		TracerProvider: tracenoop.NewTracerProvider(),
		MeterProvider:  metricnoop.NewMeterProvider(),
	})
	require.NoError(t, err)

	assert.Nil(t, svc.sdkTracerProvider, "a supplied provider is not the Service's to shut down")
	assert.Nil(t, svc.sdkMeterProvider)
	assert.NotNil(t, svc.meterProvider, "the supplied provider is still the one handed to integrations")
	assert.NoError(t, svc.Stop(t.Context()))
}

// TestNew_InstallsGlobals pins that the logger and tracer are registered the same
// way: their providers become the OpenTelemetry globals, and the helix wrappers the
// defaults telemetry/log and telemetry/trace fall back to.
func TestNew_InstallsGlobals(t *testing.T) {
	restoreDefaults(t)

	svc, err := New(t.Context(), Config{
		TracerProvider: tracenoop.NewTracerProvider(),
		LoggerProvider: lognoop.NewLoggerProvider(),
		MeterProvider:  metricnoop.NewMeterProvider(),
	})
	require.NoError(t, err)

	assert.Equal(t, svc.logger.Provider(), otel.GetLoggerProvider())
	assert.Equal(t, svc.tracer.Provider(), otel.GetTracerProvider())
	assert.Same(t, svc.logger, log.Default())
	assert.Same(t, svc.tracer, trace.Default())
	assert.NoError(t, svc.Stop(t.Context()))
}

func TestNew_SkipGlobalsLeavesDefaultsAlone(t *testing.T) {
	restoreDefaults(t)

	logger, tracer := log.NewNopLogger(), trace.NewNopTracer()
	log.SetDefault(logger)
	trace.SetDefault(tracer)

	svc := newTestService(t)

	assert.Same(t, logger, log.Default())
	assert.Same(t, tracer, trace.Default())
	assert.NoError(t, svc.Stop(t.Context()))
}

// restoreDefaults uninstalls, once the test ends, the default logger and tracer it
// installed. Every other test here builds its Service with SkipGlobals, so there
// is nothing installed before it to put back.
func restoreDefaults(t *testing.T) {
	t.Helper()

	t.Cleanup(func() {
		log.SetDefault(nil)
		trace.SetDefault(nil)
	})
}

func TestNew_SDKDisabled(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")

	svc, err := New(t.Context(), Config{SkipGlobals: true})
	require.NoError(t, err)

	assert.Nil(t, svc.sdkTracerProvider)
	assert.Nil(t, svc.sdkLoggerProvider)
	assert.Nil(t, svc.sdkMeterProvider)
	assert.NotNil(t, svc.meterProvider, "a disabled SDK still yields a usable no-op provider")
	assert.NoError(t, svc.Stop(t.Context()))
}

// TestStop_UnreachableCollectorIsNotAShutdownFailure pins the asymmetry documented
// in Stop: a PeriodicReader exports on Shutdown and reports an unreachable
// collector, where a BatchSpanProcessor drops silently. Since Run surfaces Stop's
// error as the process exit status, a down collector must not make a clean
// shutdown look like a failed one.
func TestStop_UnreachableCollectorIsNotAShutdownFailure(t *testing.T) {
	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")

	svc, err := New(t.Context(), Config{SkipGlobals: true})
	require.NoError(t, err)
	require.NotNil(t, svc.sdkMeterProvider)

	// Nothing is listening on the default OTLP endpoint, so the reader's final
	// export fails. Stop must still report success, and must not spend the OTLP
	// exporter's full 10s default timeout doing it.
	start := time.Now()
	assert.NoError(t, svc.Stop(t.Context()))
	assert.Less(t, time.Since(start), telemetryDrainTimeout+2*time.Second,
		"the metric drain must stay inside telemetryDrainTimeout")
}

// TestMeterProvider_IsReachableThroughIntegrationAPI pins that an integration can
// reach the meter provider without the global one, which is what Config.SkipGlobals
// makes necessary.
func TestMeterProvider_IsReachableThroughIntegrationAPI(t *testing.T) {
	svc, err := New(t.Context(), Config{SkipGlobals: true})
	require.NoError(t, err)

	assert.NotNil(t, MeterProvider(svc))
	assert.NoError(t, svc.Stop(t.Context()))
}

// TestMeterProvider_DropsSuppressedScopes pins that the scopes whose spans are
// suppressed are suppressed from metrics too: gocloud records on the global meter
// the same operations the bucket integration measures.
func TestMeterProvider_DropsSuppressedScopes(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	svc, err := New(t.Context(), Config{
		SkipGlobals:   true,
		MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
	})
	require.NoError(t, err)

	for _, scope := range []string{"gocloud.dev/blob", "probe"} {
		counter, err := svc.meterProvider.Meter(scope).Int64Counter("probe.calls")
		require.NoError(t, err)
		counter.Add(t.Context(), 1)
	}

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))

	var scopes []string
	for _, sm := range rm.ScopeMetrics {
		scopes = append(scopes, sm.Scope.Name)
	}
	assert.Equal(t, []string{"probe"}, scopes)
}
