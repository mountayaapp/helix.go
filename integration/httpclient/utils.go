package httpclient

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"

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
	if opErr, ok := errors.AsType[*net.OpError](err); ok && (opErr.Op == "dial" || opErr.Op == "proxyconnect") {
		return true
	}

	_, ok := errors.AsType[*net.DNSError](err)
	return ok
}

// malformedLocation starts the text net/http gives a redirect whose Location
// header does not parse: the header quoted as the endpoint wrote it, then the
// parser's error, which quotes it again.
const malformedLocation = "failed to parse Location header "

// redact returns err, the failure of a request, with nothing of the request's
// query in its text, so that an API key passed as a query parameter does not
// reach the span or the log the error ends up in. An error that is no *url.Error
// names no URL, and is returned as it is.
//
// net/http reports a failed request as a *url.Error quoting the URL asked, the
// redirect's when one was followed. The query string and the fragment are
// stripped from it, and the password from its credentials. The error is modified
// in place: it is the one net/http just returned, and nothing else holds it. A URL
// that cannot be parsed back is dropped rather than kept verbatim, since what
// makes it unparsable could be anywhere in it.
//
// Some texts quote what the endpoint answered. A redirect whose Location header
// does not parse is reported with the header as the endpoint wrote it, which can
// repeat the request's query, re-encoded or not, a signature an earlier redirect
// added, or a key the endpoint put in a path segment, which nothing tells from a
// path: the header is left out of that text whole, and the parser's reason kept
// (withoutLocation). A response net/http refuses to read, its status line or a
// header line malformed, is reported with the line or the value it refused, which
// can hold the same: the text is cut where the quote begins, and the sentence
// naming what was refused kept (withoutAnswer).
func redact(err error) error {
	urlErr, ok := errors.AsType[*url.Error](err)
	if !ok {
		return err
	}

	urlErr.URL = withoutQuery(urlErr.URL)

	if urlErr.Err == nil {
		return err
	}

	text := urlErr.Err.Error()
	if redacted, ok := withoutLocation(text); ok {
		urlErr.Err = &redactedError{text: redacted, err: urlErr.Err}
	} else if redacted, ok := withoutAnswer(text); ok {
		urlErr.Err = &redactedError{text: redacted, err: urlErr.Err}
	}

	return err
}

// answerQuotes are the sentences after which the standard library quotes what an
// endpoint answered and it refused to read, each with what is kept of it:
// net/textproto's for a header line, net/http's for a status line and the values
// of the headers that frame a body, and the HTTP/2 transport's for a header's
// name. What follows the sentence is the endpoint's, quoted as it wrote it.
var answerQuotes = []struct {
	quotes string
	kept   string
}{
	{quotes: "malformed MIME header initial line: ", kept: "malformed MIME header initial line"},
	{quotes: "malformed MIME header line: ", kept: "malformed MIME header line"},
	{quotes: "malformed MIME header: missing colon: ", kept: "malformed MIME header: missing colon"},
	{quotes: "malformed HTTP response ", kept: "malformed HTTP response"},
	{quotes: "malformed HTTP status code ", kept: "malformed HTTP status code"},
	{quotes: "malformed HTTP version ", kept: "malformed HTTP version"},
	{quotes: "bad trailer key ", kept: "bad trailer key"},
	{quotes: "bad Content-Length ", kept: "bad Content-Length"},
	{quotes: "invalid empty Content-Length ", kept: "invalid empty Content-Length"},
	{quotes: "message cannot contain multiple Content-Length headers; got ", kept: "message cannot contain multiple Content-Length headers"},
	{quotes: "too many transfer encodings: ", kept: "too many transfer encodings"},
	{quotes: "unsupported transfer encoding: ", kept: "unsupported transfer encoding"},
	{quotes: "invalid header field name ", kept: "invalid header field name"},
	{quotes: "invalid header field value for ", kept: "invalid header field value"},
	{quotes: "invalid pseudo-header ", kept: "invalid pseudo-header"},
	{quotes: "duplicate pseudo-header ", kept: "duplicate pseudo-header"},
}

// withoutAnswer returns text cut where it first quotes what an endpoint answered
// (answerQuotes), the sentence naming what was refused kept and nothing after it,
// and false when text quotes no answer.
func withoutAnswer(text string) (string, bool) {
	cut, kept := -1, ""
	for _, answer := range answerQuotes {
		if i := strings.Index(text, answer.quotes); i >= 0 && (cut < 0 || i < cut) {
			cut, kept = i, answer.kept
		}
	}

	if cut < 0 {
		return "", false
	}

	return text[:cut] + kept, true
}

// withoutQuery returns address without its query string, its fragment and the
// password of its credentials, and the empty string when it cannot be parsed.
func withoutQuery(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		return ""
	}

	u.RawQuery, u.ForceQuery = "", false
	u.Fragment, u.RawFragment = "", ""

	return u.Redacted()
}

// withoutLocation returns the text net/http gives a malformed Location header,
// `failed to parse Location header "<header>": parse "<header>": <reason>`, as
// "failed to parse Location header: <reason>", every string the reason quotes
// left out too, and false for any other text. A text that does not read as
// expected is that sentence without a reason.
func withoutLocation(text string) (string, bool) {
	rest, found := strings.CutPrefix(text, malformedLocation)
	if !found {
		return "", false
	}

	const withoutHeader = "failed to parse Location header"

	header, err := strconv.QuotedPrefix(rest)
	if err != nil {
		return withoutHeader, true
	}

	// What follows is the parser's *url.Error: `parse "<header>": <reason>`.
	parsed, found := strings.CutPrefix(rest[len(header):], ": parse ")
	if !found {
		return withoutHeader, true
	}

	again, err := strconv.QuotedPrefix(parsed)
	if err != nil {
		return withoutHeader, true
	}

	reason, found := strings.CutPrefix(parsed[len(again):], ": ")
	if !found || reason == "" {
		return withoutHeader, true
	}

	return withoutHeader + ": " + withoutQuoted(reason), true
}

// maxQuotedReason is how long a string the parser's reason quotes is kept at
// most: the character it stopped on, or an escape (`invalid URL escape "%zz"`).
// A longer one is a part of the header (`invalid port ":8443" after host`).
const maxQuotedReason = 4

// withoutQuoted returns text with every string it quotes, `"..."` as strconv
// quotes one, written as `"…"` when it is longer than maxQuotedReason: the parser
// quotes the part of the header it stopped on.
func withoutQuoted(text string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(text, '"')
		if i < 0 {
			b.WriteString(text)
			return b.String()
		}

		b.WriteString(text[:i])

		quoted, err := strconv.QuotedPrefix(text[i:])
		if err != nil {
			b.WriteString(text[i:])
			return b.String()
		}

		if value, err := strconv.Unquote(quoted); err == nil && len(value) <= maxQuotedReason {
			b.WriteString(quoted)
		} else {
			b.WriteString(`"…"`)
		}

		text = text[i+len(quoted):]
	}
}

// redactedError stands for an error whose text quoted what the endpoint answered:
// it reads as that text with what it quoted left out.
//
// The error it stands for is kept for errors.Is and errors.As alone, so that what
// the failure was is still told the way it was (a deadline, a connection that
// could not be established, which decides whether the request fails over). It
// declares no Unwrap: the text of the error it stands for is the one thing it
// exists to keep out of a span or a log, and whatever prints a chain would reach
// it there.
type redactedError struct {
	text string
	err  error
}

// Error returns the redacted text.
func (e *redactedError) Error() string {
	return e.text
}

// Is reports whether the error this one stands for is target.
func (e *redactedError) Is(target error) bool {
	return errors.Is(e.err, target)
}

// As finds target in the chain of the error this one stands for.
func (e *redactedError) As(target any) bool {
	return errors.As(e.err, target)
}

// Timeout reports whether the error this one stands for is a timeout, which is
// how a *url.Error asks the error it carries.
func (e *redactedError) Timeout() bool {
	timeout, ok := e.err.(interface{ Timeout() bool }) //nolint:errorlint // As *url.Error asks: the error itself, not its chain.
	return ok && timeout.Timeout()
}

// Temporary reports whether the error this one stands for is temporary, which
// is how a *url.Error asks the error it carries.
func (e *redactedError) Temporary() bool {
	temporary, ok := e.err.(interface{ Temporary() bool }) //nolint:errorlint // As *url.Error asks: the error itself, not its chain.
	return ok && temporary.Temporary()
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
