package httpclient

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelative(t *testing.T) {
	testcases := []struct {
		path     string
		expected bool
	}{
		{path: "", expected: true},
		{path: "/", expected: true},
		{path: "/things?limit=10", expected: true},
		{path: "?limit=10", expected: true},
		{path: "/files/a%2Fb", expected: true},
		{path: "///things", expected: true},
		{path: "things", expected: false},
		{path: "@evil.tld/x", expected: false},
		{path: ".evil.tld", expected: false},
		{path: "//evil.tld/x", expected: false},
		{path: "https://evil.tld/x", expected: false},
		{path: "mailto:someone@evil.tld", expected: false},
	}

	for _, tc := range testcases {
		t.Run(tc.path, func(t *testing.T) {
			ref, err := url.Parse(tc.path)
			require.NoError(t, err)

			assert.Equal(t, tc.expected, relative(ref))
		})
	}
}

func TestEndpoint_Resolve(t *testing.T) {
	testcases := []struct {
		name     string
		endpoint string
		path     string
		expected string
	}{
		{name: "no base path", endpoint: "https://api.tld", path: "/things", expected: "https://api.tld/things"},
		{name: "base path", endpoint: "https://api.tld/v1", path: "/things?limit=10", expected: "https://api.tld/v1/things?limit=10"},
		{name: "query only", endpoint: "https://api.tld/v1", path: "?limit=10", expected: "https://api.tld/v1?limit=10"},
		{name: "empty", endpoint: "https://api.tld:8443/v1", path: "", expected: "https://api.tld:8443/v1"},
		{name: "escaped base path", endpoint: "https://api.tld/a%2Fb", path: "/c", expected: "https://api.tld/a%2Fb/c"},
		{name: "escaped path", endpoint: "https://api.tld/v1", path: "/a%2Fb", expected: "https://api.tld/v1/a%2Fb"},
		{name: "host-like path", endpoint: "https://api.tld", path: "///evil.tld/x", expected: "https://api.tld///evil.tld/x"},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			base, msg := parseEndpoint(tc.endpoint)
			require.Empty(t, msg)

			ref, err := url.Parse(tc.path)
			require.NoError(t, err)

			e := newEndpoints([]*url.URL{base})[0]
			got := e.resolve(ref)

			assert.Equal(t, tc.expected, got.String())
			assert.Equal(t, base.Host, got.Host)

			// The endpoint's own URL is left alone for the next request.
			assert.Equal(t, tc.endpoint, e.url.String())
		})
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)

	testcases := []struct {
		name     string
		value    string
		expected time.Duration
	}{
		{name: "missing", value: "", expected: 0},
		{name: "seconds", value: "30", expected: 30 * time.Second},
		{name: "zero", value: "0", expected: 0},
		{name: "negative", value: "-5", expected: 0},
		{name: "capped", value: "86400", expected: maxPause},
		{name: "overflowing", value: "99999999999999999", expected: maxPause},
		{name: "date", value: now.Add(time.Minute).Format(http.TimeFormat), expected: time.Minute},
		{name: "past date", value: now.Add(-time.Minute).Format(http.TimeFormat), expected: 0},
		{name: "garbage", value: "soon", expected: 0},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, retryAfter(tc.value, now))
		})
	}
}

func TestEndpoint_HonourRetryAfter(t *testing.T) {
	testcases := []struct {
		name   string
		status int
		paused bool
	}{
		{name: "too many requests", status: http.StatusTooManyRequests, paused: true},
		{name: "service unavailable", status: http.StatusServiceUnavailable, paused: true},
		{name: "internal server error", status: http.StatusInternalServerError, paused: false},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			e := &endpoint{}
			e.honourRetryAfter(&http.Response{
				StatusCode: tc.status,
				Header:     http.Header{"Retry-After": {"10"}},
			})

			now := time.Now()
			assert.Equal(t, tc.paused, e.paused(now))
			if tc.paused {
				// Ten seconds, and up to a tenth of it again as jitter.
				until := time.Unix(0, e.pausedUntil.Load())
				assert.WithinRange(t, until, now.Add(9*time.Second), now.Add(12*time.Second))
			}
		})
	}
}
