package httpclient

import (
	"errors"
	"net/http"
	"strings"
)

// maxRedirects is how many redirects one attempt follows, net/http's own default.
const maxRedirects = 10

// errTooManyRedirects is what an attempt fails with past maxRedirects.
var errTooManyRedirects = errors.New("stopped after 10 redirects")

// portableHeaders are the only request headers carried by a redirect to another
// host. They negotiate content and describe the body; none of them is a
// credential.
var portableHeaders = map[string]bool{
	"Accept":           true,
	"Accept-Language":  true,
	"Content-Language": true,
	"Content-Type":     true,
	"User-Agent":       true,
}

// checkRedirect is the redirect policy of every client this integration builds.
// See Config.FollowRedirects.
//
// net/http's own policy follows any redirect anywhere, and strips only the
// headers it knows to be credentials — Authorization and Cookie among them —
// and only on the way to an unrelated domain. An X-API-Key header, or anything
// else set through Headers, would follow a redirect to any host, and an
// Authorization header would follow a downgrade from https to http on the same
// one.
//
// A redirect it refuses is answered with http.ErrUseLastResponse, so that the
// caller receives the 3xx response rather than an error. A refused redirect is a
// response like any other, and is not failed over: another endpoint would answer
// the same.
func (cfg *Config) checkRedirect(req *http.Request, via []*http.Request) error {
	crossHost := !strings.EqualFold(req.URL.Host, via[0].URL.Host)
	downgrade := via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https"

	switch {
	case downgrade, crossHost && !cfg.FollowRedirects:
		return http.ErrUseLastResponse
	case len(via) >= maxRedirects:
		return errTooManyRedirects
	}

	// The header was copied from the original request by net/http, so it holds
	// whatever the caller and Headers set: deleting from it here is what keeps
	// those off the other host.
	if crossHost {
		for key := range req.Header {
			if !portableHeaders[key] {
				delete(req.Header, key)
			}
		}
	}

	return nil
}
