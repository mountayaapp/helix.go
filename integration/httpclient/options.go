package httpclient

import (
	"net/http"
)

/*
RequestOption configures a single outbound request before it is sent.
*/
type RequestOption func(req *http.Request)

/*
WithHeader sets a header on a single outbound request. It takes precedence over
the client's configured default Headers for the same key.
*/
func WithHeader(key, value string) RequestOption {
	return func(req *http.Request) {
		req.Header.Set(key, value)
	}
}
