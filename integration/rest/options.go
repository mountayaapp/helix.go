package rest

import (
	"time"
)

/*
RouteOption configures a single route registered on the REST API. Every verb
accepts them variadically, so a route can carry more policy over time without
another change to the REST interface — and so routes that need none read exactly
as they did before any existed.
*/
type RouteOption func(*routeOptions)

/*
routeOptions holds the policy resolved for one route. It is seeded from Config
before the route's own options are applied, so an option always describes a
deviation from the API-wide default rather than restating it.
*/
type routeOptions struct {

	// timeout is the request budget for the route. Zero, like any non-positive
	// value, means the route runs without a deadline.
	timeout time.Duration
}

/*
WithTimeout bounds how long a single route may spend handling a request,
overriding Config.RequestTimeout. The budget rides the request context, so every
call derived from it — a database query, an outbound HTTP request — is cancelled
once it is spent, and the handler's own error path is what reports the failure.

A non-positive duration waives the budget, which WithoutTimeout says more
plainly.
*/
func WithTimeout(d time.Duration) RouteOption {
	return func(o *routeOptions) {
		o.timeout = d
	}
}

/*
WithoutTimeout waives Config.RequestTimeout for a route whose response is
long-lived by design — server-sent events, long polling — where a deadline would
sever a healthy stream rather than catch a stuck handler.
*/
func WithoutTimeout() RouteOption {
	return func(o *routeOptions) {
		o.timeout = 0
	}
}

/*
resolveRouteOptions applies opts in order over the defaults Config carries, and
returns the request budget the route runs under. A non-positive result means the
route is left unbounded.
*/
func (r *rest) resolveRouteOptions(opts []RouteOption) time.Duration {
	resolved := routeOptions{
		timeout: r.config.RequestTimeout,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(&resolved)
		}
	}

	return resolved.timeout
}
