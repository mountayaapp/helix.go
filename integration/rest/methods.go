package rest

import (
	"net/http"
	"strings"
	"sync"
)

// allowOrder is the order the Allow header of a 405 lists methods in: the order
// the Router interface declares them.
var allowOrder = []string{
	http.MethodGet,
	http.MethodHead,
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
	http.MethodOptions,
}

// routeMethods records the methods registered on each route: what the Allow
// header of a 405 lists, and whether a route's HEAD was implied by its GET.
//
// Routes are keyed by their shape rather than their template. The router holds
// "/things/:id" and "/things/:name" as one route, keeps the template it was given
// first, and answers every request matching either under that one, so a 405 on
// it must list the methods of both.
type routeMethods struct {
	mu sync.RWMutex

	// shapes maps a route's shape to the methods registered on it, each to whether
	// it was implied rather than registered: only a HEAD ever is.
	shapes map[string]map[string]bool
}

// add records method as registered on the route at template, and reports whether
// a HEAD must be registered along with it: when method is GET, implyHead is set,
// and no HEAD was registered on the route first.
//
// It panics on a HEAD registered after the GET of the same route implied one: the
// router would refuse it as a duplicate, without saying why there is one.
func (m *routeMethods) add(method, template string, implyHead bool) bool {
	shape := routeShape(template)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.shapes == nil {
		m.shapes = make(map[string]map[string]bool)
	}

	methods, ok := m.shapes[shape]
	if !ok {
		methods = make(map[string]bool)
		m.shapes[shape] = methods
	}

	if implied := methods[http.MethodHead]; method == http.MethodHead && implied {
		panic("rest: HEAD " + template + " is already answered by its GET route; register the HEAD route before the GET one, or set Config.NoImplicitHead")
	}

	methods[method] = false
	if method != http.MethodGet || !implyHead {
		return false
	}

	if _, ok := methods[http.MethodHead]; ok {
		return false
	}

	methods[http.MethodHead] = true
	return true
}

// allow returns the Allow header of a 405 on the route at template: the methods
// registered on it, comma-separated. It is empty for a template no route was
// registered under.
func (m *routeMethods) allow(template string) string {
	shape := routeShape(template)

	m.mu.RLock()
	defer m.mu.RUnlock()

	methods := m.shapes[shape]
	if len(methods) == 0 {
		return ""
	}

	allowed := make([]string, 0, len(methods))
	for _, method := range allowOrder {
		if _, ok := methods[method]; ok {
			allowed = append(allowed, method)
		}
	}

	return strings.Join(allowed, ", ")
}

// routeShape returns template with the names of its parameters removed, which is
// what the router tells one route from another by: "/things/:" for
// "/things/:id", and "/files/" for "/files/*path", whose wildcard belongs to the
// route of the path it follows.
func routeShape(template string) string {
	if !strings.ContainsAny(template, ":*") {
		return template
	}

	segments := strings.Split(template, "/")
	for i, segment := range segments {
		switch {
		case strings.HasPrefix(segment, ":"):
			segments[i] = ":"
		case strings.HasPrefix(segment, "*"):
			segments[i] = ""
		}
	}

	return strings.Join(segments, "/")
}
