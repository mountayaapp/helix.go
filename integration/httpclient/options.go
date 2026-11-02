package httpclient

import (
	"net/http"
)

// RequestOption configures an outbound request built by Do or Get. It is applied
// once, before the first attempt, and every endpoint failed over to receives the
// request it produced.
type RequestOption func(req *http.Request)

// WithHeader sets a header on a single outbound request. It takes precedence over
// the client's configured default Headers for the same key.
func WithHeader(key, value string) RequestOption {
	return func(req *http.Request) {
		req.Header.Set(key, value)
	}
}
