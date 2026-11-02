package rest

import (
	"net/url"
	"slices"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
)

// undeclaredQueryMessage is the message of the entry refusing a query parameter
// the operation does not declare (ConfigOpenAPI.RefuseUndeclaredQuery).
const undeclaredQueryMessage = "Is not a query parameter of this operation"

// undeclaredQueryEntries returns one entry per query parameter query carries that
// route does not declare and admitted does not name, at ["request", "query",
// name], in the order of the names, so a refusal reads the same however the
// client ordered its query string. A parameter sent more than once is one entry.
func undeclaredQueryEntries(route *routers.Route, query url.Values, admitted []string) []errorstack.Entry {
	if len(query) == 0 {
		return nil
	}

	declared := declaredQuery(route)

	names := make([]string, 0, len(query))
	for name := range query {
		if _, ok := declared[name]; ok || slices.Contains(admitted, name) {
			continue
		}

		names = append(names, name)
	}

	slices.Sort(names)

	entries := make([]errorstack.Entry, 0, len(names))
	for _, name := range names {
		entries = append(entries, validationEntry(undeclaredQueryMessage, []any{"request", "query", name}))
	}

	return entries
}

// declaredQuery returns the names of the query parameters route declares: the
// ones its operation and its path item declare, and the name of every apiKey
// security scheme carried in the query that one of the operation's security
// requirements names, the document's when the operation declares none of its
// own. A credential the description says travels in the query is a parameter the
// request may carry, even though the validator itself leaves authentication to
// the application.
func declaredQuery(route *routers.Route) map[string]struct{} {
	declared := map[string]struct{}{}

	add := func(parameters openapi3.Parameters) {
		for _, parameter := range parameters {
			if parameter != nil && parameter.Value != nil && parameter.Value.In == openapi3.ParameterInQuery {
				declared[parameter.Value.Name] = struct{}{}
			}
		}
	}

	if route.PathItem != nil {
		add(route.PathItem.Parameters)
	}

	if route.Operation != nil {
		add(route.Operation.Parameters)
	}

	if route.Spec == nil || route.Spec.Components == nil {
		return declared
	}

	requirements := route.Spec.Security
	if route.Operation != nil && route.Operation.Security != nil {
		requirements = *route.Operation.Security
	}

	for _, requirement := range requirements {
		for name := range requirement {
			scheme := route.Spec.Components.SecuritySchemes[name]
			if scheme != nil && scheme.Value != nil && scheme.Value.Type == "apiKey" && scheme.Value.In == openapi3.ParameterInQuery {
				declared[scheme.Value.Name] = struct{}{}
			}
		}
	}

	return declared
}
