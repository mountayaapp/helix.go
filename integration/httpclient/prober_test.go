package httpclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// probeEvery has every optional client the test connects probe its upstream every
// interval, until the test ends.
func probeEvery(t *testing.T, interval time.Duration) {
	t.Helper()

	before := reachabilityInterval
	reachabilityInterval = interval
	t.Cleanup(func() { reachabilityInterval = before })
}

// reachableByEndpoint returns the httpclient.reachable gauge of the client named
// name, by the endpoint each point names ("" for none), and nil when the gauge
// reports nothing.
func reachableByEndpoint(t *testing.T, rec *servicetest.Recorder, name string) map[string]int64 {
	t.Helper()

	for _, m := range rec.Metrics() {
		if m.Name != "httpclient.reachable" {
			continue
		}

		gauge, ok := m.Data.(metricdata.Gauge[int64])
		require.True(t, ok, "httpclient.reachable is a %T, not an int64 gauge", m.Data)

		out := map[string]int64{}
		for _, point := range gauge.DataPoints {
			if got, _ := point.Attributes.Value(integration.Key(identifier, "name")); got.AsString() != name {
				continue
			}

			endpoint, _ := point.Attributes.Value(attrKeyEndpoint)
			out[endpoint.AsString()] = point.Value
		}

		if len(out) == 0 {
			return nil
		}

		return out
	}

	return nil
}

// silentUpstream returns the base URL of a server that accepts every connection
// and never answers: an upstream that hangs.
func silentUpstream(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	return "http://" + listener.Addr().String()
}

// An optional client never holds a readiness probe: against an upstream that
// hangs, its health check answers at once, healthy, where a client that is not
// optional would wait out its Timeout.
func TestOptional_CheckHealthAnswersAtOnceAgainstASilentUpstream(t *testing.T) {
	svc, _ := servicetest.New(t)

	_, err := Connect(t.Context(), svc, Config{Name: "fleet", Endpoints: []string{silentUpstream(t)}, HealthPath: "/health", Timeout: 10 * time.Second, Optional: true})
	require.NoError(t, err)

	probe, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	started := time.Now()
	require.NoError(t, svc.CheckHealth(probe))
	assert.Less(t, time.Since(started), 100*time.Millisecond, "the readiness check waited on the silent upstream")
}

// An optional client's background probe keeps the httpclient.reachable gauge
// current for each endpoint, following an upstream that goes down and comes back,
// and stops once the client is closed.
func TestOptional_TheGaugeFollowsEachEndpoint(t *testing.T) {
	probeEvery(t, 5*time.Millisecond)

	svc, rec := servicetest.New(t)

	var failing atomic.Bool
	flaky := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		if failing.Load() {
			rw.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		rw.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(flaky.Close)

	steady := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) { rw.WriteHeader(http.StatusOK) }))
	t.Cleanup(steady.Close)

	client, err := Connect(t.Context(), svc, Config{Name: "fleet", Endpoints: []string{flaky.URL, steady.URL}, HealthPath: "/health", Optional: true})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		got := reachableByEndpoint(t, rec, "fleet")
		return got[flaky.URL] == 1 && got[steady.URL] == 1
	}, 2*time.Second, 5*time.Millisecond, "the gauge never reported both endpoints answering")

	failing.Store(true)
	assert.Eventually(t, func() bool {
		got := reachableByEndpoint(t, rec, "fleet")
		return got[flaky.URL] == 0 && got[steady.URL] == 1
	}, 2*time.Second, 5*time.Millisecond, "the gauge never followed the endpoint down")

	failing.Store(false)
	assert.Eventually(t, func() bool {
		return reachableByEndpoint(t, rec, "fleet")[flaky.URL] == 1
	}, 2*time.Second, 5*time.Millisecond, "the gauge never followed the endpoint back")

	conn := client.(*connection)
	require.NoError(t, dependency{conn: conn}.Close(t.Context()))

	select {
	case <-conn.prober.done:
	case <-time.After(time.Second):
		t.Fatal("the background probe still runs after Close")
	}
	assert.Nil(t, reachableByEndpoint(t, rec, "fleet"), "a closed client still reports its upstream")
}

// An optional client with a custom HealthCheck reports the client as a whole, as
// the check decides it.
func TestOptional_ACustomCheckIsReportedForTheClient(t *testing.T) {
	probeEvery(t, 5*time.Millisecond)

	svc, rec := servicetest.New(t)

	var down atomic.Bool
	_, err := Connect(t.Context(), svc, Config{
		Name:      "fleet",
		Endpoints: []string{"http://127.0.0.1:1"},
		Optional:  true,
		HealthCheck: func(context.Context, HTTPClient) error {
			if down.Load() {
				return errors.New("down")
			}

			return nil
		},
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return reachableByEndpoint(t, rec, "fleet")[""] == 1
	}, 2*time.Second, 5*time.Millisecond)

	down.Store(true)
	assert.Eventually(t, func() bool {
		return reachableByEndpoint(t, rec, "fleet")[""] == 0
	}, 2*time.Second, 5*time.Millisecond)
	require.NoError(t, svc.CheckHealth(t.Context()), "an optional client is healthy whatever its check says")
}

// The first probe comes an interval after the client is attached, not at it, and a
// client closed before then probes nothing.
func TestOptional_TheFirstProbeWaitsAnInterval(t *testing.T) {
	probeEvery(t, 300*time.Millisecond)

	svc, _ := servicetest.New(t)

	var probes, closedProbes atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/closed") {
			closedProbes.Add(1)
		} else {
			probes.Add(1)
		}
		rw.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	_, err := Connect(t.Context(), svc, Config{Name: "fleet", Endpoints: []string{upstream.URL}, HealthPath: "/health", Optional: true})
	require.NoError(t, err)

	closed, err := Connect(t.Context(), svc, Config{Name: "closed", Endpoints: []string{upstream.URL + "/closed"}, HealthPath: "/health", Optional: true})
	require.NoError(t, err)
	require.NoError(t, dependency{conn: closed.(*connection)}.Close(t.Context()))

	time.Sleep(100 * time.Millisecond)
	assert.Zero(t, probes.Load(), "a probe was sent before an interval passed")

	assert.Eventually(t, func() bool { return probes.Load() >= 2 }, 3*time.Second, 5*time.Millisecond, "the probes never came")
	assert.Zero(t, closedProbes.Load(), "the client closed before its first interval probed its upstream")
}

// The default is unchanged: a client that is not optional gates readiness on its
// health path, probes nothing in the background, and reports no gauge.
func TestOptional_TheDefaultIsUnchanged(t *testing.T) {
	probeEvery(t, 5*time.Millisecond)

	svc, rec := servicetest.New(t)
	down, _ := newStatusServer(t, http.StatusServiceUnavailable)

	client, err := Connect(t.Context(), svc, Config{Name: "fleet", Endpoints: []string{down}, HealthPath: "/health"})
	require.NoError(t, err)
	require.Error(t, svc.CheckHealth(t.Context()))

	time.Sleep(30 * time.Millisecond)
	assert.Nil(t, client.(*connection).prober)
	assert.Nil(t, reachableByEndpoint(t, rec, "fleet"))
}
