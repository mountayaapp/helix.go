package rest

import (
	"net/http"
	"time"
)

// RouteOption configures a single route registered on the REST API. Every Router
// method accepts them variadically, so a route can carry more policy over time
// without another change to the Router interface — and so routes that need none
// read exactly as they did before any existed. Options passed to Group apply to
// every route of the group, before the route's own.
type RouteOption func(*routeOptions)

// routeOptions holds the policy resolved for one route. It is seeded from Config
// before the route's own options are applied, so an option always describes a
// deviation from the API-wide default rather than restating it.
type routeOptions struct {

	// timeout is the request budget for the route. Zero, like any non-positive
	// value, means the route runs without a deadline.
	timeout time.Duration

	// redactPath keeps the route's concrete paths out of its telemetry. See
	// WithRedactedPath.
	redactPath bool

	// preValidation are the middlewares the route runs ahead of the validation of
	// its request, outermost first. See WithPreValidation.
	preValidation []func(http.Handler) http.Handler

	// undeclaredQuery are the query parameters the route admits although its
	// description does not declare them. See WithUndeclaredQuery.
	undeclaredQuery []string
}

// WithTimeout bounds how long a single route may spend handling a request,
// overriding Config.RequestTimeout. The budget rides the request context, so every
// call derived from it — a database query, an outbound HTTP request — is cancelled
// once it is spent, and the handler's own error path is what reports the failure.
//
// A non-positive duration waives the budget, which WithoutTimeout says more
// plainly.
func WithTimeout(d time.Duration) RouteOption {
	return func(o *routeOptions) {
		o.timeout = d
	}
}

// WithoutTimeout waives Config.RequestTimeout for a route whose response is
// long-lived by design — server-sent events, long polling — where a deadline would
// sever a healthy stream rather than catch a stuck handler.
func WithoutTimeout() RouteOption {
	return func(o *routeOptions) {
		o.timeout = 0
	}
}

// WithRedactedPath keeps the concrete path of a request out of its telemetry: the
// span records the route template ("/things/:id") as the request's path, and none
// of the path's parameters, even with Config.RecordRouteParams set.
//
// It is for a route whose path carries a value a trace backend must not keep, a
// credential or a one-time token. It holds for every request the router matches to
// the route's path, whatever its method, so a request for a method the path does
// not serve, answered 405, is recorded the same way. It is applied as soon as the
// route has matched, before any validation or middleware of the route runs, so a
// request refused there is recorded the same way too.
func WithRedactedPath() RouteOption {
	return func(o *routeOptions) {
		o.redactPath = true
	}
}

// WithPreValidation runs mw once the route has matched and before its request is
// validated against the OpenAPI description, in the order given: the first sees
// the request first. Called more than once, on a Group and on its routes, the
// middlewares accumulate, the group's ahead of the route's.
//
// It is where a check that does not depend on the request's shape belongs,
// authentication above all. A middleware that refuses the request answers before
// anything has read its body or judged its parameters, so a caller who may not
// call the route learns nothing about what the route accepts, and no body is read
// on behalf of a caller that is refused anyway:
//
//	api.POST("/things", createThing, rest.WithPreValidation(authenticate))
//
// The middlewares run under the route's budget, and ParamsFromContext answers in
// them. A response one of them writes is validated against the description like
// the handler's own. Without OpenAPI validation, they simply run ahead of the
// handler.
func WithPreValidation(mw ...func(http.Handler) http.Handler) RouteOption {
	return func(o *routeOptions) {
		o.preValidation = append(o.preValidation, mw...)
	}
}

// WithUndeclaredQuery admits, on a route, query parameters its description does
// not declare, when ConfigOpenAPI.RefuseUndeclaredQuery refuses the ones it does
// not. Called more than once, on a Group and on its routes, the names accumulate.
//
// It is for a parameter the route reads before its request is validated, in a
// WithPreValidation middleware, and that the description has no place for: a
// credential a browser can only send in the URL of a link it follows, say. A
// parameter the handler reads belongs in the description instead, where it is
// validated like the rest:
//
//	api.GET("/files/:id", download, rest.WithPreValidation(authenticate), rest.WithUndeclaredQuery("token"))
func WithUndeclaredQuery(names ...string) RouteOption {
	return func(o *routeOptions) {
		o.undeclaredQuery = append(o.undeclaredQuery, names...)
	}
}

// resolveRouteOptions applies opts in order over the defaults Config carries, and
// returns the policy the route runs under. A non-positive timeout means the route
// is left unbounded. A nil option is skipped.
func (r *rest) resolveRouteOptions(opts []RouteOption) routeOptions {
	resolved := routeOptions{
		timeout: r.config.RequestTimeout,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(&resolved)
		}
	}

	return resolved
}
