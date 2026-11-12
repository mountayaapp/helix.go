package httpclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// trackingBody is an io.ReadCloser that records how much was read and whether it
// was closed, so drainClose can be observed.
type trackingBody struct {
	reader *bytes.Reader
	read   int
	closed bool
}

func newTrackingBody(payload []byte) *trackingBody {
	return &trackingBody{reader: bytes.NewReader(payload)}
}

func (b *trackingBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}

func (b *trackingBody) Close() error {
	b.closed = true
	return nil
}

func TestPreComputedAttributeKeys(t *testing.T) {
	assert.Equal(t, attribute.Key("httpclient.endpoint"), attrKeyEndpoint)
	assert.Equal(t, attribute.Key("httpclient.status_code"), attrKeyStatus)
	assert.Equal(t, attribute.Key("httpclient.attempts"), attrKeyAttempts)
}

func TestSetRequestAttributes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	inst := newTestInstrument(t, sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	e := newEndpoints([]*url.URL{{Scheme: "https", Host: "api.tld", Path: "/v1"}})[0]

	testcases := []struct {
		name     string
		endpoint *endpoint
		status   int
		attempts int
		want     []attribute.KeyValue
	}{
		{
			name:     "answered",
			endpoint: e,
			status:   http.StatusOK,
			attempts: 1,
			want: []attribute.KeyValue{
				attrKeyAttempts.Int(1),
				attrKeyEndpoint.String("https://api.tld"),
				attrKeyStatus.Int(http.StatusOK),
			},
		},
		{
			name:     "unanswered",
			endpoint: e,
			attempts: 3,
			want: []attribute.KeyValue{
				attrKeyAttempts.Int(3),
				attrKeyEndpoint.String("https://api.tld"),
			},
		},
		{
			name: "never sent",
			want: []attribute.KeyValue{
				attrKeyAttempts.Int(0),
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, op := inst.StartOperation(t.Context(), trace.SpanKindInternal, http.MethodGet)
			setRequestAttributes(op, tc.endpoint, tc.status, tc.attempts)
			op.End(ctx)

			spans := recorder.Ended()
			got := spans[len(spans)-1].Attributes()

			// The first attribute is the name the Instrument stamps on every span.
			assert.Equal(t, tc.want, got[1:])
		})
	}
}

// newTestInstrument builds an Instrument the way Connect does, minus the Service.
func newTestInstrument(tb testing.TB, provider *sdktrace.TracerProvider) *integration.Instrument {
	tb.Helper()

	inst, err := integration.NewInstrument(integration.InstrumentConfig{
		Identifier:     identifier,
		Humanized:      humanized,
		TracerProvider: provider,
	})
	require.NoError(tb, err)

	return inst
}

func TestIdempotent(t *testing.T) {
	testcases := []struct {
		method   string
		header   http.Header
		defaults http.Header
		expected bool
	}{
		{method: http.MethodGet, expected: true},
		{method: http.MethodHead, expected: true},
		{method: http.MethodOptions, expected: true},
		{method: http.MethodTrace, expected: true},
		{method: http.MethodPut, expected: true},
		{method: http.MethodDelete, expected: true},
		{method: http.MethodPost, expected: false},
		{method: http.MethodPatch, expected: false},
		{method: http.MethodPost, header: http.Header{"Idempotency-Key": {"k"}}, expected: true},
		{method: http.MethodPatch, header: http.Header{"X-Idempotency-Key": {"k"}}, expected: true},
		{method: http.MethodPost, defaults: http.Header{"Idempotency-Key": {"k"}}, expected: true},
	}

	for _, tc := range testcases {
		t.Run(fmt.Sprintf("%s %v %v", tc.method, tc.header, tc.defaults), func(t *testing.T) {
			req := &http.Request{Method: tc.method, Header: tc.header}
			assert.Equal(t, tc.expected, idempotent(req, tc.defaults))
		})
	}
}

func TestUnsent(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}

	assert.True(t, unsent(&url.Error{Op: "Get", URL: "http://api.tld", Err: refused}))
	assert.True(t, unsent(&net.OpError{Op: "proxyconnect", Net: "tcp", Err: refused}))
	assert.True(t, unsent(&net.DNSError{Err: "no such host", Name: "api.tld"}))
	assert.False(t, unsent(&net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}))
	assert.False(t, unsent(context.DeadlineExceeded))
	assert.False(t, unsent(errors.New("EOF")))
}

func TestRedact(t *testing.T) {
	testcases := []struct {
		name     string
		url      string
		expected string
	}{
		{name: "query", url: "https://api.tld/things?api_key=secret", expected: "https://api.tld/things"},
		{name: "fragment", url: "https://api.tld/things#secret", expected: "https://api.tld/things"},
		{name: "password", url: "https://user:secret@api.tld/things", expected: "https://user:xxxxx@api.tld/things"},
		{name: "unparsable", url: "https://api.tld/%zz?api_key=secret", expected: ""},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			err := redact(&url.Error{Op: "Get", URL: tc.url, Err: errors.New("boom")})

			var urlErr *url.Error
			require.ErrorAs(t, err, &urlErr)
			assert.Equal(t, tc.expected, urlErr.URL)
			assert.NotContains(t, err.Error(), "secret")
		})
	}

	plain := errors.New("plain")
	assert.Equal(t, plain, redact(plain))
	assert.NoError(t, redact(nil))
}

// apiToken is the credential the requests below carry.
const apiToken = "SECRETVALUE0123456789"

// TestRedact_MalformedLocation pins the one text of net/http that quotes what an
// endpoint answered: a redirect whose Location header does not parse is reported
// with the header as the endpoint wrote it. The header is left out whole, however
// the endpoint repeated the request in it, and the error still names the URL
// asked, without its query, and the parser's reason. Each case is a real redirect
// net/http followed.
func TestRedact_MalformedLocation(t *testing.T) {
	mux := http.NewServeMux()

	// Each handler answers a redirect whose Location does not parse and repeats
	// what the request held as its case says.
	redirect := func(path string, location func(req *http.Request) string) {
		mux.HandleFunc(path, func(rw http.ResponseWriter, req *http.Request) {
			rw.Header()["Location"] = []string{location(req)}
			rw.WriteHeader(http.StatusFound)
		})
	}

	redirect("/echo", func(req *http.Request) string { return "http://bad host/%zz?" + req.URL.RawQuery })
	redirect("/lower", func(req *http.Request) string { return "http://bad host/%zz?" + strings.ToLower(req.URL.RawQuery) })
	redirect("/plus", func(req *http.Request) string {
		return "http://bad host/%zz?" + strings.ReplaceAll(req.URL.RawQuery, "%20", "+")
	})
	redirect("/reencoded", func(req *http.Request) string { return "http://bad host/%zz?" + req.URL.Query().Encode() })
	redirect("/hop1", func(*http.Request) string { return "/hop2?X-Signature=SIGNEDSECRET0123456789abcdef" })
	redirect("/hop2", func(req *http.Request) string { return "http://bad host/%zz?" + req.URL.RawQuery })
	redirect("/bot/", func(req *http.Request) string { return "http://bad host/%zz" + req.URL.Path })
	redirect("/port", func(req *http.Request) string { return "http://api.tld:" + req.URL.Query().Get("api_key") + "/next" })

	server := httptest.NewServer(mux)
	defer server.Close()

	testcases := []struct {
		name   string
		target string
		secret string
	}{
		{name: "a key of the query, repeated as written", target: "/echo?api_key=" + apiToken, secret: apiToken},
		{name: "a key shorter than eight bytes", target: "/echo?key=s3cr3t7", secret: "s3cr3t7"},
		{name: "an encoded key, repeated in lower-case escapes", target: "/lower?api_key=secret%2BTOKEN%2F0123", secret: "secret%2btoken%2f0123"},
		{name: "a key with spaces, repeated with plus signs", target: "/plus?api_key=secret%20token%20value", secret: "secret+token+value"},
		{name: "a key the endpoint re-encodes", target: "/reencoded?api_key=secret/token+0123~x", secret: "secret"},
		{name: "a key in a path segment", target: "/bot/" + apiToken + "/send", secret: apiToken},
		{name: "a signature an earlier redirect added, repeated by the next", target: "/hop1?api_key=" + apiToken, secret: "SIGNEDSECRET0123456789abcdef"},
		{name: "a key the parser quotes in its reason", target: "/port?api_key=" + apiToken, secret: apiToken},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, server.URL+tc.target, nil)
			require.NoError(t, err)

			resp, err := (&http.Client{}).Do(req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.secret, "the endpoint did not repeat the secret: the case proves nothing")

			redacted := redact(err).Error()

			// The request's own path, the caller's, is kept: what follows it is the
			// endpoint's answer, of which nothing is.
			_, answered, found := strings.Cut(redacted, `": `)
			require.True(t, found, redacted)

			assert.NotContains(t, answered, tc.secret)
			assert.NotContains(t, redacted, "?", "a query is left in %s", redacted)
			assert.True(t, strings.HasPrefix(answered, "failed to parse Location header: "), redacted)
		})
	}

	t.Run("a value that is a word of the text leaves the text as it was", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, server.URL+"/echo?q=Location&why=unexpected", nil)
		require.NoError(t, err)

		resp, err := (&http.Client{}).Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		require.Error(t, err)

		assert.Equal(t, `Get "`+server.URL+`/echo": failed to parse Location header: invalid character " " in host name`, redact(err).Error())
	})
}

// TestWithoutLocation pins how the text net/http gives a malformed Location is
// read: the header left out, the reason kept with what it quotes left out, and a
// text that does not read as expected reduced to the sentence alone.
func TestWithoutLocation(t *testing.T) {
	testcases := []struct {
		name   string
		text   string
		want   string
		isText bool
	}{
		{
			name:   "as net/http writes it",
			text:   `failed to parse Location header "/moved/%zz?api_key=x": parse "/moved/%zz?api_key=x": invalid URL escape "%zz"`,
			want:   `failed to parse Location header: invalid URL escape "%zz"`,
			isText: true,
		},
		{
			name:   "a reason quoting nothing",
			text:   `failed to parse Location header "http://a b/?k=v": parse "http://a b/?k=v": net/url: invalid control character in URL`,
			want:   "failed to parse Location header: net/url: invalid control character in URL",
			isText: true,
		},
		{
			name:   "a reason quoting a part of the header",
			text:   `failed to parse Location header "http://api.tld:SECRETVALUE/x": parse "http://api.tld:SECRETVALUE/x": invalid port ":SECRETVALUE" after host`,
			want:   `failed to parse Location header: invalid port "…" after host`,
			isText: true,
		},
		{
			name:   "a text that does not read as expected",
			text:   `failed to parse Location header "unterminated`,
			want:   "failed to parse Location header",
			isText: true,
		},
		{name: "another text", text: "stopped after 10 redirects"},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := withoutLocation(tc.text)

			assert.Equal(t, tc.isText, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// Any other failure is left as net/http told it, and the failure a redacted one
// reports is still told the way it was: whether the request was sent, whether it
// timed out.
func TestRedact_KeepsWhatTheFailureWas(t *testing.T) {
	address := "https://api.tld/search?q=connection+refused"

	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}
	err := redact(&url.Error{Op: "Get", URL: address, Err: refused})

	assert.Equal(t, `Get "https://api.tld/search": dial tcp: connect: connection refused`, err.Error())
	assert.True(t, unsent(err))

	var opErr *net.OpError
	require.ErrorAs(t, err, &opErr)
	assert.Same(t, refused, opErr)

	malformed := redact(&url.Error{Op: "Get", URL: address, Err: fmt.Errorf("failed to parse Location header %q: %w", "/x?k=v", context.DeadlineExceeded)})
	assert.Equal(t, `Get "https://api.tld/search": failed to parse Location header`, malformed.Error())
	assert.ErrorIs(t, malformed, context.DeadlineExceeded)

	var urlErr *url.Error
	require.ErrorAs(t, malformed, &urlErr)
	assert.NoError(t, errors.Unwrap(urlErr.Err), "the text left out is reachable through the chain")
	assert.False(t, urlErr.Timeout(), "a wrapped deadline was never a timeout to *url.Error")
}

func TestStatusError(t *testing.T) {
	assert.Equal(t, "status 503 Service Unavailable", statusError(http.StatusServiceUnavailable).Error())
	assert.Equal(t, "status 599", statusError(599).Error())
}

func TestDrainClose(t *testing.T) {
	// Safe with a nil response.
	drainClose(nil)

	// Safe with a nil body.
	drainClose(&http.Response{})

	// Drains and closes a small body.
	small := newTrackingBody([]byte("hello"))
	drainClose(&http.Response{Body: small})
	assert.True(t, small.closed)
	assert.Equal(t, 5, small.read)

	// Drains at most 4KiB of a large body, then closes it.
	large := newTrackingBody(bytes.Repeat([]byte("x"), 16<<10))
	drainClose(&http.Response{Body: large})
	assert.True(t, large.closed)
	assert.LessOrEqual(t, large.read, 4<<10)
}

func TestNewTunedTransport(t *testing.T) {
	transport := newTunedTransport()

	assert.NotNil(t, transport.Proxy)
	assert.NotNil(t, transport.DialContext)
	assert.Equal(t, 100, transport.MaxIdleConns)
	assert.Equal(t, 32, transport.MaxIdleConnsPerHost)
	assert.Equal(t, 90*time.Second, transport.IdleConnTimeout)
	assert.Equal(t, 5*time.Second, transport.TLSHandshakeTimeout)
	assert.Equal(t, 1*time.Second, transport.ExpectContinueTimeout)
}

func BenchmarkSetRequestAttributes(b *testing.B) {
	inst := newTestInstrument(b, sdktrace.NewTracerProvider())
	_, op := inst.StartOperation(b.Context(), trace.SpanKindInternal, http.MethodGet)
	e := newEndpoints([]*url.URL{{Scheme: "https", Host: "api.tld"}})[0]

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		setRequestAttributes(op, e, http.StatusOK, 1)
	}
}
