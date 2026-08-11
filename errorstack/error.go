package errorstack

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

/*
Ensure *Error complies to Go error and json.Marshaler types.
*/
var (
	_ error          = (*Error)(nil)
	_ json.Marshaler = (*Error)(nil)
)

/*
Canonical machine-readable codes carried in extensions.code. The set mirrors
the HTTP status codes supported by helix.go plus VALIDATION_FAILED for entries
produced via NewValidation.
*/
const (
	CodeBadRequest         = "BAD_REQUEST"
	CodeUnauthorized       = "UNAUTHORIZED"
	CodePaymentRequired    = "PAYMENT_REQUIRED"
	CodeForbidden          = "FORBIDDEN"
	CodeNotFound           = "NOT_FOUND"
	CodeMethodNotAllowed   = "METHOD_NOT_ALLOWED"
	CodeConflict           = "CONFLICT"
	CodePayloadTooLarge    = "PAYLOAD_TOO_LARGE"
	CodeTooManyRequests    = "TOO_MANY_REQUESTS"
	CodeInternalError      = "INTERNAL_ERROR"
	CodeNotImplemented     = "NOT_IMPLEMENTED"
	CodeBadGateway         = "BAD_GATEWAY"
	CodeServiceUnavailable = "SERVICE_UNAVAILABLE"
	CodeGatewayTimeout     = "GATEWAY_TIMEOUT"
	CodeValidationFailed   = "VALIDATION_FAILED"
)

/*
Entry is a single element of the spec errors[] array. It mirrors the GraphQL
error shape: a required message, an optional path identifying the field that
errored, and an optional extensions map carrying machine-readable metadata such
as a code.
*/
type Entry struct {

	// Message is the human-readable description of the error. Sentence case,
	// no trailing period, present tense, actionable.
	Message string `json:"message"`

	// Path identifies the field in the request payload (or response data, for
	// resolvers) that errored. Segments should be lower_snake_case strings or
	// integer indexes (for list-element validation).
	//
	// Example:
	//
	//   []any{"request", "body", "email"}
	//   []any{"items", 0, "name"}
	Path []any `json:"path,omitempty"`

	// Extensions carries machine-readable metadata. The "code" key is always
	// set on entries produced by New, Validation, or Wrap.
	Extensions map[string]any `json:"extensions,omitempty"`
}

/*
Error is a Go error that serializes to a GraphQL-spec error envelope:

	{"errors":[{"message":"…","path":[…],"extensions":{…}}, …], "extensions":{…}}

Constructors guarantee at least one entry. Error is not safe for concurrent
mutation: build it on a single goroutine via constructors and chainable
methods (Append, SetExtension), then return. Reading a built Error from
multiple goroutines is safe.
*/
type Error struct {

	// Entries is the non-empty list of errors[] elements emitted in the
	// response envelope.
	Entries []Entry

	// Extensions is the optional top-level extensions map of the response
	// envelope (distinct from per-entry extensions).
	Extensions map[string]any

	// cause is the wrapped underlying error preserved for errors.Is/As
	// support. Never serialized.
	cause error
}

/*
EntryOption configures a single Entry at construction time.
*/
type EntryOption func(*Entry)

/*
WithCode sets the extensions.code value on the entry. When not set, New
defaults to CodeInternalError; Validation always overrides to
CodeValidationFailed.
*/
func WithCode(code string) EntryOption {
	return func(entry *Entry) {
		if entry.Extensions == nil {
			entry.Extensions = map[string]any{}
		}

		entry.Extensions["code"] = code
	}
}

/*
WithPath sets the path of the entry. Segments should be lower_snake_case
strings or integer indexes.
*/
func WithPath(segments ...any) EntryOption {
	return func(entry *Entry) {
		entry.Path = segments
	}
}

/*
WithExtension sets a single key on the entry's extensions map. Multiple calls
accumulate. The reserved "code" key should be set via WithCode.
*/
func WithExtension(key string, value any) EntryOption {
	return func(entry *Entry) {
		if entry.Extensions == nil {
			entry.Extensions = map[string]any{}
		}

		entry.Extensions[key] = value
	}
}

/*
New returns an Error containing a single Entry built from the message and
options. The entry's extensions.code defaults to CodeInternalError unless
WithCode is provided.
*/
func New(message string, opts ...EntryOption) *Error {
	entry := Entry{
		Message:    message,
		Extensions: map[string]any{"code": CodeInternalError},
	}

	for _, opt := range opts {
		opt(&entry)
	}

	return &Error{
		Entries: []Entry{entry},
	}
}

/*
NewValidation returns an Error built from one or more validation entries. Each
entry's extensions.code is forced to CodeValidationFailed; any other extension
keys carried on the input entries are preserved.

Returns a non-nil *Error with at least one entry. Empty input yields a single
fallback entry with code CodeInternalError rather than silently degrading to
nil — callers may still rely on err != nil to detect the validation case.
*/
func NewValidation(entries ...Entry) *Error {
	out := make([]Entry, 0, len(entries))

	for _, entry := range entries {
		if entry.Extensions == nil {
			entry.Extensions = map[string]any{}
		}
		entry.Extensions["code"] = CodeValidationFailed
		out = append(out, entry)
	}

	if len(out) == 0 {
		out = append(out, Entry{
			Message:    "Internal server error",
			Extensions: map[string]any{"code": CodeInternalError},
		})
	}

	return &Error{
		Entries: out,
	}
}

/*
Wrap returns a single-entry *Error built from message and opts, with cause
preserved for errors.Is/As support and surfaced through Error(). The cause's
text never leaks into Entries — keeping the response envelope focused on what
should reach a client.

The return type is the error interface (not *Error) so that the nil case
propagates cleanly through interface-typed callers:

	func Start(ctx context.Context) error {
	    return errorstack.Wrap(srv.Run(ctx), "Failed to start server")
	}

Returning *Error here would expose the typed-nil-through-interface trap (a nil
*Error becomes a non-nil error interface value); returning error sidesteps it.
*/
func Wrap(cause error, message string, opts ...EntryOption) error {
	if cause == nil {
		return nil
	}

	err := New(message, opts...)
	err.cause = cause
	return err
}

/*
Append appends entries to the Error and returns it for chaining.
*/
func (err *Error) Append(entries ...Entry) *Error {
	err.Entries = append(err.Entries, entries...)
	return err
}

/*
SetExtension sets a key on the response-level (top-level) extensions map and
returns the Error for chaining.
*/
func (err *Error) SetExtension(key string, value any) *Error {
	if err.Extensions == nil {
		err.Extensions = map[string]any{}
	}

	err.Extensions[key] = value
	return err
}

/*
Unwrap returns the wrapped cause for errors.Is/As support.
*/
func (err *Error) Unwrap() error {
	return err.cause
}

/*
Error returns a short, log-line-friendly stringification of the error: the
first entry's code, followed by each entry's message and (when set) its path.
When a cause is present, its text is appended after a colon.
*/
func (err *Error) Error() string {
	var b strings.Builder

	if len(err.Entries) == 0 {
		b.WriteString(CodeInternalError)
		b.WriteString(": Internal server error")
		if err.cause != nil {
			b.WriteString(": ")
			b.WriteString(err.cause.Error())
		}

		return b.String()
	}

	if code, ok := err.Entries[0].Extensions["code"].(string); ok && code != "" {
		b.WriteString(code)
		b.WriteString(": ")
	}

	for i, entry := range err.Entries {
		if i > 0 {
			b.WriteString("; ")
		}

		b.WriteString(entry.Message)

		if len(entry.Path) > 0 {
			b.WriteString(" (")
			for j, segment := range entry.Path {
				if j > 0 {
					b.WriteByte('.')
				}

				switch v := segment.(type) {
				case string:
					b.WriteString(v)
				default:
					b.WriteString(strings.Trim(toJSON(v), `"`))
				}
			}

			b.WriteByte(')')
		}
	}

	if err.cause != nil {
		b.WriteString(": ")
		b.WriteString(err.cause.Error())
	}

	return b.String()
}

/*
MarshalJSON serializes the Error to the spec-compliant response envelope:

	{"errors":[…], "extensions":{…}}

errors[] is always non-empty and every entry carries a non-empty
extensions.code (defaulted to CodeInternalError when missing), so the wire
shape always satisfies the OpenAPI Error schema even for entries appended
without going through the public constructors. The top-level extensions
field is omitted when empty.
*/
func (err *Error) MarshalJSON() ([]byte, error) {
	entries := err.Entries
	if len(entries) == 0 {
		entries = []Entry{{
			Message:    "Internal server error",
			Extensions: map[string]any{"code": CodeInternalError},
		}}
	}

	normalized := make([]Entry, len(entries))
	for i, entry := range entries {
		if _, ok := entry.Extensions["code"].(string); !ok || entry.Extensions["code"] == "" {
			ext := make(map[string]any, len(entry.Extensions)+1)
			for k, v := range entry.Extensions {
				ext[k] = v
			}
			ext["code"] = CodeInternalError
			entry.Extensions = ext
		}
		normalized[i] = entry
	}

	return json.Marshal(struct {
		Errors     []Entry        `json:"errors"`
		Extensions map[string]any `json:"extensions,omitempty"`
	}{
		Errors:     normalized,
		Extensions: err.Extensions,
	})
}

/*
EntriesOf returns the errors[] entries representing err. If err is an *Error
(directly or via the unwrap chain), its Entries are returned verbatim.
Otherwise a single fallback entry is built from err.Error() carrying
CodeInternalError. Returns nil when err is nil.
*/
func EntriesOf(err error) []Entry {
	if err == nil {
		return nil
	}

	var inner *Error
	if errors.As(err, &inner) {
		if inner == nil {
			return nil
		}

		return inner.Entries
	}

	return []Entry{{
		Message:    err.Error(),
		Extensions: map[string]any{"code": CodeInternalError},
	}}
}

/*
HTTPStatusToCode maps an HTTP status code to its canonical
SCREAMING_SNAKE_CASE code. Unknown statuses fall back to CodeInternalError.
*/
func HTTPStatusToCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return CodeBadRequest
	case http.StatusUnauthorized:
		return CodeUnauthorized
	case http.StatusPaymentRequired:
		return CodePaymentRequired
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusMethodNotAllowed:
		return CodeMethodNotAllowed
	case http.StatusConflict:
		return CodeConflict
	case http.StatusRequestEntityTooLarge:
		return CodePayloadTooLarge
	case http.StatusTooManyRequests:
		return CodeTooManyRequests
	case http.StatusInternalServerError:
		return CodeInternalError
	case http.StatusNotImplemented:
		return CodeNotImplemented
	case http.StatusBadGateway:
		return CodeBadGateway
	case http.StatusServiceUnavailable:
		return CodeServiceUnavailable
	case http.StatusGatewayTimeout:
		return CodeGatewayTimeout
	default:
		return CodeInternalError
	}
}

/*
toJSON marshals v with encoding/json and returns its string form, falling back
to an empty string on error. Used only for path-segment stringification in
Error().
*/
func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}

	return string(b)
}
