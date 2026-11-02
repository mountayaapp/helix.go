package rest

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service"

	"github.com/getkin/kin-openapi/routers"
	"github.com/uptrace/bunrouter"
)

// REST is the handle New returns: the routes of the API, and the handler that
// serves them.
//
// It carries no lifecycle. The Service the server was registered with starts and
// drains it; the caller only gets to register routes on it.
type REST interface {
	Router

	// Handler returns the http.Handler the server serves: the probes, every route,
	// the OpenAPI validation, Config.Middleware, Config.EdgeMiddleware and the
	// telemetry around all of them. It is what a test drives with net/http/httptest to exercise the API
	// without a listener.
	Handler() http.Handler
}

// Router registers routes. Every method accepts RouteOption variadically, so a
// route declares its own policy where its path is declared — the only place the
// route is known before the router has matched anything.
//
// A path is a pattern: "/things/:id" matches one segment and binds it to "id",
// and "/files/*path" matches the rest of the path. ParamsFromContext and
// ParamFromContext read the values back in the handler.
//
// A GET route answers HEAD too, unless Config.NoImplicitHead is set. A request
// for a method the path does not take is answered 405, with an Allow header
// listing the ones it does. A path that names a route only once it is cleaned up,
// or once a trailing slash is dropped or added, is answered 308 to the route's
// own path for a GET or a HEAD, and 404 for any other method.
//
// Registering a route twice, a malformed path, a nil handler, or a method other
// than the seven this interface names panics: an invalid route is a programming
// error, found the first time the program starts. The probes already hold
// GET /health and GET /ready.
type Router interface {

	// GET registers handler for GET requests to path.
	GET(path string, handler http.HandlerFunc, opts ...RouteOption)

	// HEAD registers handler for HEAD requests to path. A GET route answers HEAD
	// with its own handler unless Config.NoImplicitHead is set, so a HEAD route of
	// its own is only needed to answer differently, and is registered before the
	// GET of the same path: registered after it, it panics.
	HEAD(path string, handler http.HandlerFunc, opts ...RouteOption)

	// POST registers handler for POST requests to path.
	POST(path string, handler http.HandlerFunc, opts ...RouteOption)

	// PUT registers handler for PUT requests to path.
	PUT(path string, handler http.HandlerFunc, opts ...RouteOption)

	// PATCH registers handler for PATCH requests to path.
	PATCH(path string, handler http.HandlerFunc, opts ...RouteOption)

	// DELETE registers handler for DELETE requests to path.
	DELETE(path string, handler http.HandlerFunc, opts ...RouteOption)

	// OPTIONS registers handler for OPTIONS requests to path. A CORS preflight is
	// usually answered by Config.EdgeMiddleware before any route is matched, and
	// needs no route of its own.
	OPTIONS(path string, handler http.HandlerFunc, opts ...RouteOption)

	// Handle registers handler for requests to path with the given method, which
	// is one of the seven above. It is the form to use when the method is a
	// variable, or when the handler is an http.Handler rather than a function.
	Handle(method string, path string, handler http.Handler, opts ...RouteOption)

	// Group returns a Router registering its routes under prefix, with opts
	// applied to each of them before the route's own. Groups nest.
	//
	//	stream := api.Group("/stream", rest.WithoutTimeout())
	//	stream.GET("/events", handler) // GET /stream/events, unbounded
	Group(prefix string, opts ...RouteOption) Router
}

// rest represents the rest integration. It implements REST and nothing else: its
// lifecycle is an integration.HTTPLifecycle the Service holds, which no type
// assertion on this value can reach.
type rest struct {

	// routes registers the application's routes. It is embedded, so the REST
	// handle registers on the root of the application's route group.
	routes

	// config holds the Config initially passed when creating a new REST API.
	config *Config

	// inst names this server to the telemetry pipeline.
	inst *integration.Instrument

	// http instruments the inbound requests. It is built once here rather than per
	// request, since it registers the duration and size instruments.
	http *integration.HTTPServer

	// probes answer the liveness and readiness endpoints. They report on the
	// Service, so readiness covers every integration attached to it.
	probes integration.HTTPProbes

	// bun is the underlying router. This package has been designed to easily
	// switch from one underlying router to another if necessary, in case one goes
	// unmaintained or doesn't meet our requirements anymore.
	bun *bunrouter.Router

	// oapirouter is the OpenAPI router used to validate requests and responses
	// against the OpenAPI description passed in Config.
	oapirouter routers.Router

	// handler is the whole handler chain the server serves. See buildHandler.
	handler http.Handler

	// redactedRoutes holds the templates of the routes registered WithRedactedPath,
	// for middlewareRoute to look up the route it matched in.
	redactedRoutes sync.Map

	// methods records the methods registered on each route, for the Allow header
	// of a 405 and the HEAD a GET implies.
	methods routeMethods

	// slashRoutes reports that a route's template ends in a slash, so a path
	// without one may still name a route once one is added. See canonicalPaths.
	slashRoutes atomic.Bool
}

// New tries to build a new HTTP API server for Config, and registers it with svc,
// which starts it with Run and drains it with Stop. Returns an error if Config or
// the OpenAPI descriptions are not valid.
//
// ctx bounds the loading of the OpenAPI descriptions, which may be fetched over
// the network.
func New(ctx context.Context, svc *service.Service, cfg Config) (REST, error) {
	if svc == nil {
		return nil, errorstack.NewValidation(errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"service"},
		})
	}

	// No need to continue if Config is not valid.
	err := cfg.sanitize()
	if err != nil {
		return nil, err
	}

	inst, err := integration.NewInstrument(integration.InstrumentConfig{
		Identifier:     identifier,
		Humanized:      humanized,
		Name:           cfg.name(),
		TracerProvider: service.TracerProvider(svc),
		MeterProvider:  service.MeterProvider(svc),
		Propagator:     service.Propagator(svc),
	})
	if err != nil {
		return nil, err
	}

	r := &rest{
		config: &cfg,
		inst:   inst,
		http: inst.NewHTTPServer(integration.HTTPServerOptions{
			PublicEndpoint: cfg.PublicEndpoint,
		}),
		probes: integration.HTTPProbes{
			CheckHealth: svc.CheckHealth,
			Readiness:   cfg.Readiness,
		},
	}

	// Only try to build the OpenAPI router if enabled in Config. It is built before
	// any route is registered: each route's validation matches against it.
	if cfg.OpenAPI.Enabled {
		r.oapirouter, err = NewOpenAPIRouter(ctx, cfg.OpenAPI.Descriptions...)
		if err != nil {
			return nil, errorstack.NewValidation(errorstack.Nest(err, "config", "openapi", "descriptions")...)
		}
	}

	// Already validated by sanitize, which is why no entries are expected here; they
	// are still checked, since a server silently listening without TLS is the one
	// outcome this must never have.
	tlsConfig, entries := cfg.TLS.ToServerTLS()
	if len(entries) > 0 {
		return nil, errorstack.NewValidation(entries...)
	}

	r.buildRouter()
	r.handler = r.buildHandler()

	// Build the HTTP server here rather than in Start. Start runs in a goroutine the
	// Service owns and Stop runs in another, so a server built there is written by
	// one and read by the other. Building it now also means a malformed key pair
	// fails New, where the caller can act on it, rather than surfacing as a server
	// that quietly failed to start after the process reported itself healthy.
	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           r.handler,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
	}

	// Hand the server to the Service, which owns its lifecycle from here. Request
	// contexts derive from the Service's, so a handler's logs and spans go to the
	// Service's providers even when it was told to leave the globals alone.
	lifecycle := integration.NewHTTPLifecycle(service.Context(context.Background(), svc), server)
	if err := service.Serve(svc, cfg.name(), lifecycle); err != nil {
		return nil, err
	}

	return r, nil
}

// Handler returns the handler chain the server serves.
func (r *rest) Handler() http.Handler {
	return r.handler
}

// buildHandler builds the handler chain the server serves, from the outside in:
//
//   - the entry span and the request measurements, opened before anything else so
//     that every response is traced, whoever writes it;
//   - for the probes, the router directly: they answer without authentication or
//     any other application policy, and are never validated against a description;
//   - for everything else, Config.EdgeMiddleware, then the answer to a panic in
//     anything further in, then the cross-origin protection and the body limit,
//     then the answer to a path that names a route only once it is made
//     canonical, then Config.Middleware, then the stamping of the Event either
//     middleware put in the context, then the router.
//
// The router names the span after the route it matched, which is the one thing
// worth having in a span name that only the router knows.
func (r *rest) buildHandler() http.Handler {
	app := integration.StampEventMiddleware(r.bun)
	if r.config.Middleware != nil {
		app = r.config.Middleware(app)
	}

	app = r.canonicalPaths(app)
	app = r.limitRequestBody(app)
	if r.config.CrossOriginProtection != nil {
		app = r.protectCrossOrigin(app)
	}

	app = recoverPanics(app)
	if r.config.EdgeMiddleware != nil {
		app = r.config.EdgeMiddleware(app)
	}

	return r.http.Middleware(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.URL.Path == integration.PathLiveness || req.URL.Path == integration.PathReadiness {
			r.bun.ServeHTTP(rw, req)
			return
		}

		app.ServeHTTP(rw, req)
	}))
}

// buildRouter builds the HTTP router. It comes with opinionated handlers for 404
// and 405 HTTP errors, as well as for the probes.
//
// The probes are registered on the router directly, and the application's routes
// through route, which is where the OpenAPI validation runs: a probe is not in any
// description, and validating it would only ever report that.
func (r *rest) buildRouter() {
	r.bun = bunrouter.New(
		bunrouter.Use(r.middlewareRoute),
		bunrouter.WithNotFoundHandler(r.handlerNotFound),
		bunrouter.WithMethodNotAllowedHandler(r.handlerMethodNotAllowed),
	)

	r.probe(integration.PathLiveness, r.probes.HandleLiveness)
	r.probe(integration.PathReadiness, r.probes.HandleReadiness)

	r.routes = routes{r: r, group: &r.bun.Group}
}

// probe registers a probe handler for GET requests to path, and for HEAD ones as
// a GET route of the application's would be.
func (r *rest) probe(path string, handler http.HandlerFunc) {
	fn := func(rw http.ResponseWriter, req bunrouter.Request) error {
		handler(rw, req.Request)
		return nil
	}

	r.bun.GET(path, fn)
	if r.methods.add(http.MethodGet, path, !r.config.NoImplicitHead) {
		r.bun.HEAD(path, fn)
	}
}

// limitRequestBody refuses a request whose declared body is over
// Config.MaxRequestBodyBytes, and caps the reading of any other.
//
// A body declared too large is refused before anything reads it, with the REST
// error envelope. A body sent without a length is cut off at the limit instead,
// and whatever reads it gets an *http.MaxBytesError; net/http reads no more than
// a bounded remainder of it once the handler returns.
func (r *rest) limitRequestBody(next http.Handler) http.Handler {
	limit := r.config.MaxRequestBodyBytes

	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.ContentLength > limit {
			NewResponseError[NoMetadata](req).
				SetStatus(http.StatusRequestEntityTooLarge).
				Write(rw)
			return
		}

		if req.Body != nil && req.Body != http.NoBody {
			req.Body = http.MaxBytesReader(rw, req.Body, limit)
		}

		next.ServeHTTP(rw, req)
	})
}

// protectCrossOrigin refuses the non-safe cross-origin browser requests
// Config.CrossOriginProtection detects, with the REST error envelope.
//
// It calls Check rather than wrapping with the protection's own Handler, whose
// refusal is a plain-text body: every error this server answers with is the same
// envelope. The protection is only read, never configured here, since it is the
// caller's value.
func (r *rest) protectCrossOrigin(next http.Handler) http.Handler {
	protection := r.config.CrossOriginProtection

	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if err := protection.Check(req); err != nil {
			NewResponseError[NoMetadata](req).
				SetStatus(http.StatusForbidden).
				Write(rw)
			return
		}

		next.ServeHTTP(rw, req)
	})
}
