package httpclient

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
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
	assert.Equal(t, attribute.Key("httpclient.method"), attrKeyMethod)
	assert.Equal(t, attribute.Key("httpclient.status_code"), attrKeyStatus)
}

func TestPreComputedSpanNames(t *testing.T) {
	assert.Equal(t, "HTTP Client: Request", spanRequest)
	assert.Equal(t, "HTTP Client: Status", spanStatus)
}

func TestSetRequestAttributes(t *testing.T) {
	span := trace.NewSpan(nil)

	// Should not panic with nil span internals, with or without a status code.
	setRequestAttributes(span, "https://api.tld", http.MethodGet, http.StatusOK)
	setRequestAttributes(span, "https://api.tld", http.MethodGet, 0)
}

func TestStatusOf(t *testing.T) {
	testcases := []struct {
		name     string
		resp     *http.Response
		expected int
	}{
		{
			name:     "nil response returns zero",
			resp:     nil,
			expected: 0,
		},
		{
			name:     "response returns its status code",
			resp:     &http.Response{StatusCode: http.StatusTeapot},
			expected: http.StatusTeapot,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, statusOf(tc.resp))
		})
	}
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
	span := trace.NewSpan(nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		setRequestAttributes(span, "https://api.tld", http.MethodGet, http.StatusOK)
	}
}
