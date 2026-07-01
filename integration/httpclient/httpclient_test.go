package httpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	// Disable the OpenTelemetry SDK so spans become no-ops and service.New does
	// not create real exporters.
	os.Setenv("OTEL_SDK_DISABLED", "true")
}

// newServer starts an httptest server that counts the requests it receives.
func newServer(t *testing.T, handler http.HandlerFunc) (string, *atomic.Int64) {
	t.Helper()

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		handler(rw, req)
	}))
	t.Cleanup(srv.Close)

	return srv.URL, &hits
}

// newStatusServer starts a server that always responds with the given status.
func newStatusServer(t *testing.T, status int) (string, *atomic.Int64) {
	t.Helper()

	return newServer(t, func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(status)
	})
}

// deadURL returns the URL of a server that has already been closed, so any
// request to it fails with a transport error.
func deadURL(t *testing.T) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	return url
}

// newConn builds a *connection directly, bypassing Connect (and therefore the
// Service singleton) so request and Status logic can be tested in isolation.
func newConn(endpoints []string, mutators ...func(*Config)) *connection {
	cfg := Config{
		Name:       "test",
		Endpoints:  endpoints,
		Timeout:    10 * time.Second,
		HealthPath: "/health",
	}
	for _, mutate := range mutators {
		mutate(&cfg)
	}

	return &connection{
		config:    &cfg,
		client:    &http.Client{Timeout: cfg.Timeout},
		endpoints: cfg.Endpoints,
		single:    len(cfg.Endpoints) == 1,
	}
}

func TestDo_SingleGet(t *testing.T) {
	url, hits := newServer(t, func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("ok"))
	})
	conn := newConn([]string{url})

	resp, err := conn.Get(context.Background(), "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok", string(body))
	assert.Equal(t, int64(1), hits.Load())
}

func TestDo_PostSendsMethodPathBody(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Method", req.Method)
		rw.Header().Set("X-Echo-Path", req.URL.Path)
		body, _ := io.ReadAll(req.Body)
		_, _ = rw.Write(body)
	})
	conn := newConn([]string{url})

	resp, err := conn.Do(context.Background(), http.MethodPost, "/v1/things", []byte(`{"a":1}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	echoed, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.MethodPost, resp.Header.Get("X-Echo-Method"))
	assert.Equal(t, "/v1/things", resp.Header.Get("X-Echo-Path"))
	assert.Equal(t, `{"a":1}`, string(echoed))
}

func TestDo_AppliesDefaultHeaders(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Auth", req.Header.Get("Authorization"))
		rw.WriteHeader(http.StatusOK)
	})
	conn := newConn([]string{url}, func(cfg *Config) {
		cfg.Headers = map[string]string{"Authorization": "Bearer token"}
	})

	resp, err := conn.Get(context.Background(), "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "Bearer token", resp.Header.Get("X-Echo-Auth"))
}

func TestDo_WithHeaderSetsHeader(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Auth", req.Header.Get("Authorization"))
		rw.WriteHeader(http.StatusOK)
	})
	conn := newConn([]string{url})

	resp, err := conn.Get(context.Background(), "/", WithHeader("Authorization", "Bearer token"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "Bearer token", resp.Header.Get("X-Echo-Auth"))
}

func TestDo_WithHeaderOverridesDefault(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Auth", req.Header.Get("Authorization"))
		rw.WriteHeader(http.StatusOK)
	})
	conn := newConn([]string{url}, func(cfg *Config) {
		cfg.Headers = map[string]string{"Authorization": "Bearer default"}
	})

	resp, err := conn.Get(context.Background(), "/", WithHeader("Authorization", "Bearer override"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "Bearer override", resp.Header.Get("X-Echo-Auth"))
}

func TestDo_NoOptionsLeavesHeadersUntouched(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Auth", req.Header.Get("Authorization"))
		rw.WriteHeader(http.StatusOK)
	})
	conn := newConn([]string{url})

	resp, err := conn.Get(context.Background(), "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Empty(t, resp.Header.Get("X-Echo-Auth"))
}

func TestDo_WithHeaderForwardedAcrossFailover(t *testing.T) {
	url0, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Auth", req.Header.Get("Authorization"))
		rw.WriteHeader(http.StatusInternalServerError)
	})
	url1, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Auth", req.Header.Get("Authorization"))
		rw.WriteHeader(http.StatusOK)
	})
	conn := newConn([]string{url0, url1})

	resp, err := conn.Do(context.Background(), http.MethodGet, "/", nil, WithHeader("Authorization", "Bearer token"))
	require.NoError(t, err)
	defer resp.Body.Close()

	// The healthy endpoint reached after failover still saw the per-request header.
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Bearer token", resp.Header.Get("X-Echo-Auth"))
}

func TestDo_RoundRobinDistribution(t *testing.T) {
	urls := make([]string, 3)
	counters := make([]*atomic.Int64, 3)
	for i := range urls {
		body := strconv.Itoa(i)
		urls[i], counters[i] = newServer(t, func(rw http.ResponseWriter, _ *http.Request) {
			_, _ = rw.Write([]byte(body))
		})
	}
	conn := newConn(urls)

	var served []string
	for range 6 {
		resp, err := conn.Get(context.Background(), "/")
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		served = append(served, string(body))
	}

	assert.Equal(t, []string{"0", "1", "2", "0", "1", "2"}, served)
	for i := range counters {
		assert.Equal(t, int64(2), counters[i].Load())
	}
}

func TestDo_FailoverOn5xx(t *testing.T) {
	url0, hits0 := newStatusServer(t, http.StatusInternalServerError)
	url1, hits1 := newServer(t, func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("up"))
	})
	conn := newConn([]string{url0, url1})

	resp, err := conn.Get(context.Background(), "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "up", string(body))
	assert.Equal(t, int64(1), hits0.Load())
	assert.Equal(t, int64(1), hits1.Load())
}

func TestDo_FailoverOnTransportError(t *testing.T) {
	url1, hits1 := newStatusServer(t, http.StatusOK)
	conn := newConn([]string{deadURL(t), url1})

	resp, err := conn.Get(context.Background(), "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int64(1), hits1.Load())
}

func TestDo_FailoverMixedRetryable(t *testing.T) {
	url0, _ := newStatusServer(t, http.StatusInternalServerError)
	url2, hits2 := newStatusServer(t, http.StatusOK)
	conn := newConn([]string{url0, deadURL(t), url2})

	resp, err := conn.Get(context.Background(), "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int64(1), hits2.Load())
}

func TestDo_4xxIsNotRetried(t *testing.T) {
	url0, hits0 := newStatusServer(t, http.StatusNotFound)
	url1, hits1 := newStatusServer(t, http.StatusOK)
	conn := newConn([]string{url0, url1})

	resp, err := conn.Get(context.Background(), "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, int64(1), hits0.Load())
	assert.Equal(t, int64(0), hits1.Load())
}

func TestDo_AllReturn5xxReturnsLastResponse(t *testing.T) {
	url0, hits0 := newStatusServer(t, http.StatusInternalServerError)
	url1, hits1 := newStatusServer(t, http.StatusServiceUnavailable)
	conn := newConn([]string{url0, url1})

	resp, err := conn.Get(context.Background(), "/")
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, int64(1), hits0.Load())
	assert.Equal(t, int64(1), hits1.Load())
}

func TestDo_5xxThenTransportErrorReturnsResponse(t *testing.T) {
	url0, hits0 := newStatusServer(t, http.StatusServiceUnavailable)
	conn := newConn([]string{url0, deadURL(t)})

	resp, err := conn.Get(context.Background(), "/")
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	// A trailing transport error must not discard the 5xx response retained from an
	// earlier endpoint: the contract surfaces the last 5xx when there is one.
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, int64(1), hits0.Load())
}

func TestDo_AllUnreachableReturnsError(t *testing.T) {
	conn := newConn([]string{deadURL(t), deadURL(t)})

	resp, err := conn.Get(context.Background(), "/")
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestDo_BodyReplayedAcrossFailover(t *testing.T) {
	url0, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		_, _ = io.ReadAll(req.Body)
		rw.WriteHeader(http.StatusInternalServerError)
	})
	url1, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write(body)
	})
	conn := newConn([]string{url0, url1})

	resp, err := conn.Do(context.Background(), http.MethodPost, "/", []byte("hello"))
	require.NoError(t, err)
	defer resp.Body.Close()

	echoed, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "hello", string(echoed))
}

func TestDo_SingleEndpoint5xxReturnedAsIs(t *testing.T) {
	url, hits := newStatusServer(t, http.StatusBadGateway)
	conn := newConn([]string{url})

	resp, err := conn.Get(context.Background(), "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Equal(t, int64(1), hits.Load())
	assert.Equal(t, uint64(0), conn.index.Load()) // fast path leaves the cursor untouched
}

func TestGet_PreservesQueryString(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Path", req.URL.Path)
		rw.Header().Set("X-Echo-Query", req.URL.RawQuery)
		rw.WriteHeader(http.StatusOK)
	})
	conn := newConn([]string{url})

	resp, err := conn.Get(context.Background(), "/search?q=1&l=fr")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "/search", resp.Header.Get("X-Echo-Path"))
	assert.Equal(t, "q=1&l=fr", resp.Header.Get("X-Echo-Query"))
}

func TestDo_ContextCancelledBeforeRequest(t *testing.T) {
	url0, hits0 := newStatusServer(t, http.StatusOK)
	url1, hits1 := newStatusServer(t, http.StatusOK)
	conn := newConn([]string{url0, url1})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resp, err := conn.Do(ctx, http.MethodGet, "/", nil)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, int64(0), hits0.Load())
	assert.Equal(t, int64(0), hits1.Load())
}

func TestDo_ContextDeadlineStopsFailover(t *testing.T) {
	url0, _ := newServer(t, func(rw http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		rw.WriteHeader(http.StatusInternalServerError)
	})
	url1, hits1 := newStatusServer(t, http.StatusOK)
	conn := newConn([]string{url0, url1})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	resp, err := conn.Do(ctx, http.MethodGet, "/", nil)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, int64(0), hits1.Load()) // second endpoint never reached
}

func TestDo_RespectsContextDeadline(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		rw.WriteHeader(http.StatusOK)
	})
	conn := newConn([]string{url})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := conn.Get(ctx, "/")
	assert.Error(t, err)
	assert.Less(t, time.Since(start), 150*time.Millisecond)
}

func TestDo_RespectsConfigTimeout(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		rw.WriteHeader(http.StatusOK)
	})
	conn := newConn([]string{url}, func(cfg *Config) { cfg.Timeout = 50 * time.Millisecond })

	_, err := conn.Get(context.Background(), "/")
	assert.Error(t, err)
}

func TestDo_ConcurrentCallers(t *testing.T) {
	urls := make([]string, 3)
	counters := make([]*atomic.Int64, 3)
	for i := range urls {
		urls[i], counters[i] = newStatusServer(t, http.StatusOK)
	}
	conn := newConn(urls)

	const calls = 60
	var wg sync.WaitGroup
	for range calls {
		wg.Go(func() {
			resp, err := conn.Get(context.Background(), "/")
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		})
	}
	wg.Wait()

	var total int64
	for _, counter := range counters {
		total += counter.Load()
	}
	assert.Equal(t, int64(calls), total)
}

func TestStatus_SingleHealthy(t *testing.T) {
	url, _ := newStatusServer(t, http.StatusOK)
	conn := newConn([]string{url})

	status, err := conn.Status(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
}

func TestStatus_SingleUnhealthy(t *testing.T) {
	url, _ := newStatusServer(t, http.StatusInternalServerError)
	conn := newConn([]string{url})

	status, err := conn.Status(context.Background())
	require.Error(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Contains(t, err.Error(), url)

	var es *errorstack.Error
	require.ErrorAs(t, err, &es)
	assert.Equal(t, errorstack.CodeServiceUnavailable, es.Entries[0].Extensions["code"])
}

func TestStatus_SingleUnreachable(t *testing.T) {
	url := deadURL(t)
	conn := newConn([]string{url})

	status, err := conn.Status(context.Background())
	require.Error(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Contains(t, err.Error(), "is not reachable")
	assert.Contains(t, err.Error(), url)
}

func TestStatus_MultipleAllHealthy(t *testing.T) {
	u0, _ := newStatusServer(t, http.StatusOK)
	u1, _ := newStatusServer(t, http.StatusOK)
	u2, _ := newStatusServer(t, http.StatusOK)
	conn := newConn([]string{u0, u1, u2})

	status, err := conn.Status(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
}

func TestStatus_MultipleOneDownNamesIt(t *testing.T) {
	up1, _ := newStatusServer(t, http.StatusOK)
	up2, _ := newStatusServer(t, http.StatusOK)
	down, _ := newStatusServer(t, http.StatusServiceUnavailable)
	conn := newConn([]string{up1, up2, down})

	status, err := conn.Status(context.Background())
	require.Error(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Contains(t, err.Error(), down)
	assert.NotContains(t, err.Error(), up1)
	assert.NotContains(t, err.Error(), up2)
}

func TestStatus_MultipleAllDown(t *testing.T) {
	d0, _ := newStatusServer(t, http.StatusInternalServerError)
	d1, _ := newStatusServer(t, http.StatusInternalServerError)
	d2, _ := newStatusServer(t, http.StatusInternalServerError)
	conn := newConn([]string{d0, d1, d2})

	status, err := conn.Status(context.Background())
	require.Error(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, status)
	for _, url := range []string{d0, d1, d2} {
		assert.Contains(t, err.Error(), url)
	}
}

func TestStatus_ProbesEveryEndpointOnce(t *testing.T) {
	u0, h0 := newStatusServer(t, http.StatusOK)
	u1, h1 := newStatusServer(t, http.StatusOK)
	u2, h2 := newStatusServer(t, http.StatusOK)
	conn := newConn([]string{u0, u1, u2})

	_, err := conn.Status(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), h0.Load())
	assert.Equal(t, int64(1), h1.Load())
	assert.Equal(t, int64(1), h2.Load())
}

func TestStatus_UsesCustomHealthPath(t *testing.T) {
	// The wrong path returns 5xx, so a healthy result proves /ready was probed.
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/ready" {
			rw.WriteHeader(http.StatusOK)
			return
		}
		rw.WriteHeader(http.StatusInternalServerError)
	})
	conn := newConn([]string{url}, func(cfg *Config) { cfg.HealthPath = "/ready" })

	status, err := conn.Status(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
}

func TestStatus_NoHealthEndpointIsHealthy(t *testing.T) {
	// An API with no health endpoint returns 404 for the probe; it is reachable
	// and not erroring, so it is healthy.
	url, _ := newStatusServer(t, http.StatusNotFound)
	conn := newConn([]string{url})

	status, err := conn.Status(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
}

func TestStatus_CustomCallbackOverridesDefault(t *testing.T) {
	url, hits := newStatusServer(t, http.StatusInternalServerError)
	var called atomic.Bool
	conn := newConn([]string{url}, func(cfg *Config) {
		cfg.Status = func(context.Context, HTTPClient) (int, error) {
			called.Store(true)
			return http.StatusOK, nil
		}
	})

	status, err := conn.Status(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.True(t, called.Load())
	assert.Equal(t, int64(0), hits.Load()) // default probe skipped
}

func TestStatus_CustomCallbackUsesClient(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/custom" {
			rw.WriteHeader(http.StatusOK)
			return
		}
		rw.WriteHeader(http.StatusInternalServerError)
	})
	conn := newConn([]string{url}, func(cfg *Config) {
		cfg.Status = func(ctx context.Context, client HTTPClient) (int, error) {
			resp, err := client.Get(ctx, "/custom")
			if err != nil {
				return http.StatusServiceUnavailable, err
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return http.StatusOK, nil
			}
			return http.StatusServiceUnavailable, errors.New("unhealthy")
		}
	})

	status, err := conn.Status(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
}

func TestStatus_ProbesConcurrently(t *testing.T) {
	newSlow := func() string {
		url, _ := newServer(t, func(rw http.ResponseWriter, _ *http.Request) {
			time.Sleep(80 * time.Millisecond)
			rw.WriteHeader(http.StatusOK)
		})
		return url
	}
	conn := newConn([]string{newSlow(), newSlow(), newSlow()})

	start := time.Now()
	status, err := conn.Status(context.Background())
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.Less(t, elapsed, 200*time.Millisecond) // concurrent (~80ms), not sequential (~240ms)
}

var (
	testServiceOnce sync.Once
	testService     *service.Service
	testServiceErr  error
)

// sharedService returns a process-wide Service. Only one Service can be created
// per process, so it is built once and reused across tests.
func sharedService(t *testing.T) *service.Service {
	t.Helper()

	testServiceOnce.Do(func() {
		testService, testServiceErr = service.New()
	})
	require.NoError(t, testServiceErr)
	require.NotNil(t, testService)

	return testService
}

func TestConnect_AttachesHealthyClient(t *testing.T) {
	svc := sharedService(t)
	url, _ := newStatusServer(t, http.StatusOK)

	client, err := Connect(svc, Config{Name: "connect-healthy", Endpoints: []string{url}})
	require.NoError(t, err)
	require.NotNil(t, client)

	conn, ok := client.(*connection)
	require.True(t, ok)
	status, err := conn.Status(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
}

func TestConnect_InvalidConfigIsNotAttached(t *testing.T) {
	svc := sharedService(t)

	client, err := Connect(svc, Config{Name: "no-endpoints"})
	assert.Error(t, err)
	assert.Nil(t, client)
}

func TestConnect_InvalidTLSReturnsError(t *testing.T) {
	svc := sharedService(t)

	client, err := Connect(svc, Config{
		Name:      "bad-tls",
		Endpoints: []string{"https://api.tld"},
		TLS:       integration.ConfigTLS{Enabled: true, CertPEM: []byte("cert")},
	})
	assert.Error(t, err)
	assert.Nil(t, client)
}

func TestConnect_ParticipatesInServiceStatus(t *testing.T) {
	svc := sharedService(t)
	down, _ := newStatusServer(t, http.StatusServiceUnavailable)

	_, err := Connect(svc, Config{Name: "svc-status-down", Endpoints: []string{down}})
	require.NoError(t, err)

	status, err := svc.Status(context.Background())
	assert.Error(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, status)
}

func TestConnection_NameAndClose(t *testing.T) {
	conn := newConn([]string{"https://api.tld"}, func(cfg *Config) { cfg.Name = "named" })

	assert.Equal(t, "named", conn.Name())
	assert.NoError(t, conn.Close(context.Background()))
}
