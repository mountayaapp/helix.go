package httpclient

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"slices"
	"sync/atomic"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service"
	"github.com/mountayaapp/helix.go/telemetry/trace"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// HTTPClient exposes an opinionated way to call an external HTTP API across one or
// more interchangeable endpoints, with automatic round-robin, failover, and
// distributed tracing.
//
// A request path is appended to the endpoint it is sent to, base path included:
// with the endpoint https://api.tld/v1, the path /things?limit=10 is sent to
// https://api.tld/v1/things?limit=10. It must be empty or start with "/" or "?",
// so that no path can name another host, and it is sent as written: escape any
// value interpolated into it with url.PathEscape or url.QueryEscape.
//
// A request that fails — a transport error, or a 5xx response — is sent again to
// the next endpoint when Config.Failover allows it; any response below 500,
// including a 4xx, is returned as is. When every endpoint failed, the last 5xx
// response is returned if there is one, and the last transport error otherwise.
// A 429 or 503 response carrying Retry-After also takes its endpoint out of the
// rotation for that long, as long as another endpoint is left in it. The client
// never waits on the caller's behalf: failing over is immediate, and a call is
// bounded by Config.Timeout per attempt and by the context as a whole.
type HTTPClient interface {

	// Do sends an HTTP request with the given method, path and body, and returns
	// the response. The body is replayed on every endpoint failed over to.
	//
	// Each RequestOption is applied once, before the first attempt, and takes
	// precedence over the client's configured default Headers.
	//
	// It returns the raw response: the caller is responsible for closing resp.Body
	// and decoding the payload.
	Do(ctx context.Context, method, path string, body []byte, opts ...RequestOption) (*http.Response, error)

	// Get is a convenience for a GET request. The path may carry a query string.
	// Each RequestOption is applied to the request and takes precedence over the
	// client's configured default Headers.
	Get(ctx context.Context, path string, opts ...RequestOption) (*http.Response, error)

	// DoRequest sends req, for full control over the request: a body streamed from
	// an io.Reader, a header set Do has no option for. The request is sent through
	// the same endpoints, failover, redirect policy and tracing as Do, and its
	// context is the one the request carries.
	//
	// req.URL must be relative, holding only the path and the query: it is
	// resolved against each endpoint the way Do resolves a path, and refused like
	// one when it names a scheme or a host. Build it with
	// http.NewRequestWithContext(ctx, method, "/things", body). The client's
	// default Headers fill in what req.Header does not set.
	//
	// req is not modified. A body can only be sent to another endpoint when
	// req.GetBody can produce it again, which http.NewRequestWithContext sets up
	// for a *bytes.Buffer, *bytes.Reader or *strings.Reader; a request whose body
	// cannot be replayed is never failed over.
	DoRequest(req *http.Request) (*http.Response, error)
}

// connection represents the HTTP client integration. It respects the
// integration.Dependency and HTTPClient interfaces.
type connection struct {

	// config holds the Config initially passed when creating a new client.
	config *Config

	// inst names this client to the telemetry pipeline. Every span below is
	// opened through it, which is what keeps the naming the same here as in the
	// eight other integrations.
	inst *integration.Instrument

	// client is the underlying HTTP client shared across all endpoints.
	client *http.Client

	// endpoints are the base URLs to round-robin across, in configuration order.
	// The probe shares them, and with them which ones are paused.
	endpoints []*endpoint

	// index is the round-robin cursor.
	index atomic.Uint64

	// probe is this connection as a health check sees it: the same endpoints,
	// headers and policies, sending through the same transport, but tracing,
	// measuring and propagating nothing. Readiness is polled every few seconds by
	// every replica, and a span and a measurement per poll would bury the traffic
	// they exist to describe. The probe's own probe is nil.
	probe *connection
}

// Connect creates an HTTP client given the Config and attaches it to the Service as
// a dependency. Returns an error if Config is not valid.
//
// It does not probe the endpoints: an unreachable endpoint is reported through
// the Service's readiness when probing is configured, not at startup, so a
// temporarily down upstream does not prevent the Service from starting.
func Connect(ctx context.Context, svc *service.Service, cfg Config) (HTTPClient, error) {

	// No need to continue if Config is not valid.
	err := cfg.sanitize()
	if err != nil {
		return nil, err
	}

	propagator := cfg.Propagator
	if propagator == nil {
		propagator = service.Propagator(svc)
	}

	conn, err := newConnection(&cfg, integration.InstrumentConfig{
		Identifier:     identifier,
		Humanized:      humanized,
		Name:           cfg.name(),
		TracerProvider: service.TracerProvider(svc),
		MeterProvider:  service.MeterProvider(svc),
		Propagator:     propagator,
	})
	if err != nil {
		return nil, err
	}

	// Hand the connection to the Service, which owns its lifecycle from here.
	if err := service.Attach(svc, cfg.name(), dependency{conn: conn}); err != nil {
		return nil, err
	}

	return conn, nil
}

// newConnection builds the connection a sanitized Config describes, with its
// telemetry going where instCfg says.
func newConnection(cfg *Config, instCfg integration.InstrumentConfig) (*connection, error) {
	base := cfg.Transport
	if base == nil {
		transport := newTunedTransport()

		// Set TLS options only if enabled in Config.
		if cfg.TLS.Enabled {
			tlsConfig, entries := cfg.TLS.ToStandardTLS()
			if len(entries) > 0 {
				return nil, errorstack.NewValidation(entries...)
			}

			transport.TLSClientConfig = tlsConfig
		}

		base = transport
	}

	inst, err := integration.NewInstrument(instCfg)
	if err != nil {
		return nil, err
	}

	silentCfg := instCfg
	silentCfg.TracerProvider = tracenoop.NewTracerProvider()
	silentCfg.MeterProvider = metricnoop.NewMeterProvider()
	silentCfg.Propagator = propagation.NewCompositeTextMapPropagator()

	silent, err := integration.NewInstrument(silentCfg)
	if err != nil {
		return nil, err
	}

	endpoints := newEndpoints(cfg.urls)
	conn := &connection{
		config:    cfg,
		inst:      inst,
		client:    cfg.newClient(inst, base),
		endpoints: endpoints,
		probe: &connection{
			config:    cfg,
			inst:      silent,
			client:    cfg.newClient(silent, base),
			endpoints: endpoints,
		},
	}

	return conn, nil
}

// newClient returns the *http.Client sending through base, instrumented by inst.
//
// The transport is instrumented rather than each attempt being described by hand.
// One span per transport attempt is the right granularity here, since a single
// logical request may touch several endpoints, and it is the only layer that sees
// the attempt at all: failover happens above it, redirects below.
//
// It is also where the trace context is injected into the outgoing headers,
// which is what makes a call from one helix service to another one trace.
func (cfg *Config) newClient(inst *integration.Instrument, base http.RoundTripper) *http.Client {
	return &http.Client{
		Timeout:       max(cfg.Timeout, 0),
		Transport:     inst.NewHTTPTransport().RoundTripper(base),
		CheckRedirect: cfg.checkRedirect,
	}
}

// Do builds the request and sends it through DoRequest.
func (conn *connection) Do(ctx context.Context, method, path string, body []byte, opts ...RequestOption) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, path, reader)
	if err != nil {
		return nil, errorstack.Wrap(redact(err), "Request is not valid")
	}

	for _, opt := range opts {
		opt(req)
	}

	return conn.DoRequest(req)
}

// Get is a convenience for a GET request.
func (conn *connection) Get(ctx context.Context, path string, opts ...RequestOption) (*http.Response, error) {
	return conn.Do(ctx, http.MethodGet, path, nil, opts...)
}

// DoRequest automatically handles round-robin, failover, and tracing.
//
// A request refused before anything is sent opens no span: it never happened,
// and the error returned says why.
func (conn *connection) DoRequest(req *http.Request) (*http.Response, error) {
	if req.URL == nil || !relative(req.URL) {
		return nil, errInvalidPath()
	}

	// Named for the method rather than for the fact that a request happened: the
	// path is the caller's and has no template here, so it stays an attribute of
	// the attempt where its cardinality costs nothing.
	//
	// Internal rather than client: the attempts below are the client spans, one
	// per call that actually left the process. Two client spans nested in each
	// other would be counted as two calls by anything drawing a service map.
	ctx, op := conn.inst.StartOperation(req.Context(), trace.SpanKindInternal, integration.NormalizeMethod(req.Method))
	defer op.End(ctx)

	count := len(conn.endpoints)

	// Fast path: a single endpoint has nothing to round-robin or skip.
	var (
		start      int
		now        time.Time
		skipPaused bool
	)

	if count > 1 {
		start = int((conn.index.Add(1) - 1) % uint64(count))
		now = time.Now()
		skipPaused = slices.ContainsFunc(conn.endpoints, func(e *endpoint) bool {
			return !e.paused(now)
		})
	}

	var (
		retained         *http.Response // most recent 5xx response, kept for the caller
		retainedEndpoint *endpoint      // endpoint retained came from
		lastErr          error          // most recent transport error
		last             *endpoint      // most recently attempted endpoint
		attempts         int
	)

	for i := range count {
		e := conn.endpoints[(start+i)%count]

		// An endpoint that asked to be left alone is skipped, as long as there is
		// another to send to: when every one of them did, the request goes out
		// anyway, since the client has no answer of its own to give.
		if skipPaused && e.paused(now) {
			continue
		}

		// Stop trying further endpoints if the context is done.
		if err := ctx.Err(); err != nil {
			drainClose(retained)
			op.RecordError("failed to execute request", err)
			setRequestAttributes(op, last, 0, attempts)
			return nil, err
		}

		resp, err := conn.attempt(ctx, e, req, attempts == 0)
		attempts++
		last = e

		if err == nil {
			e.honourRetryAfter(resp)

			// Success: drain any 5xx response retained from an earlier endpoint so
			// its connection returns to the pool instead of leaking.
			if resp.StatusCode < http.StatusInternalServerError {
				drainClose(retained)
				setRequestAttributes(op, e, resp.StatusCode, attempts)
				return resp, nil
			}

			// A 5xx supersedes the previously retained one.
			drainClose(retained)
			retained, retainedEndpoint = resp, e
		} else {

			// A transport error keeps the last error but must not discard a 5xx
			// response already retained from an earlier endpoint: the contract
			// surfaces the last 5xx response when there is one, otherwise the last
			// transport error.
			lastErr = err
		}

		if !conn.mayFailover(req, err) {
			break
		}
	}

	// Every endpoint tried failed. Surface the last 5xx response when there is
	// one, otherwise the last transport error. A 5xx is returned rather than
	// turned into an error, but it is still a failure, and recorded as one: the
	// outcome of the operation's measurement would otherwise count it as a
	// success.
	if retained != nil {
		op.RecordError("failed to execute request", statusError(retained.StatusCode))
		setRequestAttributes(op, retainedEndpoint, retained.StatusCode, attempts)
		return retained, nil
	}

	op.RecordError("failed to execute request", lastErr)
	setRequestAttributes(op, last, 0, attempts)
	return nil, lastErr
}

// attempt sends req to a single endpoint. first is true for the first attempt
// of a request, which sends the request's own body; the ones after it send a
// copy from req.GetBody.
//
// The request is cloned rather than modified: it is the caller's, and it is sent
// again, unchanged, to every endpoint failed over to.
func (conn *connection) attempt(ctx context.Context, e *endpoint, req *http.Request, first bool) (*http.Response, error) {
	out := req.Clone(ctx)
	out.URL = e.resolve(req.URL)
	if out.Method == "" {
		out.Method = http.MethodGet
	}

	if !first && req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}

		out.Body = body
	}

	// The default headers fill in what the request does not set, so per-request
	// options take precedence over them. The values are copied, since the request
	// is handed to code that may append to them.
	if out.Header == nil {
		out.Header = make(http.Header, len(conn.config.headers))
	}

	for key, values := range conn.config.headers {
		if _, ok := out.Header[key]; !ok {
			out.Header[key] = slices.Clone(values)
		}
	}

	resp, err := conn.client.Do(out)
	return resp, redact(err)
}

// mayFailover reports whether req, whose last attempt failed with err — or with a
// 5xx response when err is nil — may be sent to the next endpoint. See
// FailoverPolicy.
func (conn *connection) mayFailover(req *http.Request, err error) bool {

	// A body that cannot be produced again cannot be sent again: the attempt that
	// failed consumed it, or closed it, whatever the policy says.
	if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
		return false
	}

	switch conn.config.Failover {
	case FailoverAlways:
		return true
	case FailoverUnsent:
		return err != nil && unsent(err)
	default:
		return idempotent(req, conn.config.headers) || (err != nil && unsent(err))
	}
}
