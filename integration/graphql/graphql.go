package graphql

import (
	"context"
	"net/http"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service"
	"github.com/mountayaapp/helix.go/telemetry/log"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/vektah/gqlparser/v2/ast"
)

// graphql represents the graphql integration: the routing and the gqlgen server
// behind it. Its lifecycle is not its own: New hands the HTTP server it builds
// to the Service, through an integration.HTTPLifecycle.
type graphql struct {

	// config holds the Config initially passed when creating a new GraphQL API.
	config *Config

	// inst names this server to the telemetry pipeline. Every span this
	// integration opens - the HTTP entry span and the per-operation spans the
	// gqlgen extension adds - is opened through it.
	inst *integration.Instrument

	// http instruments the inbound requests. It is built once here rather than
	// per request, since it registers the duration and size instruments.
	http *integration.HTTPServer

	// probes answer the liveness and readiness endpoints. They report on the
	// Service, so readiness covers every integration attached to it.
	probes integration.HTTPProbes

	// post is the transport gqlgen serves GraphQL requests with, held so that
	// handleGraphQL checks requests against the very transport that will serve
	// them.
	post transport.POST

	// gql is the gqlgen server executing GraphQL requests.
	gql *handler.Server

	// mux is the HTTP serve mux used to route requests to the GraphQL handler
	// and the health endpoints.
	mux *http.ServeMux
}

// New tries to build a new GraphQL API server for Config, and registers it with
// the Service, which starts it in Run and drains it in Stop. Returns an error if
// svc is nil or Config is not valid.
func New(ctx context.Context, svc *service.Service, cfg Config) error {
	if svc == nil {
		return errorstack.NewValidation(errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"service"},
		})
	}

	// No need to continue if Config is not valid.
	if err := cfg.sanitize(); err != nil {
		return err
	}

	// Built here rather than when the server starts, so that a malformed key pair
	// fails New rather than a goroutine the Service owns.
	tlsConfig, entries := cfg.TLS.ToServerTLS()
	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	g, err := build(svc, &cfg)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           g.handler(),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		TLSConfig:         tlsConfig,
	}

	// Hand the server to the Service, which owns its lifecycle from here. Request
	// contexts derive from the Service's, so that handlers log and trace through
	// the Service's providers.
	lifecycle := integration.NewHTTPLifecycle(service.Context(context.Background(), svc), server)
	if err := service.Serve(svc, cfg.name(), lifecycle); err != nil {
		return err
	}

	// GraphiQL loads the schema by introspecting it. Not refused, since serving the
	// IDE to write operations by hand is a choice someone can make, but almost
	// certainly not the intended one.
	if cfg.GraphiQL.Enabled && !cfg.Introspection.Enabled {
		log.Warn(service.Context(ctx, svc), "GraphiQL is enabled but introspection is not, so it cannot load the schema",
			log.String("graphql.name", cfg.name()),
		)
	}

	return nil
}

// build builds the integration for a sanitized Config: its instrumentation, the
// gqlgen server and the routing in front of it. New and the tests share it, so
// both exercise the same server.
func build(svc *service.Service, cfg *Config) (*graphql, error) {
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

	g := &graphql{
		config: cfg,
		inst:   inst,
		http: inst.NewHTTPServer(integration.HTTPServerOptions{
			PublicEndpoint: cfg.PublicEndpoint,
		}),
		probes: integration.HTTPProbes{
			CheckHealth: svc.CheckHealth,
			Readiness:   cfg.Readiness,
		},
	}

	g.buildGQL()
	g.buildMux()

	return g, nil
}

// buildGQL builds the gqlgen server.
func (g *graphql) buildGQL() {
	cfg := g.config

	// Serve POST, and answer OPTIONS with the methods the endpoint allows. Wire the
	// errorstack-aware error presenter so resolver errors carry path/extensions
	// consistently with the rest of helix.go, and so that an error nobody wrote
	// for a client is not shown to one.
	g.gql = handler.New(cfg.Schema)
	g.gql.AddTransport(transport.Options{AllowedMethods: []string{http.MethodOptions, http.MethodPost}})
	g.gql.AddTransport(g.post)
	g.gql.SetErrorPresenter(errorPresenter)
	g.gql.SetRecoverFunc(recoverFunc)

	// Cache parsed documents. handler.New installs no caches at all — only the
	// deprecated NewDefaultServer does — so without this every request re-lexes,
	// re-parses and re-validates its query against the schema, including the
	// handful of operations a typical client sends over and over.
	g.gql.SetQueryCache(lru.New[*ast.QueryDocument](cfg.QueryCacheSize))

	// gqlgen sets no token limit of its own: the parser reads whatever it is given.
	g.gql.SetParserTokenLimit(cfg.ParserTokenLimit)

	// A validation error suggesting the field a client probably meant names a
	// field of the schema. With introspection off, that is the schema leaking one
	// typo at a time.
	g.gql.SetDisableSuggestion(!cfg.Introspection.Enabled)

	// Trace the execution itself, not just the request that carried it. A GraphQL
	// endpoint serves every operation from one path, so the HTTP span is the one
	// span that cannot say what was asked for. Registered first, so that its
	// operation span is the outermost and covers every other extension.
	g.gql.Use(&telemetry{inst: g.inst, resolvers: cfg.TraceResolvers})

	// Enable introspection when configured, so clients can discover the schema
	// via __schema and __type queries.
	if cfg.Introspection.Enabled {
		g.gql.Use(extension.Introspection{})
	}

	// Enable Automatic Persisted Queries when a cache is configured. The write the
	// extension makes is held back until the operation has passed the limits
	// below; see apqCache.
	if cfg.APQ.Enabled {
		g.gql.Use(extension.AutomaticPersistedQuery{Cache: apqCache{
			cache:          cfg.APQ.Cache,
			maxQueryLength: cfg.APQ.MaxQueryLength,
		}})
	}

	// Refuse an operation too deep or too costly before any of it executes. The
	// depth check runs first: it is the cheaper of the two, and walks a document
	// the complexity calculation would otherwise recurse through in full.
	g.gql.Use(depthLimit{limit: cfg.DepthLimit})
	g.gql.Use(extension.FixedComplexityLimit(cfg.ComplexityLimit))

	if cfg.APQ.Enabled {
		g.gql.Use(apqCommit{cache: cfg.APQ.Cache})
	}

	// Last, so that whatever the application changes is changed on top of the
	// above rather than overwritten by it.
	if cfg.Configure != nil {
		cfg.Configure(g.gql)
	}
}

// buildMux builds the integration's HTTP serve mux: the health and readiness
// probes, the GraphQL endpoint (POST, and OPTIONS for the methods it allows),
// GraphiQL when enabled, and the method-not-allowed and not-found fallbacks.
func (g *graphql) buildMux() {
	cfg := g.config

	g.mux = http.NewServeMux()
	g.mux.HandleFunc("GET "+integration.PathLiveness, g.probes.HandleLiveness)
	g.mux.HandleFunc("GET "+integration.PathReadiness, g.probes.HandleReadiness)
	g.mux.HandleFunc("POST "+cfg.Path, g.handleGraphQL)
	g.mux.Handle("OPTIONS "+cfg.Path, g.gql)
	g.mux.HandleFunc(cfg.Path, g.handlerMethodNotAllowed)
	if cfg.GraphiQL.Enabled {
		g.mux.Handle("GET "+cfg.GraphiQL.Path, playground.Handler("GraphiQL", cfg.Path))
	}

	g.mux.HandleFunc("/", g.handlerNotFound)
}

// handler returns the handler the HTTP server serves: the mux, wrapped in the
// application's middleware, wrapped in this integration's instrumentation.
func (g *graphql) handler() http.Handler {

	// Event stamping sits innermost, so it runs after the application's
	// middleware has had its chance to put an Event in the request context.
	inner := integration.StampEventMiddleware(g.mux)

	// Wrap the built-in HTTP handler with the one given by the user, if applicable.
	// Skip user middleware for the health endpoints so they always respond without
	// requiring authentication or other service-level checks. Config.sanitize
	// refuses a GraphQL or GraphiQL path equal to one of them, so this cannot take
	// either outside the middleware.
	h := inner
	if g.config.Middleware != nil {
		wrapped := g.config.Middleware(inner)
		h = http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == integration.PathLiveness || req.URL.Path == integration.PathReadiness {
				inner.ServeHTTP(rw, req)
				return
			}

			wrapped.ServeHTTP(rw, req)
		})
	}

	// Wrap the handler previously built with this integration's own
	// instrumentation, outermost so that the span covers the application's
	// middleware too. It speaks the same vocabulary as the other eight
	// integrations rather than the semantic conventions otelhttp emits — see the
	// package overview of service.
	return g.http.Middleware(h)
}
