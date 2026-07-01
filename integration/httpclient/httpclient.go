package httpclient

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync/atomic"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/service"
	"github.com/mountayaapp/helix.go/telemetry/trace"
)

/*
Pre-computed span names to avoid allocations on every call.
*/
const (
	spanRequest = humanized + ": Request"
	spanStatus  = humanized + ": Status"
)

/*
HTTPClient exposes an opinionated way to call an external HTTP API across one or
more interchangeable endpoints, with automatic round-robin, failover, and
distributed tracing.
*/
type HTTPClient interface {

	// Do sends an HTTP request with the given method and body to {endpoint}{path},
	// where endpoint is selected by round-robin. On a transport error or a 5xx
	// response it fails over to the next endpoint; any response below 500 (including
	// 4xx) is returned as is. The body is replayed on each attempt.
	//
	// Each RequestOption is applied to every attempt and takes precedence over the
	// client's configured default Headers.
	//
	// It returns the raw response: the caller is responsible for closing resp.Body
	// and decoding the payload.
	Do(ctx context.Context, method, path string, body []byte, opts ...RequestOption) (*http.Response, error)

	// Get is a convenience for a GET request. The path may carry a query string.
	// Each RequestOption is applied to the request and takes precedence over the
	// client's configured default Headers.
	Get(ctx context.Context, path string, opts ...RequestOption) (*http.Response, error)
}

/*
connection represents the HTTP client integration. It respects the
integration.Dependency and HTTPClient interfaces.
*/
type connection struct {

	// config holds the Config initially passed when creating a new client.
	config *Config

	// client is the underlying HTTP client shared across all endpoints.
	client *http.Client

	// endpoints is the normalized list of base URLs to round-robin across.
	endpoints []string

	// index is the round-robin cursor.
	index atomic.Uint64

	// single is true when there is exactly one endpoint, enabling a fast path.
	single bool
}

/*
Connect creates an HTTP client given the Config and attaches it to the Service as
a dependency. Returns an error if Config is not valid.

It does not probe the endpoints: an unreachable endpoint is reported through
Status, not at startup, so a temporarily down upstream does not prevent the
Service from starting.
*/
func Connect(svc *service.Service, cfg Config) (HTTPClient, error) {

	// No need to continue if Config is not valid.
	err := cfg.sanitize()
	if err != nil {
		return nil, err
	}

	transport := newTunedTransport()

	// Set TLS options only if enabled in Config.
	if cfg.TLS.Enabled {
		tlsConfig, entries := cfg.TLS.ToStandardTLS()
		if len(entries) > 0 {
			return nil, errorstack.NewValidation(entries...)
		}

		transport.TLSClientConfig = tlsConfig
	}

	conn := &connection{
		config:    &cfg,
		client:    &http.Client{Timeout: cfg.Timeout, Transport: transport},
		endpoints: cfg.Endpoints,
		single:    len(cfg.Endpoints) == 1,
	}

	// Try to attach the integration to the service.
	if err := service.Attach(svc, conn); err != nil {
		return nil, err
	}

	return conn, nil
}

/*
attempt builds and sends a single request to a specific endpoint, applying each
RequestOption first and then the configured default headers when not already
present on the request - so per-request options take precedence over defaults.
*/
func (conn *connection) attempt(ctx context.Context, endpoint, method, path string, body []byte, opts ...RequestOption) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint+path, reader)
	if err != nil {
		return nil, err
	}

	for _, opt := range opts {
		opt(req)
	}

	for key, value := range conn.config.Headers {
		if req.Header.Get(key) == "" {
			req.Header.Set(key, value)
		}
	}

	return conn.client.Do(req)
}

/*
Do automatically handles round-robin, failover, and tracing.
*/
func (conn *connection) Do(ctx context.Context, method, path string, body []byte, opts ...RequestOption) (*http.Response, error) {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanRequest)
	defer span.End()

	// Fast path: a single endpoint has nothing to fail over to.
	if conn.single {
		resp, err := conn.attempt(ctx, conn.endpoints[0], method, path, body, opts...)
		if err != nil {
			span.RecordError("failed to execute request", err)
		}

		setRequestAttributes(span, conn.endpoints[0], method, statusOf(resp))
		return resp, err
	}

	start := conn.index.Add(1) - 1
	count := uint64(len(conn.endpoints))

	var (
		lastResp     *http.Response // most recent 5xx response retained for the caller
		lastErr      error          // most recent transport error
		lastEndpoint string         // endpoint lastResp came from, for span attributes
		endpoint     string         // most recently attempted endpoint
	)

	for i := range count {

		// Stop trying further endpoints if the context is done.
		if err := ctx.Err(); err != nil {
			drainClose(lastResp)
			span.RecordError("failed to execute request", err)
			return nil, err
		}

		endpoint = conn.endpoints[(start+i)%count]

		resp, err := conn.attempt(ctx, endpoint, method, path, body, opts...)
		if err == nil && resp.StatusCode < 500 {

			// Success: drain any 5xx response retained from an earlier endpoint so
			// its connection returns to the pool instead of leaking.
			drainClose(lastResp)
			setRequestAttributes(span, endpoint, method, resp.StatusCode)
			return resp, nil
		}

		// A transport error keeps the last error but must not discard a 5xx response
		// already retained from an earlier endpoint: the contract surfaces the last
		// 5xx response when there is one, otherwise the last transport error.
		if err != nil {
			lastErr = err
			continue
		}

		// Retryable 5xx response: supersede the previously retained one and remember
		// its endpoint, then try the next endpoint.
		drainClose(lastResp)
		lastResp, lastEndpoint = resp, endpoint
	}

	// Every endpoint failed. Surface the last 5xx response when there is one,
	// otherwise the last transport error.
	if lastResp != nil {
		setRequestAttributes(span, lastEndpoint, method, lastResp.StatusCode)
		return lastResp, nil
	}

	span.RecordError("failed to execute request", lastErr)
	setRequestAttributes(span, endpoint, method, 0)
	return nil, lastErr
}

/*
Get is a convenience for a GET request.
*/
func (conn *connection) Get(ctx context.Context, path string, opts ...RequestOption) (*http.Response, error) {
	return conn.Do(ctx, http.MethodGet, path, nil, opts...)
}
