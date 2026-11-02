// Package httpclient exposes an opinionated HTTP client to communicate with
// external HTTP APIs.
//
// A single client can target one or more interchangeable endpoints: requests are
// round-robined across them, and failed over to the next one when that is safe —
// see FailoverPolicy. A path cannot leave an endpoint's host, a redirect leaves it
// only when allowed to and without the client's headers, and no query string
// reaches a span or an error. It brings automatic distributed tracing and
// registers itself as a dependency of the Service via service.Attach, reporting
// the health of every endpoint through the Service's readiness when probing is
// configured.
package httpclient

// identifier represents the integration's unique identifier.
const identifier = "httpclient"

// humanized represents the integration's humanized name.
const humanized = "HTTP Client"
