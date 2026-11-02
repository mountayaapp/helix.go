package httpclient

import (
	"context"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	"go.opentelemetry.io/otel/propagation"
)

// FailoverPolicy decides which failed requests a client sends again to its next
// endpoint.
//
// Failing a request over means sending it twice, and an endpoint that answered a
// 5xx, or dropped the connection after the request was written, may well have
// acted on it first. For a read that costs nothing; for a payment it charges
// twice.
type FailoverPolicy string

const (

	// FailoverIdempotent fails a request over after a transport error or a 5xx
	// response when sending it twice is safe: its method is idempotent (GET, HEAD,
	// OPTIONS, TRACE, PUT, DELETE), or it carries an Idempotency-Key or
	// X-Idempotency-Key header. Any other request fails over only when it never
	// reached the endpoint, as FailoverUnsent does. It is the default.
	FailoverIdempotent FailoverPolicy = "idempotent"

	// FailoverUnsent fails a request over only when it provably never reached the
	// endpoint: the connection could not be established. A 5xx response, or a
	// connection lost after the request was written, is returned as is.
	FailoverUnsent FailoverPolicy = "unsent"

	// FailoverAlways fails any request over after any transport error or 5xx
	// response, whatever its method. Use it only when every endpoint deduplicates
	// the requests it receives.
	FailoverAlways FailoverPolicy = "always"
)

// Config is used to configure the HTTP client integration.
type Config struct {

	// Name identifies this client in health output and error context. Two clients
	// of the same kind — one per upstream API — are told apart by it. Defaults to
	// the integration's identifier when left empty.
	Name string `json:"name"`

	// Endpoints is the list of interchangeable base URLs the client targets.
	// Requests are round-robined across them, and failed over according to
	// Failover.
	//
	// At least one endpoint is required. Each one must be an absolute http or https
	// URL, and may carry a base path that every request path is appended to. It
	// must not carry credentials, a query string or a fragment: credentials belong
	// in Headers, where they are not written into every URL a log or an error
	// prints. Trailing slashes and surrounding whitespace are trimmed.
	//
	// Examples:
	//
	//   []string{"https://api.tld"}
	//   []string{"https://api-1.tld/v1", "https://api-2.tld/v1"}
	Endpoints []string `json:"endpoints"`

	// Headers are set on every request issued by the client when the request does
	// not set them already. Typically used for content negotiation or
	// authentication.
	//
	// They are only ever sent to the endpoints' own hosts: see FollowRedirects. The
	// map is copied when the client is created, so changing it afterwards has no
	// effect.
	Headers map[string]string `json:"-"`

	// Timeout bounds each attempt: connecting, following any redirect, and reading
	// the response body, which keeps counting after Do returns. It applies again to
	// every endpoint failed over to, so one call can take up to len(Endpoints)
	// times Timeout. Give the context a deadline to bound a whole call, failover
	// included.
	//
	// A negative Timeout disables it, leaving the context as the only bound. That
	// is what a request streaming a large body through DoRequest needs.
	//
	// Default:
	//
	//   10 * time.Second
	Timeout time.Duration `json:"timeout"`

	// Failover decides which failed requests are sent again to the next endpoint.
	// See FailoverPolicy.
	//
	// Default:
	//
	//   FailoverIdempotent
	Failover FailoverPolicy `json:"failover"`

	// FollowRedirects lets the client follow a redirect to another host. A
	// redirect not followed is returned as is: the caller receives the 3xx
	// response, Location header included.
	//
	// By default, a redirect is followed only when it stays on the host of the
	// endpoint it came from. When FollowRedirects is set, a redirect to another
	// host is followed too, but carries none of the request's headers except
	// Accept, Accept-Language, Content-Language, Content-Type and User-Agent: the
	// credentials in Headers are meant for the endpoints, not for wherever one of
	// them points. A redirect from https to http is never followed.
	FollowRedirects bool `json:"follow_redirects"`

	// Propagator injects the trace context, and the baggage, into every outgoing
	// request. Defaults to the Service's own propagator — W3C trace context and
	// baggage unless the Service was configured otherwise.
	//
	// The baggage carries the six Event fields helix propagates (event.id,
	// event.name, event.organization_id, event.tenant_id, event.app.name and
	// event.app.version), along with any baggage an upstream service sent. For an
	// API outside your own services, set propagation.TraceContext{} to send the
	// trace context alone, or propagation.NewCompositeTextMapPropagator() to send
	// nothing.
	Propagator propagation.TextMapPropagator `json:"-"`

	// Transport is the http.RoundTripper requests are sent through. The client
	// wraps it to trace and measure every attempt. Defaults to an *http.Transport
	// tuned for connection reuse, configured with TLS.
	//
	// TLS must not be enabled together with a custom Transport: configure TLS on
	// the Transport instead.
	Transport http.RoundTripper `json:"-"`

	// HealthPath is the path probed on each endpoint by the default health check.
	// An endpoint is healthy as long as it is reachable and responds with a status
	// below 500, so an API that exposes no health endpoint (returning 404) is still
	// healthy. Ignored when HealthCheck is set.
	//
	// Probing is opt-in, because a client reports its health to the Service's
	// readiness probe: probing an API outside your own services makes every
	// replica of yours unready when that API is down, which takes your service
	// down with it. When both HealthPath and HealthCheck are empty, the client is
	// always healthy.
	//
	// Example:
	//
	//   "/health"
	HealthPath string `json:"health_path"`

	// HealthCheck overrides how the client's health is determined. It receives a
	// client so it can issue probe requests, and returns nil when healthy or an
	// error describing the failure otherwise. Use it for stricter checks — say,
	// requiring a 2xx from a real health endpoint.
	//
	// The client it receives sends to the same endpoints with the same settings,
	// but traces and measures nothing: a readiness probe polled every few seconds
	// is not traffic worth a span.
	HealthCheck func(ctx context.Context, client HTTPClient) error `json:"-"`

	// TLS configures TLS for the default transport. It is only needed for a custom
	// certificate authority or a client certificate: an https endpoint is verified
	// against the system's roots without it.
	//
	// A ServerName set here is sent to, and verified against, every endpoint. Leave
	// it empty for each endpoint to be verified against its own host.
	TLS integration.ConfigTLS `json:"tls"`

	// urls are the Endpoints, parsed once by sanitize.
	urls []*url.URL

	// headers are the Headers, canonicalized once by sanitize, so that a request
	// does not pay for it on every attempt.
	headers http.Header

	// health is the HealthPath, parsed once by sanitize. Nil when probing is off.
	health *url.URL
}

// sanitize sets default values - when applicable - and validates the configuration.
// Returns an error if configuration is not valid.
func (cfg *Config) sanitize() error {
	var entries []errorstack.Entry

	if len(cfg.Endpoints) == 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "endpoints"},
		})
	} else {

		// Normalized into a slice of this Config's own rather than in place: Config
		// is passed by value but a slice header is not a copy of its backing array,
		// so trimming in place would rewrite the caller's own slice.
		normalized := make([]string, len(cfg.Endpoints))
		urls := make([]*url.URL, 0, len(cfg.Endpoints))
		for i, endpoint := range cfg.Endpoints {
			endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
			normalized[i] = endpoint

			u, msg := parseEndpoint(endpoint)
			if msg != "" {
				entries = append(entries, errorstack.Entry{
					Message: msg,
					Path:    []any{"config", "endpoints", i},
				})

				continue
			}

			urls = append(urls, u)
		}

		cfg.Endpoints = normalized
		if len(urls) == len(normalized) {
			cfg.urls = urls
		}
	}

	// Copied for the same reason the endpoints are: the map is the caller's, who
	// may still be writing to it while requests read it.
	if len(cfg.Headers) > 0 {
		cfg.Headers = maps.Clone(cfg.Headers)
		cfg.headers = make(http.Header, len(cfg.Headers))
		for key, value := range cfg.Headers {
			cfg.headers.Set(key, value)
		}
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}

	switch cfg.Failover {
	case "":
		cfg.Failover = FailoverIdempotent
	case FailoverIdempotent, FailoverUnsent, FailoverAlways:
	default:
		entries = append(entries, errorstack.Entry{
			Message: `Must be one of "idempotent", "unsent" or "always"`,
			Path:    []any{"config", "failover"},
		})
	}

	if cfg.HealthPath != "" {
		ref, err := url.Parse(cfg.HealthPath)
		if err != nil || !relative(ref) {
			entries = append(entries, errorstack.Entry{
				Message: `Must start with "/" or "?"`,
				Path:    []any{"config", "health_path"},
			})
		} else {
			cfg.health = ref
		}
	}

	if cfg.Transport != nil && cfg.TLS.Enabled {
		entries = append(entries, errorstack.Entry{
			Message: "Must not be enabled with a custom transport; configure TLS on the transport instead",
			Path:    []any{"config", "tls"},
		})
	}

	entries = append(entries, cfg.TLS.Sanitize()...)
	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	return nil
}

// name returns the name the connection registers under, falling back to the
// integration's identifier when the Config leaves it empty.
func (cfg *Config) name() string {
	if cfg.Name != "" {
		return cfg.Name
	}

	return identifier
}

// parseEndpoint parses a normalized endpoint. It returns the message of the
// validation entry describing why it cannot be one, or an empty message when it
// can.
//
// The parts it refuses are the ones that would end up in every request URL, and
// from there in every error and log line naming one: credentials, a query string
// carrying an API key. A fragment is never sent at all.
func parseEndpoint(raw string) (*url.URL, string) {
	u, err := url.Parse(raw)

	switch {
	case err != nil || u.Host == "":
		return nil, "Must be a valid absolute URL"
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, `Must use the "http" or "https" scheme`
	case u.User != nil:
		return nil, "Must not contain credentials; set them in headers instead"
	case u.RawQuery != "" || u.ForceQuery:
		return nil, "Must not contain a query string"
	case strings.Contains(raw, "#"):
		return nil, "Must not contain a fragment"
	}

	return u, ""
}

// relative reports whether ref only names a path and a query below an endpoint:
// no scheme, no host, no credentials, and a path that is empty or starts with a
// slash.
//
// This is what keeps a request on the endpoint's host. A path is appended to the
// endpoint, so "@evil.tld/x" would turn https://api.tld into
// https://api.tld@evil.tld/x, whose host is evil.tld; ".evil.tld" and ":8443"
// would rewrite the host and the port the same way.
func relative(ref *url.URL) bool {
	return ref.Scheme == "" && ref.Opaque == "" && ref.User == nil && ref.Host == "" &&
		(ref.Path == "" || strings.HasPrefix(ref.Path, "/"))
}
