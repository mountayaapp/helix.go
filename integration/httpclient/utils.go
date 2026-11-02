package httpclient

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	"go.opentelemetry.io/otel/attribute"
)

// Pre-computed attribute keys to avoid allocations on every call.
var (
	attrKeyEndpoint = integration.Key(identifier, "endpoint")
	attrKeyStatus   = integration.Key(identifier, "status_code")
	attrKeyAttempts = integration.Key(identifier, "attempts")
)

// newTunedTransport returns an *http.Transport with connection-pooling defaults
// tuned for low-latency reuse across repeated requests. ForceAttemptHTTP2 is set
// because providing a custom DialContext otherwise makes net/http conservatively
// disable HTTP/2, which would drop multiplexing against modern HTTPS APIs.
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

// setRequestAttributes sets the outcome of a logical request on its Operation.
//
// The Operation describes the logical request, which may have touched several
// endpoints; the per-attempt spans come from the instrumented transport
// underneath, and already carry the method, the path and the sizes. What they
// cannot carry is the outcome of the whole: how many endpoints were tried —
// failover is this integration's behaviour rather than the transport's — and
// which one, with which status, the caller ended up with. e is nil when no
// endpoint was tried, and status is 0 when none answered.
func setRequestAttributes(op *integration.Operation, e *endpoint, status, attempts int) {
	if !op.IsRecording() {
		return
	}

	attrs := make([]attribute.KeyValue, 0, 3)
	attrs = append(attrs, attrKeyAttempts.Int(attempts))
	if e != nil {
		attrs = append(attrs, attrKeyEndpoint.String(e.origin))
	}

	if status > 0 {
		attrs = append(attrs, attrKeyStatus.Int(status))
	}

	op.SetAttributes(attrs...)
}

// idempotent reports whether req can be sent twice with the effect of once: its
// method is idempotent per RFC 9110, or it carries an idempotency key — in its
// own headers or in the client's defaults — which is the convention net/http
// follows when it replays a request itself.
func idempotent(req *http.Request, defaults http.Header) bool {
	switch req.Method {
	case "", http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace, http.MethodPut, http.MethodDelete:
		return true
	}

	return hasIdempotencyKey(req.Header) || hasIdempotencyKey(defaults)
}

// hasIdempotencyKey reports whether h carries an idempotency key, under either of
// the names net/http recognises.
func hasIdempotencyKey(h http.Header) bool {
	_, ok := h["Idempotency-Key"]
	_, xok := h["X-Idempotency-Key"]

	return ok || xok
}

// unsent reports whether err proves its request never reached the endpoint: the
// connection to it, or to the proxy in front of it, could not be established, so
// not a byte of the request was written.
//
// Anything else — a timeout, a connection reset — may have happened after the
// endpoint read the request, and possibly acted on it.
func unsent(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) && (opErr.Op == "dial" || opErr.Op == "proxyconnect") {
		return true
	}

	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr)
}

// redact strips the query string and the fragment from the URL a *url.Error
// names, and the password from its credentials, so that an API key passed as a
// query parameter does not reach the span or the log the error ends up in. The
// error is modified in place: it is the one net/http just returned, and nothing
// else holds it.
//
// A URL that cannot be parsed back is dropped rather than kept verbatim, since
// what makes it unparsable could be anywhere in it.
func redact(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}

	u, parseErr := url.Parse(urlErr.URL)
	if parseErr != nil {
		urlErr.URL = ""
		return err
	}

	u.RawQuery, u.ForceQuery = "", false
	u.Fragment, u.RawFragment = "", ""
	urlErr.URL = u.Redacted()

	return err
}

// errInvalidPath returns the error a request is refused with when its URL is not
// relative to the endpoints. It is built on each call: an *errorstack.Error can
// be appended to, so a shared one would be shared mutable state.
//
// The path is deliberately left out of the message, since it may carry a query
// string.
func errInvalidPath() error {
	return errorstack.New(`Path must be empty or start with "/" or "?"`)
}

// statusError is the error a 5xx response is recorded as on the Operation of a
// request that ends with one, and the reason an endpoint answering a health probe
// with one is unhealthy.
type statusError int

// Error renders the status the way net/http names it, falling back to the number
// alone for a status net/http has no name for.
func (e statusError) Error() string {
	text := "status " + strconv.Itoa(int(e))
	if name := http.StatusText(int(e)); name != "" {
		text += " " + name
	}

	return text
}

// drainClose drains a bounded amount of the response body and closes it so the
// underlying connection can be reused. Safe to call with a nil response.
func drainClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
}
