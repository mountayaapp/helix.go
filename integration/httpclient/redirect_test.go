package httpclient

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_CheckRedirect(t *testing.T) {
	request := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u, Header: http.Header{
			"Authorization": {"Bearer secret"},
			"X-Api-Key":     {"secret"},
			"Content-Type":  {"application/json"},
		}}
	}

	origin := request("https://api.tld/a")

	testcases := []struct {
		name     string
		follow   bool
		next     string
		via      int
		expected error
		stripped bool
	}{
		{name: "same host", next: "https://api.tld/b", expected: nil},
		{name: "same host, other case", next: "https://API.tld/b", expected: nil},
		{name: "same host downgrade", next: "http://api.tld/b", expected: http.ErrUseLastResponse},
		{name: "same host downgrade, following", follow: true, next: "http://api.tld/b", expected: http.ErrUseLastResponse},
		{name: "other host", next: "https://other.tld/b", expected: http.ErrUseLastResponse},
		{name: "other port", next: "https://api.tld:8443/b", expected: http.ErrUseLastResponse},
		{name: "other host, following", follow: true, next: "https://other.tld/b", expected: nil, stripped: true},
		{name: "too many", next: "https://api.tld/b", via: maxRedirects, expected: errTooManyRedirects},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{FollowRedirects: tc.follow}

			via := []*http.Request{origin}
			for len(via) < tc.via {
				via = append(via, request("https://api.tld/hop"))
			}

			next := request(tc.next)
			assert.Equal(t, tc.expected, cfg.checkRedirect(next, via))

			if tc.stripped {
				assert.Equal(t, http.Header{"Content-Type": {"application/json"}}, next.Header)
			}
		})
	}
}
