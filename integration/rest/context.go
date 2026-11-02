package rest

import (
	"context"

	"github.com/uptrace/bunrouter"
)

// ParamsFromContext returns the path parameters of the route that matched the
// request ctx belongs to, keyed by the names the route declared: for a route
// "/users/:id" and a request for "/users/42", {"id": "42"}.
//
// The map is built on each call and belongs to the caller. The bool reports
// whether it holds at least one parameter: it is false both for a route that
// declares none and for a context that did not come from a routed request. Use
// ParamFromContext to read a single parameter without building the map.
func ParamsFromContext(ctx context.Context) (map[string]string, bool) {
	params := paramsFrom(ctx).Map()

	return params, len(params) > 0
}

// ParamFromContext returns the value of the path parameter name of the route that
// matched the request ctx belongs to. The bool reports whether the route declares
// that parameter; it is false for a context that did not come from a routed
// request.
func ParamFromContext(ctx context.Context, name string) (string, bool) {
	return paramsFrom(ctx).Get(name)
}

// paramsFrom returns the parameters the router stored in ctx. A context this
// integration's router did not populate falls back to the parameters the
// underlying router stores itself, which is what a handler routed by a bare
// bunrouter (in a test, say) carries; failing both, it returns the zero Params,
// which holds none.
func paramsFrom(ctx context.Context) bunrouter.Params {
	if params, ok := ctx.Value(paramsKey{}).(bunrouter.Params); ok {
		return params
	}

	return bunrouter.ParamsFromContext(ctx)
}
