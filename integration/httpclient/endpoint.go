package httpclient

import (
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"
)

// maxPause caps how long an endpoint is skipped for on the word of its
// Retry-After header, so that a misconfigured or hostile value cannot take an
// endpoint out of the rotation for good.
const maxPause = 5 * time.Minute

// endpoint is one of the interchangeable base URLs a connection round-robins
// across.
type endpoint struct {

	// url is the endpoint as parsed by Config.sanitize. It is never modified: a
	// request URL is a copy of it.
	url *url.URL

	// origin is "<scheme>://<host>", which is what the httpclient.endpoint
	// attribute holds on every span: the transport underneath knows an attempt by
	// its URL, not by the endpoint it came from, and the two spans of one request
	// should not disagree about where it went.
	origin string

	// pausedUntil is when the endpoint is back in the rotation after it asked to be
	// left alone, in Unix nanoseconds. Zero, or any time in the past, means it is
	// in the rotation.
	pausedUntil atomic.Int64
}

// newEndpoints returns the endpoints of the parsed URLs, in the same order.
func newEndpoints(urls []*url.URL) []*endpoint {
	endpoints := make([]*endpoint, len(urls))
	for i, u := range urls {
		endpoints[i] = &endpoint{
			url:    u,
			origin: u.Scheme + "://" + u.Host,
		}
	}

	return endpoints
}

// resolve returns the URL ref names below the endpoint: the endpoint's path
// followed by ref's, and ref's query.
//
// The scheme and the host are the endpoint's, copied and never derived from ref:
// relative has already refused a ref that carries either, and ref is only ever
// appended to a path. No path a caller passes can reach another host.
func (e *endpoint) resolve(ref *url.URL) *url.URL {
	u := e.url.Clone()
	u.Path = e.url.Path + ref.Path
	u.RawPath = ""
	if e.url.RawPath != "" || ref.RawPath != "" {
		u.RawPath = e.url.EscapedPath() + ref.EscapedPath()
	}

	u.RawQuery = ref.RawQuery
	u.ForceQuery = ref.ForceQuery

	return u
}

// paused reports whether the endpoint asked, at now, to be left alone.
func (e *endpoint) paused(now time.Time) bool {
	return e.pausedUntil.Load() > now.UnixNano()
}

// pause takes the endpoint out of the rotation for d, plus up to a tenth of it
// at random: clients that were all told the same Retry-After should not all come
// back at the same instant.
func (e *endpoint) pause(now time.Time, d time.Duration) {
	d += rand.N(d/10 + 1)
	e.pausedUntil.Store(now.Add(d).UnixNano())
}

// honourRetryAfter takes the endpoint out of the rotation for as long as resp
// asks: the Retry-After of a 429 or a 503.
func (e *endpoint) honourRetryAfter(resp *http.Response) {
	if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusServiceUnavailable {
		return
	}

	now := time.Now()
	if d := retryAfter(resp.Header.Get("Retry-After"), now); d > 0 {
		e.pause(now, d)
	}
}

// retryAfter returns how long a Retry-After value received at now asks for, in
// seconds or as an HTTP date, capped at maxPause. It returns zero when there is
// nothing worth honouring.
func retryAfter(value string, now time.Time) time.Duration {
	if value == "" {
		return 0
	}

	var d time.Duration
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		d = maxPause
		if seconds < int64(maxPause/time.Second) {
			d = time.Duration(seconds) * time.Second
		}
	} else if at, err := http.ParseTime(value); err == nil {
		d = at.Sub(now)
	}

	return min(max(d, 0), maxPause)
}
