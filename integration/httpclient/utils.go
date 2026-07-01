package httpclient

import (
	"io"
	"net"
	"net/http"
	"time"

	"github.com/mountayaapp/helix.go/telemetry/trace"

	"go.opentelemetry.io/otel/attribute"
)

/*
Pre-computed attribute keys to avoid allocations on every call.
*/
var (
	attrKeyEndpoint = attribute.Key(identifier + ".endpoint")
	attrKeyMethod   = attribute.Key(identifier + ".method")
	attrKeyStatus   = attribute.Key(identifier + ".status_code")
)

/*
newTunedTransport returns an *http.Transport with connection-pooling defaults
tuned for low-latency reuse across repeated requests. ForceAttemptHTTP2 is set
because providing a custom DialContext otherwise makes net/http conservatively
disable HTTP/2, which would drop multiplexing against modern HTTPS APIs.
*/
func newTunedTransport() *http.Transport {
	return &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

/*
setRequestAttributes sets request attributes on a trace span.
*/
func setRequestAttributes(span *trace.Span, endpoint, method string, status int) {
	span.SetAttributes(
		attrKeyEndpoint.String(endpoint),
		attrKeyMethod.String(method),
	)

	if status > 0 {
		span.SetAttributes(attrKeyStatus.Int(status))
	}
}

/*
statusOf returns the HTTP status code of resp, or 0 when resp is nil.
*/
func statusOf(resp *http.Response) int {
	if resp == nil {
		return 0
	}

	return resp.StatusCode
}

/*
drainClose drains a bounded amount of the response body and closes it so the
underlying connection can be reused. Safe to call with a nil response.
*/
func drainClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
}
