package rest

import (
	"net/http"
	"strconv"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
)

// requestValidationStatus is the status every request-validation refusal answers
// with.
//
// kin-openapi tells a missing parameter from a malformed body from a value out of
// range, and that distinction is carried in the ENTRIES rather than in the status.
// One status keeps the refusal inside what the description already declares: a 415
// or a 422 would be a status no operation in the description lists, so a client
// generated from that description would meet a response it has no branch for — and a
// validator that answers off-contract is a strange thing to put in front of a
// contract.
const requestValidationStatus = http.StatusBadRequest

// requestValidationFallback is the message used when a validation failure carries no
// readable reason of its own. It should not be reachable; it exists so a refusal is
// never an empty sentence.
const requestValidationFallback = "Request does not match the OpenAPI description"

// requestValidationEntries flattens a kin-openapi request-validation failure into one
// errorstack entry per non-conforming value.
//
// One entry per problem, not one per request. MultiError is set on the validation
// options precisely so a caller fixing a request body learns about every field at
// once; collapsing the result back into a single sentence throws that away and turns
// one round trip into five.
//
// Each entry carries the wire path of the value that failed, so a client can put the
// message next to the input that produced it:
//
//	[]any{"request", "body", "items", 0, "name"}
//	[]any{"request", "query", "page_size"}
//	[]any{"request", "params", "thing_id"}
//	[]any{"request", "headers", "X-User-Id"}
//
// The walk uses type switches rather than errors.As throughout. MultiError and
// RequestError both define unwrapping behaviour that descends into what they carry,
// so errors.As on a MultiError of five failures matches the FIRST one and silently
// discards the other four — exactly the collapse this function exists to avoid.
//
// An error shape not recognised here still yields an entry, with the message it
// carries and no path: a refusal the caller cannot read is worse than one it cannot
// locate.
func requestValidationEntries(err error) []errorstack.Entry {
	if err == nil {
		return nil
	}

	switch typed := err.(type) { //nolint:errorlint // errors.As collapses a MultiError; see above.
	case openapi3.MultiError:
		entries := make([]errorstack.Entry, 0, len(typed))
		for _, nested := range typed {
			entries = append(entries, requestValidationEntries(nested)...)
		}
		return entries

	case *openapi3filter.RequestError:
		return requestErrorEntries(typed)

	case *openapi3filter.SecurityRequirementsError:
		// Security is enforced by the service's own middleware and the OpenAPI layer
		// runs with a no-op authentication func, so reaching here means the filter
		// could not even attempt a scheme the description declares. It is reported
		// rather than passed over: a branch that is supposed to be unreachable and
		// stays quiet when it is reached is how a description and a service drift
		// apart without anyone noticing.
		return []errorstack.Entry{validationEntry(typed.Error(), nil)}

	default:
		return []errorstack.Entry{validationEntry(typed.Error(), nil)}
	}
}

// requestErrorEntries converts one RequestError into its entries, prefixing each with
// the wire location of the parameter or body it concerns.
//
// A RequestError's own Err is frequently a MultiError of schema errors — one per
// field of a body that did not conform — which is where the per-field paths come
// from. When it carries none, the RequestError's Reason is the whole story.
func requestErrorEntries(reqErr *openapi3filter.RequestError) []errorstack.Entry {
	prefix := requestErrorPrefix(reqErr)

	if reqErr.Err == nil {
		return []errorstack.Entry{validationEntry(reqErr.Reason, prefix)}
	}

	if multi, ok := reqErr.Err.(openapi3.MultiError); ok { //nolint:errorlint // As collapses a MultiError.
		entries := make([]errorstack.Entry, 0, len(multi))
		for _, nested := range multi {
			entries = append(entries, schemaErrorEntry(nested, prefix))
		}
		return entries
	}

	return []errorstack.Entry{schemaErrorEntry(reqErr.Err, prefix)}
}

// schemaErrorEntry converts one leaf validation failure into an entry, extending the
// prefix with the JSON pointer of the value inside the body or parameter when the
// failure is a schema error and therefore knows where it happened.
func schemaErrorEntry(err error, prefix []any) errorstack.Entry {
	schemaErr, ok := err.(*openapi3.SchemaError) //nolint:errorlint // A leaf of the walk, never wrapped.
	if !ok {
		return validationEntry(err.Error(), prefix)
	}

	// Error rather than Reason: the middleware installs a custom schema-error func
	// that already reduces Error to the reason alone, and Error still says something
	// when Reason is empty.
	return validationEntry(schemaErr.Error(), joinPath(prefix, pointerSegments(schemaErr.JSONPointer())))
}

// requestErrorPrefix returns the wire path of whatever the RequestError concerns.
//
// The four parameter locations map to the segment a client reads them back under.
// A RequestError naming no parameter is about the body, which is the only other
// thing ValidateRequest looks at.
func requestErrorPrefix(reqErr *openapi3filter.RequestError) []any {
	if reqErr.Parameter == nil {
		return []any{"request", "body"}
	}

	switch reqErr.Parameter.In {
	case openapi3.ParameterInPath:
		return []any{"request", "params", reqErr.Parameter.Name}
	case openapi3.ParameterInQuery:
		return []any{"request", "query", reqErr.Parameter.Name}
	case openapi3.ParameterInHeader:
		return []any{"request", "headers", reqErr.Parameter.Name}
	case openapi3.ParameterInCookie:
		return []any{"request", "cookies", reqErr.Parameter.Name}
	default:
		return []any{"request"}
	}
}

// pointerSegments converts a JSON pointer into path segments, turning a token that
// reads as an integer into an int so a list element surfaces as ["items", 0, "name"]
// rather than ["items", "0", "name"].
//
// A property whose NAME is a number would be converted too. That is accepted: the
// pointer carries no type information to tell the two apart, and a numeric property
// name is not a shape this description produces.
func pointerSegments(pointer []string) []any {
	segments := make([]any, 0, len(pointer))
	for _, token := range pointer {
		if index, err := strconv.Atoi(token); err == nil {
			segments = append(segments, index)
			continue
		}

		segments = append(segments, token)
	}

	return segments
}

// joinPath concatenates two path halves into a freshly allocated slice.
//
// The allocation is the point. A prefix is shared across every entry of one
// RequestError, and appending to it in a loop would let each entry overwrite the
// segments of the one before it whenever the prefix has spare capacity — the kind of
// aliasing that produces a correct first entry and four wrong ones.
func joinPath(prefix []any, suffix []any) []any {
	joined := make([]any, 0, len(prefix)+len(suffix))
	joined = append(joined, prefix...)
	joined = append(joined, suffix...)

	return joined
}

// validationEntry builds one entry, falling back to a fixed sentence when the failure
// carries no message. The code is left unset: SetValidations forces every entry to
// VALIDATION_FAILED, and naming it here twice would let the two spellings drift.
func validationEntry(message string, path []any) errorstack.Entry {
	if message == "" {
		message = requestValidationFallback
	}

	return errorstack.Entry{
		Message: message,
		Path:    path,
	}
}
