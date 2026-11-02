package rest

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/mountayaapp/helix.go/integration"

	"github.com/uptrace/bunrouter"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// attrPrefixRouteParam is the key prefix a matched path parameter is recorded
// under when Config.RecordRouteParams is set. The parameter names come from the
// application's routes, so this is a shape rather than a fixed key.
var attrPrefixRouteParam = string(integration.Key(identifier, "route.param."))

// Ensure routes complies to the Router type.
var _ Router = routes{}

// routes implements Router over one group of the underlying router.
type routes struct {
	r     *rest
	group *bunrouter.Group

	// prefix is the path the group registers its routes under, joined as the
	// underlying router joins it, so a route's full template is known at
	// registration.
	prefix string

	// defaults are the options of the Group calls this group descends from,
	// applied to each route before the route's own.
	defaults []RouteOption
}

// GET registers a handler for the GET method at path.
func (g routes) GET(path string, handler http.HandlerFunc, opts ...RouteOption) {
	g.Handle(http.MethodGet, path, handler, opts...)
}

// HEAD registers a handler for the HEAD method at path.
func (g routes) HEAD(path string, handler http.HandlerFunc, opts ...RouteOption) {
	g.Handle(http.MethodHead, path, handler, opts...)
}

// POST registers a handler for the POST method at path.
func (g routes) POST(path string, handler http.HandlerFunc, opts ...RouteOption) {
	g.Handle(http.MethodPost, path, handler, opts...)
}

// PUT registers a handler for the PUT method at path.
func (g routes) PUT(path string, handler http.HandlerFunc, opts ...RouteOption) {
	g.Handle(http.MethodPut, path, handler, opts...)
}

// PATCH registers a handler for the PATCH method at path.
func (g routes) PATCH(path string, handler http.HandlerFunc, opts ...RouteOption) {
	g.Handle(http.MethodPatch, path, handler, opts...)
}

// DELETE registers a handler for the DELETE method at path.
func (g routes) DELETE(path string, handler http.HandlerFunc, opts ...RouteOption) {
	g.Handle(http.MethodDelete, path, handler, opts...)
}

// OPTIONS registers a handler for the OPTIONS method at path.
func (g routes) OPTIONS(path string, handler http.HandlerFunc, opts ...RouteOption) {
	g.Handle(http.MethodOptions, path, handler, opts...)
}

// Handle registers a handler for method at path, under the policy resolved from
// the group's options followed by opts. A nil handler or a nil pre-validation
// middleware panics here, at registration, rather than on the first request routed
// to it.
//
// A GET also registers the same route for HEAD, unless Config.NoImplicitHead is
// set or the path already has a HEAD route of its own.
func (g routes) Handle(method string, path string, handler http.Handler, opts ...RouteOption) {
	if f, ok := handler.(http.HandlerFunc); handler == nil || (ok && f == nil) {
		panic("rest: nil handler for " + method + " " + path)
	}

	resolved := g.r.resolveRouteOptions(g.options(opts))
	for _, mw := range resolved.preValidation {
		if mw == nil {
			panic("rest: nil pre-validation middleware for " + method + " " + path)
		}
	}

	template := g.prefix + path
	if resolved.redactPath {
		g.r.redactedRoutes.Store(template, struct{}{})
	}

	if len(template) > 1 && strings.HasSuffix(template, "/") {
		g.r.slashRoutes.Store(true)
	}

	implied := g.r.methods.add(method, template, !g.r.config.NoImplicitHead)

	route := g.r.route(handler, resolved)
	g.group.Handle(method, path, route)
	if implied {
		g.group.Handle(http.MethodHead, path, route)
	}
}

// Group returns a Router registering under prefix, with opts applied to every
// route before the route's own.
func (g routes) Group(prefix string, opts ...RouteOption) Router {
	return routes{
		r:        g.r,
		group:    g.group.NewGroup(prefix),
		prefix:   joinPrefix(g.prefix, prefix),
		defaults: g.options(opts),
	}
}

// joinPrefix joins a group's prefix to a sub-group's the way the underlying router
// does: concatenated, without a trailing slash.
func joinPrefix(base, prefix string) string {
	joined := base + prefix
	if len(joined) > 1 {
		joined = strings.TrimSuffix(joined, "/")
	}

	return joined
}

// options returns the group's options followed by opts, in a new slice: a group's
// defaults are shared by every route and sub-group registered on it, and must not
// be appended to in place.
func (g routes) options(opts []RouteOption) []RouteOption {
	if len(g.defaults) == 0 {
		return opts
	}

	return append(slices.Clip(g.defaults), opts...)
}

// paramsKey is the context key the matched route's parameters are stored under,
// for ParamsFromContext to find.
type paramsKey struct{}

// route adapts an application handler to the underlying router, under the policy
// its route resolved to. Once the route has matched, a request runs through, from
// the outside in: its path parameters put in its context, its budget, the
// validation of its response, the middlewares passed WithPreValidation, the
// validation of its request, and the handler. See chain.
//
// It does not recover a panic, unlike the adapters the underlying router ships
// with: those turn a panic into an error the router then discards, which answers
// the client an empty 200. A panic here unwinds through Config.Middleware to
// recoverPanics, just inside Config.EdgeMiddleware, which answers it with the 500
// error envelope.
//
// The budget is applied here rather than in Config.Middleware because middleware
// wraps the whole router and therefore runs before any route has matched, where
// only the concrete request path is known — recognising a route there would mean
// maintaining a second copy of the routing table.
func (r *rest) route(handler http.Handler, opts routeOptions) bunrouter.HandlerFunc {
	timeout := opts.timeout
	next := r.chain(handler, opts)

	return func(rw http.ResponseWriter, req bunrouter.Request) error {
		ctx := context.WithValue(req.Context(), paramsKey{}, req.Params())

		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		next.ServeHTTP(rw, req.Request.WithContext(ctx))
		return nil
	}
}

// chain returns what a route runs once it has matched and its budget is set,
// built once, at registration: the validation of its response, then the route's
// preValidation in order, then the validation of its request, then handler.
//
// The validation is per route, and split in two around preValidation, so that a
// middleware there refuses a request before the description has judged it or
// anything has read its body, and so that what such a middleware answers is
// validated as a response of the route like the handler's. Without a description,
// preValidation simply runs ahead of handler.
func (r *rest) chain(handler http.Handler, opts routeOptions) http.Handler {
	next := handler
	if r.oapirouter != nil {
		next = r.middlewareRequestValidation(next, opts.undeclaredQuery)
	}

	preValidation := opts.preValidation
	for i := len(preValidation) - 1; i >= 0; i-- {
		next = preValidation[i](next)
	}

	if r.oapirouter != nil {
		next = r.middlewareResponseValidation(next)
	}

	return next
}

// middlewareRoute names the request's span after the route the router matched,
// and records the route's parameters on it when Config.RecordRouteParams is set.
// On a route registered WithRedactedPath it records the template as the request's
// path instead, and no parameter.
//
// It is the router's half of the telemetry: the span was opened around the whole
// handler chain, before routing, where only the concrete path was known. The
// route — "/things/:id" rather than "/things/42" — is what keeps the span name
// and the request measurements from growing a new value per identifier.
//
// The router wraps its not-found handler with this middleware too, so a 404 goes
// through it with an empty route, and keeps the span named after the method
// alone.
//
// A request the router is only asked about, by canonicalLocation, stops here: it
// reached this middleware, so the router matched it as written or answered it 404
// or 405 rather than redirecting it, which is all there was to find out.
func (r *rest) middlewareRoute(next bunrouter.HandlerFunc) bunrouter.HandlerFunc {
	return func(rw http.ResponseWriter, req bunrouter.Request) error {
		if probe, ok := rw.(*routeProbe); ok {
			probe.matched = true
			return nil
		}

		route := req.Route()
		r.http.SetRoute(req.Request, route)

		if _, redacted := r.redactedRoutes.Load(route); redacted {
			r.http.RedactPath(req.Request, route)
			return next(rw, req)
		}

		if r.config.RecordRouteParams {
			if span := oteltrace.SpanFromContext(req.Context()); span.IsRecording() {
				span.SetAttributes(routeParams(req.Params())...)
			}
		}

		return next(rw, req)
	}
}

// routeParams returns the matched path parameters as span attributes, keyed
// "rest.route.param.<name>".
func routeParams(params bunrouter.Params) []attribute.KeyValue {
	slice := params.Slice()
	if len(slice) == 0 {
		return nil
	}

	attrs := make([]attribute.KeyValue, 0, len(slice))
	for _, param := range slice {
		attrs = append(attrs, attribute.String(attrPrefixRouteParam+param.Key, param.Value))
	}

	return attrs
}
