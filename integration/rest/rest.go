package rest

import (
	"context"
	"net/http"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/service"

	"github.com/getkin/kin-openapi/routers"
	"github.com/uptrace/bunrouter"
	"github.com/uptrace/bunrouter/extra/bunrouterotel"
	"github.com/uptrace/bunrouter/extra/reqlog"
)

/*
REST exposes the HTTP REST API functions. Every verb accepts RouteOption
variadically, so a route declares its own policy where its path is declared —
the only place the route is known before the router has matched anything.
*/
type REST interface {
	GET(path string, handler http.HandlerFunc, opts ...RouteOption)
	DELETE(path string, handler http.HandlerFunc, opts ...RouteOption)
	PATCH(path string, handler http.HandlerFunc, opts ...RouteOption)
	POST(path string, handler http.HandlerFunc, opts ...RouteOption)
	PUT(path string, handler http.HandlerFunc, opts ...RouteOption)
}

/*
rest represents the rest integration. It respects the integration.Server and
REST interfaces.
*/
type rest struct {

	// svc is the Service this integration belongs to.
	svc *service.Service

	// config holds the Config initially passed when creating a new REST API.
	config *Config

	// bun is the underlying router. This package has been designed to easily
	// switch from one underlying router to another if necessary, in case one goes
	// unmaintained or doesn't meet our requirements anymore.
	bun *bunrouter.CompatRouter

	// server is the standard http.Server used to serve HTTP requests.
	server *http.Server

	// oapirouter is the OpenAPI router used to validate requests and responses
	// against the OpenAPI description passed in Config.
	oapirouter routers.Router
}

/*
New tries to build a new HTTP API server for Config. Returns an error if Config
or OpenAPI description are not valid.
*/
func New(svc *service.Service, cfg Config) (REST, error) {

	// No need to continue if Config is not valid.
	err := cfg.sanitize()
	if err != nil {
		return nil, err
	}

	r := &rest{
		svc:    svc,
		config: &cfg,
	}

	var entries []errorstack.Entry
	var routerEntries []errorstack.Entry
	r.bun, routerEntries = r.buildRouter()
	entries = append(entries, routerEntries...)

	// Only try to build the OpenAPI router if enabled in Config.
	if cfg.OpenAPI.Enabled {
		var oapiEntries []errorstack.Entry
		r.oapirouter, oapiEntries = r.buildRouterOpenAPI()
		entries = append(entries, oapiEntries...)
	}

	// Stop here if validation entries were collected.
	if len(entries) > 0 {
		return nil, errorstack.NewValidation(entries...)
	}

	// Otherwise, try to register the server integration to the service.
	if err := service.Serve(svc, r); err != nil {
		return nil, err
	}

	// The integration itself is the router handed back, not the underlying one it
	// delegates to: registration is the only point at which a route's path is
	// known without re-matching it, so it is where per-route policy has to be
	// applied.
	return r, nil
}

/*
GET registers a handler for the GET method at path, under the policy resolved
from opts.
*/
func (r *rest) GET(path string, handler http.HandlerFunc, opts ...RouteOption) {
	r.bun.GET(path, r.applyRouteOptions(handler, opts))
}

/*
DELETE registers a handler for the DELETE method at path, under the policy
resolved from opts.
*/
func (r *rest) DELETE(path string, handler http.HandlerFunc, opts ...RouteOption) {
	r.bun.DELETE(path, r.applyRouteOptions(handler, opts))
}

/*
PATCH registers a handler for the PATCH method at path, under the policy
resolved from opts.
*/
func (r *rest) PATCH(path string, handler http.HandlerFunc, opts ...RouteOption) {
	r.bun.PATCH(path, r.applyRouteOptions(handler, opts))
}

/*
POST registers a handler for the POST method at path, under the policy resolved
from opts.
*/
func (r *rest) POST(path string, handler http.HandlerFunc, opts ...RouteOption) {
	r.bun.POST(path, r.applyRouteOptions(handler, opts))
}

/*
PUT registers a handler for the PUT method at path, under the policy resolved
from opts.
*/
func (r *rest) PUT(path string, handler http.HandlerFunc, opts ...RouteOption) {
	r.bun.PUT(path, r.applyRouteOptions(handler, opts))
}

/*
applyRouteOptions wraps a handler with the policy its route resolved to. A route
that ends up with no budget is handed to the underlying router untouched, so the
unbounded case costs nothing per request.

The budget is applied here rather than in Config.Middleware because middleware
wraps the whole router and therefore runs before any route has matched, where
only the concrete request path is known — recognising a route there would mean
maintaining a second copy of the routing table.
*/
func (r *rest) applyRouteOptions(handler http.HandlerFunc, opts []RouteOption) http.HandlerFunc {
	timeout := r.resolveRouteOptions(opts)
	if timeout <= 0 {
		return handler
	}

	return func(rw http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), timeout)
		defer cancel()

		handler(rw, req.WithContext(ctx))
	}
}

/*
buildRouter tries to build the HTTP router. It comes with opinionated handlers
for 404 and 405 HTTP errors, as well as for the health endpoint.
*/
func (r *rest) buildRouter() (*bunrouter.CompatRouter, []errorstack.Entry) {
	opts := []bunrouter.Option{
		bunrouter.Use(reqlog.NewMiddleware(reqlog.WithEnabled(false))),
		bunrouter.Use(bunrouterotel.NewMiddleware(bunrouterotel.WithClientIP())),
		bunrouter.WithNotFoundHandler(r.handlerNotFound),
		bunrouter.WithMethodNotAllowedHandler(r.handlerMethodNotAllowed),
	}

	if r.config.OpenAPI.Enabled {
		opts = append(opts, bunrouter.WithMiddleware(r.middlewareValidation))
	}

	router := bunrouter.New(opts...).Compat()
	router.Router.GET("/health", r.handlerLiveness)
	router.Router.GET("/ready", r.handlerReadiness)

	return router, nil
}
