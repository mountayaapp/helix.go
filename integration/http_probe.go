package integration

import (
	"context"
	"encoding/json/v2"
	"net/http"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/internal/locales"
	"github.com/mountayaapp/helix.go/telemetry/log"

	oteltrace "go.opentelemetry.io/otel/trace"
)

// Paths of the two probes every helix server integration exposes. They are
// constants rather than literals because three servers answering "/health" and a
// fourth answering "/healthz" is the kind of drift nothing would catch.
const (
	PathLiveness  = "/health"
	PathReadiness = "/ready"
)

// probeSuccessBody is the constant 2xx envelope the probes return. It mirrors the
// REST integration's success shape ({"data":null}) so a service running several
// transports surfaces identical health-probe bodies. The HTTP status code carries
// the signal; the body stays deliberately empty.
var probeSuccessBody = []byte(`{"data":null}`)

// fallbackErrorBody is what an error response degrades to when the envelope
// itself cannot be marshaled. It is a valid envelope, so a client parsing the
// error path never has to handle a second shape.
var fallbackErrorBody = []byte(`{"errors":[{"message":"Internal server error","extensions":{"code":"INTERNAL_ERROR"}}]}`)

// WriteSuccess writes the canonical 2xx envelope to rw. The body is always
// {"data":null} and the status code carries the actual signal.
func WriteSuccess(rw http.ResponseWriter, status int) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	rw.Write(probeSuccessBody)
}

// WriteError writes the helix error envelope to rw for an HTTP-layer error — a
// 404, a 405, an unready service — that arose before any application code ran:
//
//	{"errors":[{"message":"…","extensions":{"code":"…"}}]}
//
// The message is localized from the request's Accept-Language, and the code is
// the one the status maps to. Nothing else is written: these responses go to
// whoever asked, and say nothing about the service behind them.
func WriteError(rw http.ResponseWriter, req *http.Request, status int) {
	body := errorstack.New(
		locales.Message(req, status),
		errorstack.WithCode(errorstack.HTTPStatusToCode(status)),
	)

	b, err := json.Marshal(body)
	if err != nil {
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusInternalServerError)
		rw.Write(fallbackErrorBody)

		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	rw.Write(b)
}

// HTTPProbes answers the liveness and readiness endpoints of a server
// integration.
//
// It exists once rather than once per server: the HTTP servers in this repository
// answered the same two paths, with the same two bodies and the same status
// mapping, in copies that had to be kept in agreement by hand.
//
// This is also the seam where health becomes a transport concern. A Dependency
// reports health as an error because a connection pool has no opinion about HTTP;
// the server exposing the probe is the only component that does.
type HTTPProbes struct {

	// CheckHealth is what the readiness probe reports on. Server integrations pass
	// the Service's own, so that the probe covers every integration attached to
	// it. A nil CheckHealth reports ready.
	CheckHealth func(ctx context.Context) error

	// Readiness replaces the probe entirely, returning the status code to answer
	// with. Set it for a service whose readiness means something other than "every
	// attached integration is reachable". A status outside 100–999 is answered as
	// 500, rather than handed to net/http, which would panic on it.
	Readiness func(req *http.Request) int
}

// HandleLiveness answers the liveness probe. It returns 200 as soon as the
// process is serving, and checks nothing: a liveness probe that fails because a
// database is unreachable gets the container restarted, which does not fix the
// database.
func (p HTTPProbes) HandleLiveness(rw http.ResponseWriter, _ *http.Request) {
	WriteSuccess(rw, http.StatusOK)
}

// HandleReadiness answers the readiness probe.
//
// A failure is answered with the same generic 503 envelope whatever caused it.
// The probe is served on the same listener as the application, to callers helix
// knows nothing about, and the reason a dependency is unhealthy — a driver error —
// routinely names hosts, users and databases. What failed, and why, goes to the
// log and to the request's span instead, which is where an operator looks.
func (p HTTPProbes) HandleReadiness(rw http.ResponseWriter, req *http.Request) {
	if p.Readiness != nil {
		status := p.Readiness(req)
		switch {
		case status < 100 || status > 999:
			WriteError(rw, req, http.StatusInternalServerError)
		case status >= http.StatusMultipleChoices:
			WriteError(rw, req, status)
		default:
			WriteSuccess(rw, status)
		}

		return
	}

	if p.CheckHealth == nil {
		WriteSuccess(rw, http.StatusOK)
		return
	}

	if err := p.CheckHealth(req.Context()); err != nil {
		oteltrace.SpanFromContext(req.Context()).RecordError(err)
		log.Warn(req.Context(), "Service is not ready", log.Err(err))

		WriteError(rw, req, http.StatusServiceUnavailable)
		return
	}

	WriteSuccess(rw, http.StatusOK)
}
