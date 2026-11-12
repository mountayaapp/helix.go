package httpclient

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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
// request to it fails to connect.
func deadURL(t *testing.T) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	return url
}

// newConn builds a *connection directly, bypassing Connect — and therefore the
// Service it would need — so that the request, failover and health logic can be
// tested in isolation.
//
// It is wired the way Connect wires it, instrumented transport and redirect
// policy included: a bare http.Client here would let these tests pass while the
// spans and the safeguards they are supposed to exercise were never there.
func newConn(tb testing.TB, endpoints []string, mutators ...func(*Config)) *connection {
	tb.Helper()

	cfg := Config{
		Name:      "test",
		Endpoints: endpoints,
	}
	for _, mutate := range mutators {
		mutate(&cfg)
	}

	require.NoError(tb, cfg.sanitize())

	conn, err := newConnection(&cfg, integration.InstrumentConfig{
		Identifier: identifier,
		Humanized:  humanized,
		Name:       cfg.name(),
	})
	require.NoError(tb, err)
	tb.Cleanup(conn.client.CloseIdleConnections)

	return conn
}

// withHealthPath turns default probing on.
func withHealthPath(path string) func(*Config) {
	return func(cfg *Config) {
		cfg.HealthPath = path
	}
}

func TestDo_SingleGet(t *testing.T) {
	url, hits := newServer(t, func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("ok"))
	})
	conn := newConn(t, []string{url})

	resp, err := conn.Get(t.Context(), "/")
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
	conn := newConn(t, []string{url})

	resp, err := conn.Do(t.Context(), http.MethodPost, "/v1/things", []byte(`{"a":1}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	echoed, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.MethodPost, resp.Header.Get("X-Echo-Method"))
	assert.Equal(t, "/v1/things", resp.Header.Get("X-Echo-Path"))
	assert.Equal(t, `{"a":1}`, string(echoed))
}

func TestDo_PathIsAppendedToTheEndpointBasePath(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Path", req.URL.EscapedPath())
		rw.Header().Set("X-Echo-Query", req.URL.RawQuery)
		rw.WriteHeader(http.StatusOK)
	})
	conn := newConn(t, []string{url + "/v1/"})

	testcases := []struct {
		name  string
		path  string
		want  string
		query string
	}{
		{name: "absolute path", path: "/things", want: "/v1/things"},
		{name: "path with query", path: "/things?limit=10", want: "/v1/things", query: "limit=10"},
		{name: "query only", path: "?limit=10", want: "/v1", query: "limit=10"},
		{name: "empty", path: "", want: "/v1"},
		{name: "escaped segment", path: "/files/a%2Fb", want: "/v1/files/a%2Fb"},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := conn.Get(t.Context(), tc.path)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tc.want, resp.Header.Get("X-Echo-Path"))
			assert.Equal(t, tc.query, resp.Header.Get("X-Echo-Query"))
		})
	}
}

// TestDo_RejectsPathsLeavingTheHost pins the fix for host injection: a path used
// to be concatenated to the endpoint, so "@other.tld/x" sent the request — and the
// default Authorization header — to other.tld.
func TestDo_RejectsPathsLeavingTheHost(t *testing.T) {
	url, hits := newStatusServer(t, http.StatusOK)
	otherURL, otherHits := newStatusServer(t, http.StatusOK)
	other := strings.TrimPrefix(otherURL, "http://")

	conn := newConn(t, []string{url}, func(cfg *Config) {
		cfg.Headers = map[string]string{"Authorization": "Bearer secret"}
	})

	for _, path := range []string{
		"@" + other + "/x",
		"." + other,
		":1",
		":1/x",
		other + "/x",
		"//" + other + "/x",
		otherURL + "/x",
		"https://" + other,
	} {
		t.Run(path, func(t *testing.T) {
			resp, err := conn.Get(t.Context(), path)
			defer drainClose(resp)

			require.Error(t, err)
			assert.Nil(t, resp)
		})
	}

	assert.Equal(t, int64(0), hits.Load())
	assert.Equal(t, int64(0), otherHits.Load())
}

func TestDoRequest_RejectsAbsoluteURL(t *testing.T) {
	url, hits := newStatusServer(t, http.StatusOK)
	otherURL, otherHits := newStatusServer(t, http.StatusOK)
	conn := newConn(t, []string{url})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, otherURL+"/x", nil)
	require.NoError(t, err)

	resp, err := conn.DoRequest(req)
	defer drainClose(resp)

	require.Error(t, err)
	assert.Equal(t, int64(0), hits.Load())
	assert.Equal(t, int64(0), otherHits.Load())
}

func TestDoRequest_StreamsBody(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Auth", req.Header.Get("Authorization"))
		rw.Header().Set("X-Echo-Type", req.Header.Get("Content-Type"))
		_, _ = io.Copy(rw, req.Body)
	})
	conn := newConn(t, []string{url}, func(cfg *Config) {
		cfg.Headers = map[string]string{"Authorization": "Bearer default"}
	})

	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("streamed"))
		_ = pw.Close()
	}()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "/upload", pr)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "text/plain")

	resp, err := conn.DoRequest(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	echoed, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "streamed", string(echoed))
	assert.Equal(t, "Bearer default", resp.Header.Get("X-Echo-Auth"))
	assert.Equal(t, "text/plain", resp.Header.Get("X-Echo-Type"))

	// The caller's request is not modified: the default header went on a clone.
	assert.Empty(t, req.Header.Get("Authorization"))
}

func TestDoRequest_UnreplayableBodyIsNotFailedOver(t *testing.T) {
	url0, hits0 := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		rw.WriteHeader(http.StatusInternalServerError)
	})
	url1, hits1 := newStatusServer(t, http.StatusOK)
	conn := newConn(t, []string{url0, url1}, func(cfg *Config) { cfg.Failover = FailoverAlways })

	// An io.Pipe has no GetBody: once sent, it cannot be produced again.
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("once"))
		_ = pw.Close()
	}()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "/", pr)
	require.NoError(t, err)

	resp, err := conn.DoRequest(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, int64(1), hits0.Load())
	assert.Equal(t, int64(0), hits1.Load())
}

func TestDo_AppliesDefaultHeaders(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Auth", req.Header.Get("Authorization"))
		rw.Header().Set("X-Echo-Key", req.Header.Get("X-Api-Key"))
		rw.WriteHeader(http.StatusOK)
	})
	conn := newConn(t, []string{url}, func(cfg *Config) {
		cfg.Headers = map[string]string{
			"Authorization": "Bearer token",
			"x-api-key":     "key",
		}
	})

	resp, err := conn.Get(t.Context(), "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "Bearer token", resp.Header.Get("X-Echo-Auth"))
	assert.Equal(t, "key", resp.Header.Get("X-Echo-Key"))
}

// TestConnect_HeadersAreCopied pins that the client keeps a copy of Headers:
// it used to read the caller's own map on every attempt, so a caller writing to it
// afterwards raced every request in flight.
func TestConnect_HeadersAreCopied(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Key", req.Header.Get("X-Api-Key"))
		rw.WriteHeader(http.StatusOK)
	})

	headers := map[string]string{"X-Api-Key": "before"}
	client, err := Connect(t.Context(), newService(t), Config{Endpoints: []string{url}, Headers: headers})
	require.NoError(t, err)

	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 100 {
			headers["X-Api-Key"] = "after-" + strconv.Itoa(i)
		}
	})

	for range 10 {
		resp, err := client.Get(t.Context(), "/")
		require.NoError(t, err)
		assert.Equal(t, "before", resp.Header.Get("X-Echo-Key"))
		drainClose(resp)
	}

	wg.Wait()
}

func TestDo_WithHeaderSetsHeader(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Auth", req.Header.Get("Authorization"))
		rw.WriteHeader(http.StatusOK)
	})
	conn := newConn(t, []string{url})

	resp, err := conn.Get(t.Context(), "/", WithHeader("Authorization", "Bearer token"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "Bearer token", resp.Header.Get("X-Echo-Auth"))
}

func TestDo_WithHeaderOverridesDefault(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Auth", req.Header.Get("Authorization"))
		rw.WriteHeader(http.StatusOK)
	})
	conn := newConn(t, []string{url}, func(cfg *Config) {
		cfg.Headers = map[string]string{"Authorization": "Bearer default"}
	})

	resp, err := conn.Get(t.Context(), "/", WithHeader("Authorization", "Bearer override"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "Bearer override", resp.Header.Get("X-Echo-Auth"))
}

func TestDo_NoOptionsLeavesHeadersUntouched(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Auth", req.Header.Get("Authorization"))
		rw.WriteHeader(http.StatusOK)
	})
	conn := newConn(t, []string{url})

	resp, err := conn.Get(t.Context(), "/")
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
	conn := newConn(t, []string{url0, url1})

	resp, err := conn.Do(t.Context(), http.MethodGet, "/", nil, WithHeader("Authorization", "Bearer token"))
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
	conn := newConn(t, urls)

	var served []string
	for range 6 {
		resp, err := conn.Get(t.Context(), "/")
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
	conn := newConn(t, []string{url0, url1})

	resp, err := conn.Get(t.Context(), "/")
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
	conn := newConn(t, []string{deadURL(t), url1})

	resp, err := conn.Get(t.Context(), "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int64(1), hits1.Load())
}

func TestDo_FailoverMixedRetryable(t *testing.T) {
	url0, _ := newStatusServer(t, http.StatusInternalServerError)
	url2, hits2 := newStatusServer(t, http.StatusOK)
	conn := newConn(t, []string{url0, deadURL(t), url2})

	resp, err := conn.Get(t.Context(), "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int64(1), hits2.Load())
}

func TestDo_4xxIsNotRetried(t *testing.T) {
	url0, hits0 := newStatusServer(t, http.StatusNotFound)
	url1, hits1 := newStatusServer(t, http.StatusOK)
	conn := newConn(t, []string{url0, url1})

	resp, err := conn.Get(t.Context(), "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, int64(1), hits0.Load())
	assert.Equal(t, int64(0), hits1.Load())
}

func TestDo_AllReturn5xxReturnsLastResponse(t *testing.T) {
	url0, hits0 := newStatusServer(t, http.StatusInternalServerError)
	url1, hits1 := newStatusServer(t, http.StatusServiceUnavailable)
	conn := newConn(t, []string{url0, url1})

	resp, err := conn.Get(t.Context(), "/")
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, int64(1), hits0.Load())
	assert.Equal(t, int64(1), hits1.Load())
}

func TestDo_5xxThenTransportErrorReturnsResponse(t *testing.T) {
	url0, hits0 := newStatusServer(t, http.StatusServiceUnavailable)
	conn := newConn(t, []string{url0, deadURL(t)})

	resp, err := conn.Get(t.Context(), "/")
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	// A trailing transport error must not discard the 5xx response retained from an
	// earlier endpoint: the contract surfaces the last 5xx when there is one.
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, int64(1), hits0.Load())
}

func TestDo_AllUnreachableReturnsError(t *testing.T) {
	conn := newConn(t, []string{deadURL(t), deadURL(t)})

	resp, err := conn.Get(t.Context(), "/")
	defer drainClose(resp)

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
	conn := newConn(t, []string{url0, url1})

	resp, err := conn.Do(t.Context(), http.MethodPut, "/", []byte("hello"))
	require.NoError(t, err)
	defer resp.Body.Close()

	echoed, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "hello", string(echoed))
}

// TestDo_PostIsNotFailedOverOn5xx pins the fix for duplicated side effects: a
// POST used to be sent again to the next endpoint after a 5xx, which the first
// one may well have acted on before failing.
func TestDo_PostIsNotFailedOverOn5xx(t *testing.T) {
	url0, hits0 := newStatusServer(t, http.StatusInternalServerError)
	url1, hits1 := newStatusServer(t, http.StatusOK)
	conn := newConn(t, []string{url0, url1})

	resp, err := conn.Do(t.Context(), http.MethodPost, "/", []byte("charge"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, int64(1), hits0.Load())
	assert.Equal(t, int64(0), hits1.Load())
}

// TestDo_PostIsNotFailedOverAfterBeingSent covers the other half: a connection
// lost after the request was written may have been lost after the endpoint acted
// on it.
func TestDo_PostIsNotFailedOverAfterBeingSent(t *testing.T) {
	url0, hits0 := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		_, _ = io.ReadAll(req.Body)
		conn, _, err := http.NewResponseController(rw).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	})
	url1, hits1 := newStatusServer(t, http.StatusOK)
	conn := newConn(t, []string{url0, url1})

	resp, err := conn.Do(t.Context(), http.MethodPost, "/", []byte("charge"))
	defer drainClose(resp)

	require.Error(t, err)
	assert.Equal(t, int64(1), hits0.Load())
	assert.Equal(t, int64(0), hits1.Load())
}

func TestDo_PostIsFailedOverWhenNeverSent(t *testing.T) {
	url1, hits1 := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		_, _ = rw.Write(body)
	})
	conn := newConn(t, []string{deadURL(t), url1})

	resp, err := conn.Do(t.Context(), http.MethodPost, "/", []byte("charge"))
	require.NoError(t, err)
	defer resp.Body.Close()

	echoed, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "charge", string(echoed))
	assert.Equal(t, int64(1), hits1.Load())
}

func TestDo_PostWithIdempotencyKeyIsFailedOver(t *testing.T) {
	url0, _ := newStatusServer(t, http.StatusInternalServerError)
	url1, hits1 := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Key", req.Header.Get("Idempotency-Key"))
		rw.WriteHeader(http.StatusCreated)
	})
	conn := newConn(t, []string{url0, url1})

	resp, err := conn.Do(t.Context(), http.MethodPost, "/", []byte("charge"), WithHeader("Idempotency-Key", "k-1"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "k-1", resp.Header.Get("X-Echo-Key"))
	assert.Equal(t, int64(1), hits1.Load())
}

func TestDo_FailoverPolicies(t *testing.T) {
	testcases := []struct {
		name     string
		policy   FailoverPolicy
		method   string
		failover bool
	}{
		{name: "idempotent policy fails a GET over", policy: FailoverIdempotent, method: http.MethodGet, failover: true},
		{name: "idempotent policy keeps a PATCH", policy: FailoverIdempotent, method: http.MethodPatch, failover: false},
		{name: "unsent policy keeps a GET", policy: FailoverUnsent, method: http.MethodGet, failover: false},
		{name: "always policy fails a POST over", policy: FailoverAlways, method: http.MethodPost, failover: true},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			url0, _ := newStatusServer(t, http.StatusBadGateway)
			url1, hits1 := newStatusServer(t, http.StatusOK)
			conn := newConn(t, []string{url0, url1}, func(cfg *Config) { cfg.Failover = tc.policy })

			resp, err := conn.Do(t.Context(), tc.method, "/", []byte("x"))
			require.NoError(t, err)
			defer resp.Body.Close()

			if tc.failover {
				assert.Equal(t, http.StatusOK, resp.StatusCode)
				assert.Equal(t, int64(1), hits1.Load())
			} else {
				assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
				assert.Equal(t, int64(0), hits1.Load())
			}
		})
	}
}

func TestDo_SingleEndpoint5xxReturnedAsIs(t *testing.T) {
	url, hits := newStatusServer(t, http.StatusBadGateway)
	conn := newConn(t, []string{url})

	resp, err := conn.Get(t.Context(), "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Equal(t, int64(1), hits.Load())
	assert.Equal(t, uint64(0), conn.index.Load()) // fast path leaves the cursor untouched
}

func TestDo_RetryAfterPausesEndpoint(t *testing.T) {
	url0, hits0 := newServer(t, func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Retry-After", "60")
		rw.WriteHeader(http.StatusServiceUnavailable)
	})
	url1, hits1 := newStatusServer(t, http.StatusOK)
	conn := newConn(t, []string{url0, url1})

	for range 4 {
		resp, err := conn.Get(t.Context(), "/")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		drainClose(resp)
	}

	// The first call reached the endpoint that asked to be left alone, and was
	// failed over; none after it did.
	assert.Equal(t, int64(1), hits0.Load())
	assert.Equal(t, int64(4), hits1.Load())
	assert.True(t, conn.endpoints[0].paused(time.Now()))
	assert.False(t, conn.endpoints[1].paused(time.Now()))
}

func TestDo_EveryEndpointPausedStillSends(t *testing.T) {
	url0, hits0 := newStatusServer(t, http.StatusOK)
	url1, hits1 := newStatusServer(t, http.StatusOK)
	conn := newConn(t, []string{url0, url1})

	for _, e := range conn.endpoints {
		e.pause(time.Now(), time.Minute)
	}

	resp, err := conn.Get(t.Context(), "/")
	require.NoError(t, err)
	drainClose(resp)

	assert.Equal(t, int64(1), hits0.Load()+hits1.Load())
}

func TestGet_PreservesQueryString(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Path", req.URL.Path)
		rw.Header().Set("X-Echo-Query", req.URL.RawQuery)
		rw.WriteHeader(http.StatusOK)
	})
	conn := newConn(t, []string{url})

	resp, err := conn.Get(t.Context(), "/search?q=1&l=fr")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "/search", resp.Header.Get("X-Echo-Path"))
	assert.Equal(t, "q=1&l=fr", resp.Header.Get("X-Echo-Query"))
}

// TestDo_ErrorsCarryNoQueryString pins the redaction: the error used to name the
// full URL, so an API key passed as a query parameter ended up in every span and
// log line the error reached.
func TestDo_ErrorsCarryNoQueryString(t *testing.T) {
	url := deadURL(t)
	conn := newConn(t, []string{url})

	resp, err := conn.Get(t.Context(), "/things?api_key=secret")
	defer drainClose(resp)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret")
	assert.Contains(t, err.Error(), url+"/things")
}

// TestDo_MalformedLocationCarriesNoQueryValue pins the redaction on the one error
// of net/http that quotes the request outside its URL: an endpoint redirecting
// with the query it was asked with, to a Location that does not parse, is
// reported with that header verbatim. The error named the API key that way even
// with the URL it names redacted.
func TestDo_MalformedLocationCarriesNoQueryValue(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("Location", "/moved/%zz?"+req.URL.RawQuery)
		rw.WriteHeader(http.StatusFound)
	})
	conn := newConn(t, []string{url})

	resp, err := conn.Get(t.Context(), "/things?api_key="+apiToken)
	defer drainClose(resp)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), apiToken)
	assert.Contains(t, err.Error(), "failed to parse Location header: ")
	assert.Contains(t, err.Error(), url+"/things")
}

func TestDo_RedirectOnTheSameHostIsFollowed(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/old" {
			http.Redirect(rw, req, "/new", http.StatusFound)
			return
		}

		rw.Header().Set("X-Echo-Path", req.URL.Path)
		rw.Header().Set("X-Echo-Key", req.Header.Get("X-Api-Key"))
		rw.WriteHeader(http.StatusOK)
	})
	conn := newConn(t, []string{url}, func(cfg *Config) {
		cfg.Headers = map[string]string{"X-Api-Key": "secret"}
	})

	resp, err := conn.Get(t.Context(), "/old")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "/new", resp.Header.Get("X-Echo-Path"))
	assert.Equal(t, "secret", resp.Header.Get("X-Echo-Key"))
}

// TestDo_RedirectToAnotherHostIsNotFollowed pins the default redirect policy: an
// endpoint used to be able to send the client, with its X-Api-Key, anywhere.
func TestDo_RedirectToAnotherHostIsNotFollowed(t *testing.T) {
	otherURL, otherHits := newStatusServer(t, http.StatusOK)
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		http.Redirect(rw, req, otherURL+"/landing", http.StatusFound)
	})
	conn := newConn(t, []string{url}, func(cfg *Config) {
		cfg.Headers = map[string]string{"X-Api-Key": "secret"}
	})

	resp, err := conn.Get(t.Context(), "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, otherURL+"/landing", resp.Header.Get("Location"))
	assert.Equal(t, int64(0), otherHits.Load())
}

func TestDo_FollowRedirectsDoesNotForwardHeaders(t *testing.T) {
	received := make(chan http.Header, 1)
	otherURL, otherHits := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		received <- req.Header.Clone()
		rw.WriteHeader(http.StatusOK)
	})
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		http.Redirect(rw, req, otherURL+"/landing", http.StatusFound)
	})
	conn := newConn(t, []string{url}, func(cfg *Config) {
		cfg.FollowRedirects = true
		cfg.Headers = map[string]string{
			"Authorization": "Bearer secret",
			"X-Api-Key":     "secret",
			"Accept":        "application/json",
		}
	})

	resp, err := conn.Get(t.Context(), "/", WithHeader("X-Per-Request", "secret"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int64(1), otherHits.Load())

	seen := <-received
	assert.Empty(t, seen.Get("Authorization"))
	assert.Empty(t, seen.Get("X-Api-Key"))
	assert.Empty(t, seen.Get("X-Per-Request"))
	assert.Equal(t, "application/json", seen.Get("Accept"))
}

func TestDo_RedirectFromHTTPSToHTTPIsNotFollowed(t *testing.T) {
	plainURL, plainHits := newStatusServer(t, http.StatusOK)
	secure := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		http.Redirect(rw, req, plainURL+"/landing", http.StatusFound)
	}))
	t.Cleanup(secure.Close)

	conn := newConn(t, []string{secure.URL}, func(cfg *Config) {
		cfg.FollowRedirects = true
		cfg.Transport = secure.Client().Transport
		cfg.Headers = map[string]string{"Authorization": "Bearer secret"}
	})

	resp, err := conn.Get(t.Context(), "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, int64(0), plainHits.Load())
}

func TestDo_ContextCancelledBeforeRequest(t *testing.T) {
	url0, hits0 := newStatusServer(t, http.StatusOK)
	url1, hits1 := newStatusServer(t, http.StatusOK)
	conn := newConn(t, []string{url0, url1})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	resp, err := conn.Do(ctx, http.MethodGet, "/", nil)
	defer drainClose(resp)

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
	conn := newConn(t, []string{url0, url1})

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	resp, err := conn.Do(ctx, http.MethodGet, "/", nil)
	defer drainClose(resp)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, int64(0), hits1.Load()) // second endpoint never reached
}

func TestDo_RespectsContextDeadline(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		rw.WriteHeader(http.StatusOK)
	})
	conn := newConn(t, []string{url})

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	resp, err := conn.Get(ctx, "/")
	defer drainClose(resp)

	assert.Error(t, err)
	assert.Less(t, time.Since(start), 150*time.Millisecond)
}

func TestDo_RespectsConfigTimeout(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		rw.WriteHeader(http.StatusOK)
	})
	conn := newConn(t, []string{url}, func(cfg *Config) { cfg.Timeout = 50 * time.Millisecond })

	resp, err := conn.Get(t.Context(), "/")
	defer drainClose(resp)

	assert.Error(t, err)
}

func TestDo_NegativeTimeoutDisablesIt(t *testing.T) {
	conn := newConn(t, []string{"https://api.tld"}, func(cfg *Config) { cfg.Timeout = -1 })

	assert.Equal(t, time.Duration(0), conn.client.Timeout)
	assert.Equal(t, time.Duration(0), conn.probe.client.Timeout)
}

func TestDo_ConcurrentCallers(t *testing.T) {
	urls := make([]string, 3)
	counters := make([]*atomic.Int64, 3)
	for i := range urls {
		urls[i], counters[i] = newStatusServer(t, http.StatusOK)
	}
	conn := newConn(t, urls)

	const calls = 60
	var wg sync.WaitGroup
	for range calls {
		wg.Go(func() {
			resp, err := conn.Get(t.Context(), "/")
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

// TestCheckHealth_OffByDefault pins that probing is opt-in: it used to probe
// /health on every endpoint, so an API outside the caller's own services being
// down made every replica of the caller unready.
func TestCheckHealth_OffByDefault(t *testing.T) {
	url, hits := newStatusServer(t, http.StatusInternalServerError)
	conn := newConn(t, []string{url})

	assert.NoError(t, dependency{conn: conn}.CheckHealth(t.Context()))
	assert.Equal(t, int64(0), hits.Load())
}

func TestCheckHealth_SingleHealthy(t *testing.T) {
	url, _ := newStatusServer(t, http.StatusOK)
	conn := newConn(t, []string{url}, withHealthPath("/health"))

	assert.NoError(t, dependency{conn: conn}.CheckHealth(t.Context()))
}

func TestCheckHealth_SingleUnhealthy(t *testing.T) {
	url, _ := newStatusServer(t, http.StatusInternalServerError)
	conn := newConn(t, []string{url}, withHealthPath("/health"))

	err := dependency{conn: conn}.CheckHealth(t.Context())
	require.Error(t, err)

	var es *errorstack.Error
	require.ErrorAs(t, err, &es)
	require.Len(t, es.Entries, 1)
	assert.Equal(t, "Integration is not in a healthy state", es.Entries[0].Message)
	assert.Equal(t, []any{"endpoints", 0}, es.Entries[0].Path)
	assert.Equal(t, errorstack.CodeServiceUnavailable, es.Entries[0].Extensions["code"])
	assert.Equal(t, "Status 500 Internal Server Error", es.Entries[0].Extensions["reason"])
}

func TestCheckHealth_SingleUnreachable(t *testing.T) {
	url := deadURL(t)
	conn := newConn(t, []string{url}, withHealthPath("/health"))

	err := dependency{conn: conn}.CheckHealth(t.Context())
	require.Error(t, err)

	var es *errorstack.Error
	require.ErrorAs(t, err, &es)
	require.Len(t, es.Entries, 1)
	assert.Equal(t, []any{"endpoints", 0}, es.Entries[0].Path)
	assert.Equal(t, errorstack.CodeServiceUnavailable, es.Entries[0].Extensions["code"])

	// The reason, for the log and the span, still says what went wrong where.
	assert.Contains(t, es.Entries[0].Extensions["reason"], url+"/health")
}

func TestCheckHealth_MultipleAllHealthy(t *testing.T) {
	u0, _ := newStatusServer(t, http.StatusOK)
	u1, _ := newStatusServer(t, http.StatusOK)
	u2, _ := newStatusServer(t, http.StatusOK)
	conn := newConn(t, []string{u0, u1, u2}, withHealthPath("/health"))

	assert.NoError(t, dependency{conn: conn}.CheckHealth(t.Context()))
}

func TestCheckHealth_MultipleOneDownNamesIt(t *testing.T) {
	up1, _ := newStatusServer(t, http.StatusOK)
	up2, _ := newStatusServer(t, http.StatusOK)
	down, _ := newStatusServer(t, http.StatusServiceUnavailable)
	conn := newConn(t, []string{up1, up2, down}, withHealthPath("/health"))

	err := dependency{conn: conn}.CheckHealth(t.Context())
	require.Error(t, err)

	var es *errorstack.Error
	require.ErrorAs(t, err, &es)
	require.Len(t, es.Entries, 1)
	assert.Equal(t, []any{"endpoints", 2}, es.Entries[0].Path)
}

func TestCheckHealth_MultipleAllDownInOrder(t *testing.T) {
	d0, _ := newStatusServer(t, http.StatusInternalServerError)
	d1, _ := newStatusServer(t, http.StatusInternalServerError)
	d2, _ := newStatusServer(t, http.StatusInternalServerError)
	conn := newConn(t, []string{d0, d1, d2}, withHealthPath("/health"))

	// Probes finish in any order; the entries do not.
	for range 5 {
		err := dependency{conn: conn}.CheckHealth(t.Context())
		require.Error(t, err)

		var es *errorstack.Error
		require.ErrorAs(t, err, &es)

		var paths [][]any
		for _, entry := range es.Entries {
			paths = append(paths, entry.Path)
		}
		assert.Equal(t, [][]any{{"endpoints", 0}, {"endpoints", 1}, {"endpoints", 2}}, paths)
	}
}

func TestCheckHealth_ProbesEveryEndpointOnce(t *testing.T) {
	u0, h0 := newStatusServer(t, http.StatusOK)
	u1, h1 := newStatusServer(t, http.StatusOK)
	u2, h2 := newStatusServer(t, http.StatusOK)
	conn := newConn(t, []string{u0, u1, u2}, withHealthPath("/health"))

	err := dependency{conn: conn}.CheckHealth(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(1), h0.Load())
	assert.Equal(t, int64(1), h1.Load())
	assert.Equal(t, int64(1), h2.Load())
}

func TestCheckHealth_UsesHealthPathAndHeaders(t *testing.T) {
	// The wrong path or a missing key returns 5xx, so a healthy result proves
	// both were sent.
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/v1/ready" && req.Header.Get("X-Api-Key") == "key" {
			rw.WriteHeader(http.StatusOK)
			return
		}
		rw.WriteHeader(http.StatusInternalServerError)
	})
	conn := newConn(t, []string{url + "/v1"}, withHealthPath("/ready"), func(cfg *Config) {
		cfg.Headers = map[string]string{"X-Api-Key": "key"}
	})

	assert.NoError(t, dependency{conn: conn}.CheckHealth(t.Context()))
}

func TestCheckHealth_NoHealthEndpointIsHealthy(t *testing.T) {
	// An API with no health endpoint returns 404 for the probe; it is reachable
	// and not erroring, so it is healthy.
	url, _ := newStatusServer(t, http.StatusNotFound)
	conn := newConn(t, []string{url}, withHealthPath("/health"))

	assert.NoError(t, dependency{conn: conn}.CheckHealth(t.Context()))
}

func TestCheckHealth_CustomCallbackOverridesDefault(t *testing.T) {
	url, hits := newStatusServer(t, http.StatusInternalServerError)
	var called atomic.Bool
	conn := newConn(t, []string{url}, withHealthPath("/health"), func(cfg *Config) {
		cfg.HealthCheck = func(context.Context, HTTPClient) error {
			called.Store(true)
			return nil
		}
	})

	assert.NoError(t, dependency{conn: conn}.CheckHealth(t.Context()))
	assert.True(t, called.Load())
	assert.Equal(t, int64(0), hits.Load()) // default probe skipped
}

func TestCheckHealth_CustomCallbackUsesClient(t *testing.T) {
	url, _ := newServer(t, func(rw http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/custom" {
			rw.WriteHeader(http.StatusOK)
			return
		}
		rw.WriteHeader(http.StatusInternalServerError)
	})
	conn := newConn(t, []string{url}, func(cfg *Config) {
		cfg.HealthCheck = func(ctx context.Context, client HTTPClient) error {
			resp, err := client.Get(ctx, "/custom")
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			return statusError(resp.StatusCode)
		}
	})

	assert.NoError(t, dependency{conn: conn}.CheckHealth(t.Context()))
}

// TestCheckHealth_CustomCallbackErrorIsUnhealthy pins that a custom check's
// error is reported the way the default one's is: it used to be returned as is,
// and came out of the readiness probe as an INTERNAL_ERROR.
func TestCheckHealth_CustomCallbackErrorIsUnhealthy(t *testing.T) {
	conn := newConn(t, []string{"https://api.tld"}, func(cfg *Config) {
		cfg.HealthCheck = func(context.Context, HTTPClient) error {
			return statusError(http.StatusServiceUnavailable)
		}
	})

	err := dependency{conn: conn}.CheckHealth(t.Context())
	require.Error(t, err)

	var es *errorstack.Error
	require.ErrorAs(t, err, &es)
	assert.Equal(t, "Integration is not in a healthy state", es.Entries[0].Message)
	assert.Equal(t, errorstack.CodeServiceUnavailable, es.Entries[0].Extensions["code"])
	assert.Equal(t, "Status 503 Service Unavailable", es.Entries[0].Extensions["reason"])
	assert.ErrorIs(t, err, statusError(http.StatusServiceUnavailable))
}

func TestCheckHealth_ProbesConcurrently(t *testing.T) {
	newSlow := func() string {
		url, _ := newServer(t, func(rw http.ResponseWriter, _ *http.Request) {
			time.Sleep(80 * time.Millisecond)
			rw.WriteHeader(http.StatusOK)
		})
		return url
	}
	conn := newConn(t, []string{newSlow(), newSlow(), newSlow()}, withHealthPath("/health"))

	start := time.Now()
	err := dependency{conn: conn}.CheckHealth(t.Context())
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 200*time.Millisecond) // concurrent (~80ms), not sequential (~240ms)
}

// TestConnection_Close pins that Close reaches the transport's pool: the
// instrumented transport used to hide CloseIdleConnections, so closing the
// client released nothing.
func TestConnection_Close(t *testing.T) {
	closed := make(chan struct{}, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusOK)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	conn := newConn(t, []string{srv.URL})

	// A request whose body is read and closed leaves its connection idle in the
	// pool, kept alive.
	resp, err := conn.Get(t.Context(), "/")
	require.NoError(t, err)
	drainClose(resp)

	select {
	case <-closed:
		t.Fatal("the connection was closed before Close was called")
	case <-time.After(50 * time.Millisecond):
	}

	require.NoError(t, dependency{conn: conn}.Close(t.Context()))

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close(ctx) left the idle connection open")
	}
}

// newService builds a Service for one test, with the OpenTelemetry SDK disabled
// so that it creates no exporter, and stops it when the test ends.
func newService(t *testing.T) *service.Service {
	t.Helper()

	t.Setenv("OTEL_SDK_DISABLED", "true")

	svc, err := service.New(t.Context(), service.Config{SkipGlobals: true})
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, svc.Stop(context.Background()))
	})

	return svc
}

func TestConnect_HealthyClient(t *testing.T) {
	url, _ := newStatusServer(t, http.StatusOK)
	svc := newService(t)

	client, err := Connect(t.Context(), svc, Config{Name: "connect-healthy", Endpoints: []string{url}, HealthPath: "/health"})
	require.NoError(t, err)
	require.NotNil(t, client)

	// Health is reported through the Service the connection registered with,
	// not through the handle the caller holds.
	assert.NoError(t, svc.CheckHealth(t.Context()))
}

// TestConnect_ReturnsNoLifecycleHandle pins the invariant: what Connect hands
// back can be used but not closed. The Service that owns the connection is the
// only thing able to end it.
func TestConnect_ReturnsNoLifecycleHandle(t *testing.T) {
	url, _ := newStatusServer(t, http.StatusOK)

	client, err := Connect(t.Context(), newService(t), Config{Name: "standalone", Endpoints: []string{url}})
	require.NoError(t, err)
	require.NotNil(t, client)

	// HTTPClient exposes Do, Get and DoRequest only. If this ever compiles, the
	// invariant has been broken:
	//
	//   client.Close(t.Context())
	var _ interface {
		Do(context.Context, string, string, []byte, ...RequestOption) (*http.Response, error)
		Get(context.Context, string, ...RequestOption) (*http.Response, error)
		DoRequest(*http.Request) (*http.Response, error)
	} = client
}

func TestConnect_InvalidConfigReturnsError(t *testing.T) {
	client, err := Connect(t.Context(), newService(t), Config{Name: "no-endpoints"})

	assert.Error(t, err)
	assert.Nil(t, client)
}

func TestConnect_InvalidTLSReturnsError(t *testing.T) {
	client, err := Connect(t.Context(), newService(t), Config{
		Name:      "bad-tls",
		Endpoints: []string{"https://api.tld"},
		TLS:       integration.ConfigTLS{Enabled: true, CertPEM: []byte("cert"), KeyPEM: []byte("key")},
	})

	assert.Error(t, err)
	assert.Nil(t, client)
}

// TestConnect_ParticipatesInServiceHealth covers registration: Connect hands the
// connection to the Service itself, under the name the Config carries, so it
// shows up in the Service's health without the caller wiring anything.
func TestConnect_ParticipatesInServiceHealth(t *testing.T) {
	svc := newService(t)
	down, _ := newStatusServer(t, http.StatusServiceUnavailable)

	_, err := Connect(t.Context(), svc, Config{Name: "downstream", Endpoints: []string{down}, HealthPath: "/health"})
	require.NoError(t, err)

	err = svc.CheckHealth(t.Context())
	require.Error(t, err)

	// The Service's own entry first, then the failing endpoint, named by where it
	// sits in the Service and in the client.
	var es *errorstack.Error
	require.ErrorAs(t, err, &es)
	require.Len(t, es.Entries, 2)
	assert.Equal(t, []any{"dependency", "downstream", "endpoints", 0}, es.Entries[1].Path)
}
