package graphql

import (
	"net/http"

	"github.com/mountayaapp/helix.go/service"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
)

/*
graphql represents the graphql integration. It respects the integration.Server
interface.
*/
type graphql struct {

	// config holds the Config initially passed when creating a new GraphQL API.
	config *Config

	// mux is the HTTP serve mux used to route requests to the GraphQL handler
	// and the health endpoint.
	mux *http.ServeMux

	// server is the standard http.Server used to serve HTTP requests.
	server *http.Server
}

/*
New tries to build a new GraphQL API server for Config. Returns an error if
Config is not valid.
*/
func New(cfg Config) error {

	// No need to continue if Config is not valid.
	err := cfg.sanitize()
	if err != nil {
		return err
	}

	g := &graphql{
		config: &cfg,
	}

	// Create the gqlgen handler with the executable schema and add the POST
	// transport for handling GraphQL requests.
	gqlHandler := handler.New(cfg.Schema)
	gqlHandler.AddTransport(transport.POST{})

	// Enable introspection when GraphiQL is enabled, so the schema can be
	// fetched by the IDE.
	if cfg.GraphiQL.Enabled {
		gqlHandler.Use(extension.Introspection{})
	}

	// Enable Automatic Persisted Queries when cache is configured.
	if cfg.APQ.Enabled {
		gqlHandler.Use(extension.AutomaticPersistedQuery{Cache: &valkeyCache{
			prefix: cfg.APQ.Prefix,
			ttl:    cfg.APQ.TTL,
			client: cfg.APQ.Valkey,
		}})
	}

	// Build the HTTP serve mux with the health, GraphQL (POST + OPTIONS for
	// CORS preflight), method not allowed, and catch-all not found endpoints.
	g.mux = http.NewServeMux()
	g.mux.HandleFunc("GET /health", g.handlerHealthcheck)
	g.mux.Handle("POST "+cfg.Path, gqlHandler)
	g.mux.Handle("OPTIONS "+cfg.Path, gqlHandler)
	g.mux.HandleFunc(cfg.Path, g.handlerMethodNotAllowed)
	if cfg.GraphiQL.Enabled {
		g.mux.Handle("GET "+cfg.GraphiQL.Path, playground.Handler("GraphiQL", cfg.Path))
	}

	g.mux.HandleFunc("/", g.handlerNotFound)

	// Try to register the server integration to the service.
	if err := service.Serve(g); err != nil {
		return err
	}

	return nil
}
