package httpclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
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
