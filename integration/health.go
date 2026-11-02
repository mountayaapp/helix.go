package integration

import (
	"github.com/mountayaapp/helix.go/errorstack"
)

// Unhealthy returns the error a Dependency or a Server reports from CheckHealth
// when its probe failed, and nil when err is nil.
//
// Every integration reported this the same way by hand, which made the sentence
// and the code six copies that had to stay in agreement. It is one function so
// that a health failure reads identically whichever integration raised it, and
// so that a third-party integration can report one the same way.
//
// The probe's own message is carried in the "reason" extension rather than in
// the entry's message, so the entry message stays the stable sentence a reader can
// recognise across integrations while the detail survives into logs and spans.
// It never reaches an HTTP body: a driver error routinely names hosts, users and
// databases, and the readiness probe answers whoever asks with a generic 503 —
// see HTTPProbes.HandleReadiness.
func Unhealthy(err error) error {
	if err == nil {
		return nil
	}

	return errorstack.Wrap(err, "Integration is not in a healthy state",
		errorstack.WithCode(errorstack.CodeServiceUnavailable),
		errorstack.WithExtension("reason", NormalizeErrorMessage(err)),
	)
}
