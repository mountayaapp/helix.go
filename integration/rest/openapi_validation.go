package rest

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
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

// undecodableBodyMessage is the message of the entry refusing a body the
// validator could not read or decode: JSON that does not parse, a form with a
// malformed part, an escape that does not decode. It is the whole of what the
// client is told. The parser's own error stays on the request's trace: it quotes
// the bytes it stopped on, sometimes several times over, and they are the
// request's.
const undecodableBodyMessage = "Cannot be decoded as its content type"

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
//
// A value that could not be parsed, before any schema was asked, is reported with
// what the request supplied cut (parseFailureMessage). A body that could not be
// read or decoded at all is one entry at the body, with a fixed message
// (undecodableBodyMessage) and nothing of the error behind it.
func requestErrorEntries(reqErr *openapi3filter.RequestError) []errorstack.Entry {
	prefix := requestErrorPrefix(reqErr)

	// The one reason that stands alone quotes the Content-Type the request sent.
	if reqErr.Err == nil {
		return []errorstack.Entry{validationEntry(boundQuoted(reqErr.Reason, unexpectedContentType, ""), prefix)}
	}

	if multi, ok := reqErr.Err.(openapi3.MultiError); ok { //nolint:errorlint // As collapses a MultiError.
		entries := make([]errorstack.Entry, 0, len(multi))
		for _, nested := range multi {
			entries = append(entries, schemaErrorEntries(nested, prefix)...)
		}
		return entries
	}

	if parseErr, ok := reqErr.Err.(*openapi3filter.ParseError); ok { //nolint:errorlint // Handed over as it is, never wrapped.
		message, ok := parseFailureMessage(parseErr)
		if !ok && reqErr.Parameter == nil {
			message = undecodableBodyMessage
		}

		return []errorstack.Entry{validationEntry(message, prefix)}
	}

	if undecodableBody(reqErr) {
		return []errorstack.Entry{validationEntry(undecodableBodyMessage, prefix)}
	}

	return schemaErrorEntries(reqErr.Err, prefix)
}

// undecodableBody reports whether reqErr is about a body the validator could not
// read or decode, rather than one it decoded and held to its schema: a body's
// failure that is neither a schema's nor the absence of a required body. What is
// left is the error of whatever read the body, whose text is not passed on.
func undecodableBody(reqErr *openapi3filter.RequestError) bool {
	if reqErr.Parameter != nil || errors.Is(reqErr.Err, openapi3filter.ErrInvalidRequired) {
		return false
	}

	var schemaErr *openapi3.SchemaError
	return !errors.As(reqErr.Err, &schemaErr)
}

// schemaErrorEntries converts one validation failure of a parameter or a body into
// its entries.
//
// An OpenAPI 3.1 description is validated as JSON Schema 2020-12, by
// santhosh-tekuri/jsonschema, and kin-openapi hands that validator's failure back as
// ONE SchemaError: its reason is the validator's whole report, a header naming the
// schema by the placeholder URL kin-openapi compiles it under, then every problem
// under it, indented. Read as it is, that is one multi-line entry per parameter or
// body, and a URL the description never mentions. So on that path the failure is
// walked down to the problems it is made of (jsonSchemaFindings), and each becomes
// an entry of its own: the value's location in the parameter or body, and a message
// naming the problem and nothing else. The header is not used.
//
// The entries are sorted by location, then by message (compareFindings), since the
// validator reports the properties of an object in no fixed order, and a request is
// answered the same way every time. Two problems reported alike at one location are
// one entry: the branches of an allOf, or a schema reached twice, can each report
// the same thing.
//
// An OpenAPI 3.0 description is validated by kin-openapi itself, whose SchemaErrors
// are already one per problem; they are converted as they always were
// (schemaErrorEntry).
func schemaErrorEntries(err error, prefix []any) []errorstack.Entry {
	var report *jsonschema.ValidationError
	if !errors.As(err, &report) {
		return []errorstack.Entry{schemaErrorEntry(err, prefix)}
	}

	findings := jsonSchemaFindings(err)
	slices.SortStableFunc(findings, compareFindings)
	findings = slices.CompactFunc(findings, func(a, b jsonSchemaFinding) bool {
		return compareFindings(a, b) == 0
	})

	entries := make([]errorstack.Entry, 0, len(findings))
	for _, finding := range findings {
		entries = append(entries, validationEntry(finding.message, joinPath(prefix, pointerSegments(finding.location))))
	}

	return entries
}

// jsonSchemaFinding is one problem of a JSON Schema 2020-12 failure: the location of
// the value within the parameter or body, and what is wrong with it.
type jsonSchemaFinding struct {
	location []string
	message  string
}

// jsonSchemaFindings returns the problems a kin-openapi failure on the JSON Schema
// 2020-12 path carries.
//
// kin-openapi mirrors the validator's tree: a SchemaError per validator error, whose
// Origin wraps a MultiError of the SchemaErrors of its causes, and whose own cause,
// reached by unwrapping a SchemaError with no Origin, is the validator error it was
// made from. So a problem is a SchemaError with no Origin, told by its validator
// error (jsonSchemaMessage); one that unwraps to none is reported with the fixed
// fallback sentence, at its location.
//
// A oneOf or an anyOf that matched no branch is one problem, at the value it
// concerns, rather than every problem of every branch: those contradict each other
// (one branch wants a value to be "Point", the next "LineString") and repeat each
// other, and the value had only to match one of them (alternativesFailed).
//
// The walk uses type switches for the reason requestValidationEntries gives: an
// errors.As on the MultiError would stop at its first error.
func jsonSchemaFindings(err error) []jsonSchemaFinding {
	switch typed := err.(type) { //nolint:errorlint // errors.As collapses a MultiError; see above.
	case openapi3.MultiError:
		var findings []jsonSchemaFinding
		for _, nested := range typed {
			findings = append(findings, jsonSchemaFindings(nested)...)
		}
		return findings

	case *openapi3.SchemaError:
		if typed.Origin == nil {
			var leaf *jsonschema.ValidationError
			if !errors.As(typed, &leaf) {
				return []jsonSchemaFinding{{location: typed.JSONPointer(), message: requestValidationFallback}}
			}
			return []jsonSchemaFinding{{location: leaf.InstanceLocation, message: jsonSchemaMessage(leaf)}}
		}

		if message, failed := alternativesFailed(typed); failed {
			return []jsonSchemaFinding{{location: typed.JSONPointer(), message: message}}
		}
		return jsonSchemaFindings(typed.Origin)

	default:
		// kin-openapi wraps the MultiError of a SchemaError's causes in a sentence of
		// its own ("validation failed due to: ..."), which is unwrapped to reach it.
		if inner := errors.Unwrap(err); inner != nil {
			return jsonSchemaFindings(inner)
		}
		return []jsonSchemaFinding{{message: requestValidationFallback}}
	}
}

// alternativesFailed reports whether schemaErr is a oneOf or an anyOf that matched
// none of its branches, with the validator's own message for it ("'oneOf' failed,
// none matched", "'anyOf' failed").
//
// kin-openapi names a SchemaError by the keyword its failure is about, and a
// SchemaError that only passes one failure on (a reference, the schema itself) takes
// the keyword of the failure it passes on, so a oneOf can be named twice in a row,
// once by its wrapper and once by itself. Only the innermost is the alternatives'
// own: a SchemaError whose single cause carries the same keyword passes it on. A
// oneOf that matched more than one branch has no causes, and is reported by its own
// message as any other problem.
func alternativesFailed(schemaErr *openapi3.SchemaError) (string, bool) {
	var failed jsonschema.ErrorKind
	switch schemaErr.SchemaField {
	case "oneOf":
		failed = &kind.OneOf{}
	case "anyOf":
		failed = &kind.AnyOf{}
	default:
		return "", false
	}

	var causes openapi3.MultiError
	if errors.As(schemaErr.Origin, &causes) && len(causes) == 1 {
		if cause, ok := causes[0].(*openapi3.SchemaError); ok && cause.SchemaField == schemaErr.SchemaField { //nolint:errorlint // A node of the walk, never wrapped.
			return "", false
		}
	}

	return failed.LocalizedString(jsonSchemaPrinter), true
}

// compareFindings orders two findings by location (compareLocations), then by
// message.
func compareFindings(a, b jsonSchemaFinding) int {
	if order := compareLocations(a.location, b.location); order != 0 {
		return order
	}

	return cmp.Compare(a.message, b.message)
}

// compareLocations orders two locations within a value as a reader of the entries
// would: segment by segment, an index before a greater one and a name before a later
// one, and a location before the locations inside it.
func compareLocations(a, b []string) int {
	for i := range min(len(a), len(b)) {
		if a[i] == b[i] {
			continue
		}

		left, leftErr := strconv.Atoi(a[i])
		right, rightErr := strconv.Atoi(b[i])
		if leftErr == nil && rightErr == nil {
			return cmp.Compare(left, right)
		}

		return cmp.Compare(a[i], b[i])
	}

	return cmp.Compare(len(a), len(b))
}

// jsonSchemaPrinter is the printer the validator's messages are rendered with, in
// English, as kin-openapi renders them.
var jsonSchemaPrinter = message.NewPrinter(language.English)

// jsonSchemaMessage is a validator leaf error's own message: what is wrong with the
// value at its location ("minLength: got 2, want 3", "missing property 'name'"),
// without the report's header or any location.
//
// The validator lists the names of an object's properties in the order it met them,
// which for the properties of the value itself is no fixed order, so the names a
// message lists are sorted first, and a request is answered the same way every time.
// The two kinds whose message names a schema by its URL, the schema itself and a
// reference cycle, are never a leaf of a value's failure; were one to be, the fixed
// fallback sentence is used instead.
//
// Four kinds quote the request rather than the description: the names of the
// properties an object may not have, the name of a property that is not a valid
// one, the string a pattern refuses and the value a format refuses. Each is cut
// first (boundName), and no more than maxRefusalNames names are listed
// (boundNames), the message saying how many more there were, so it does not grow
// with what the client sent.
func jsonSchemaMessage(leaf *jsonschema.ValidationError) string {
	switch failed := leaf.ErrorKind.(type) {
	case *kind.Schema, *kind.RefCycle:
		return requestValidationFallback
	case *kind.AdditionalProperties:
		names, more := boundNames(failed.Properties)
		listed := (&kind.AdditionalProperties{Properties: names}).LocalizedString(jsonSchemaPrinter)
		if more == 0 {
			return listed
		}

		if head, found := strings.CutSuffix(listed, " not allowed"); found {
			return fmt.Sprintf("%s and %d more not allowed", head, more)
		}

		return listed
	case *kind.PropertyNames:
		return (&kind.PropertyNames{Property: boundName(failed.Property)}).LocalizedString(jsonSchemaPrinter)
	case *kind.Pattern:
		return (&kind.Pattern{Got: boundName(failed.Got), Want: failed.Want}).LocalizedString(jsonSchemaPrinter)
	case *kind.Format:
		got := failed.Got
		if text, ok := got.(string); ok {
			got = boundName(text)
		}

		return (&kind.Format{Got: got, Want: failed.Want, Err: failed.Err}).LocalizedString(jsonSchemaPrinter)
	case *kind.Required:
		return (&kind.Required{Missing: sortedNames(failed.Missing)}).LocalizedString(jsonSchemaPrinter)
	case *kind.DependentRequired:
		return (&kind.DependentRequired{Prop: failed.Prop, Missing: sortedNames(failed.Missing)}).LocalizedString(jsonSchemaPrinter)
	case *kind.Dependency:
		return (&kind.Dependency{Prop: failed.Prop, Missing: sortedNames(failed.Missing)}).LocalizedString(jsonSchemaPrinter)
	default:
		return leaf.ErrorKind.LocalizedString(jsonSchemaPrinter)
	}
}

// sortedNames is a sorted copy of names, leaving the validator's own list as it is.
func sortedNames(names []string) []string {
	return slices.Sorted(slices.Values(names))
}

// schemaErrorEntry converts one leaf validation failure of kin-openapi's own validator,
// the one an OpenAPI 3.0 description is validated with, into an entry, extending the
// prefix with the JSON pointer of the value inside the body or parameter when the
// failure is a schema error and therefore knows where it happened.
func schemaErrorEntry(err error, prefix []any) errorstack.Entry {
	schemaErr, ok := err.(*openapi3.SchemaError) //nolint:errorlint // A leaf of the walk, never wrapped.
	if !ok {
		return validationEntry(err.Error(), prefix)
	}

	return validationEntry(schemaErrorMessage(schemaErr), joinPath(prefix, pointerSegments(schemaErr.JSONPointer())))
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
