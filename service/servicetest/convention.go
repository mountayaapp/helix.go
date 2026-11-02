package servicetest

import (
	"slices"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// sharedKeys are the attribute keys any span may carry whatever integration
// emitted it. They are the Event stamped on the entry span of a trace, whose
// three semantic-convention spellings are deliberate: identity is the one place
// where a backend recognising the key beats keeping the namespace pure.
//
// See event.Event.AppendAttrs.
var sharedKeys = map[string]bool{
	"enduser.id":          true,
	"user_agent.original": true,
	"client.address":      true,
}

// sharedPrefix is the namespace of every other Event attribute.
const sharedPrefix = "event."

// foreignNamespaces are vocabularies that belong to somebody else's
// instrumentation. A helix span carrying one means a third-party package wrote to
// it, or that an integration reached for a semantic convention instead of its own
// namespace — the drift this whole convention exists to prevent.
//
// The list is deliberately not exhaustive. It names the vocabularies the
// integrations in this repository could plausibly drift into; the
// single-namespace rule below is what catches everything else.
var foreignNamespaces = map[string]bool{
	"http":       true,
	"url":        true,
	"net":        true,
	"network":    true,
	"server":     true,
	"client":     true,
	"peer":       true,
	"db":         true,
	"rpc":        true,
	"messaging":  true,
	"code":       true,
	"exception":  true,
	"user_agent": true,
	"enduser":    true,
	"otel":       true,
	"telemetry":  true,
}

// AssertConvention fails the test for any span that does not follow the house
// convention:
//
//   - the name is "<Humanized>: <Operation>";
//   - every attribute key is "<identifier>.<path>" or on the shared allow-list;
//   - all of a span's own keys share one identifier, so a span speaks with one
//     vocabulary rather than two;
//   - no key belongs to a foreign instrumentation vocabulary.
//
// It is the consistency gate. A golden test pins what one integration emits;
// this pins that all of them emit it the same way, which is the promise the
// library actually makes.
//
// It deliberately does not check the instrumentation scope. A third-party
// integration — which the README supports — emits under its own module path and
// is no less conforming for it.
func AssertConvention(tb testing.TB, spans []sdktrace.ReadOnlySpan) {
	tb.Helper()

	for _, span := range spans {
		name := span.Name()

		sep := strings.Index(name, ": ")
		if sep <= 0 || sep+2 >= len(name) {
			tb.Errorf("servicetest: span %q does not follow the \"<Humanized>: <Operation>\" convention", name)
			continue
		}

		var namespace string
		for _, attr := range span.Attributes() {
			key := string(attr.Key)
			if sharedKeys[key] || strings.HasPrefix(key, sharedPrefix) {
				continue
			}

			dot := strings.Index(key, ".")
			if dot <= 0 || dot+1 >= len(key) {
				tb.Errorf("servicetest: span %q carries attribute %q, which is not namespaced as \"<identifier>.<path>\"", name, key)
				continue
			}

			switch ns := key[:dot]; {
			case foreignNamespaces[ns]:
				tb.Errorf("servicetest: span %q carries attribute %q, from the %q instrumentation vocabulary rather than an integration's own", name, key, ns)
			case namespace == "":
				namespace = ns
			case ns != namespace:
				tb.Errorf("servicetest: span %q mixes the %q and %q namespaces; a span speaks for one integration", name, namespace, ns)
			}
		}
	}
}

// Scopes returns the distinct instrumentation scopes the spans were emitted
// under, sorted. A golden test uses it to assert that a scope helix suppresses —
// gocloud.dev/blob — never reaches the exporter.
func Scopes(spans []sdktrace.ReadOnlySpan) []string {
	seen := make(map[string]bool, len(spans))
	out := make([]string, 0, len(spans))
	for _, span := range spans {
		if scope := span.InstrumentationScope().Name; !seen[scope] {
			seen[scope] = true
			out = append(out, scope)
		}
	}

	slices.Sort(out)
	return out
}
