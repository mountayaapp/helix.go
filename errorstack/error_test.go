package errorstack

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_DefaultsToInternalError(t *testing.T) {
	err := New("Failed to validate configuration")

	require.Len(t, err.Entries, 1)
	assert.Equal(t, "Failed to validate configuration", err.Entries[0].Message)
	assert.Equal(t, CodeInternalError, err.Entries[0].Extensions["code"])
	assert.Nil(t, err.Entries[0].Path)
}

func TestNew_WithCodeOverridesDefault(t *testing.T) {
	err := New("Resource does not exist", WithCode(CodeNotFound))

	assert.Equal(t, CodeNotFound, err.Entries[0].Extensions["code"])
}

func TestNew_WithPath(t *testing.T) {
	err := New("Must be set", WithPath("config", "address"))

	assert.Equal(t, []any{"config", "address"}, err.Entries[0].Path)
}

func TestNew_WithExtension(t *testing.T) {
	err := New("Failed to handle request", WithExtension("trace_id", "abc-123"))

	assert.Equal(t, "abc-123", err.Entries[0].Extensions["trace_id"])
	assert.Equal(t, CodeInternalError, err.Entries[0].Extensions["code"])
}

func TestNew_CombinedOptions(t *testing.T) {
	// Options are independent and must compose on a single Entry: WithCode,
	// WithPath, and WithExtension all land on the same entry without
	// stomping each other.
	err := New("Must be a valid email address",
		WithCode(CodeBadRequest),
		WithPath("request", "body", "email"),
		WithExtension("hint", "use a corporate domain"),
	)

	require.Len(t, err.Entries, 1)
	assert.Equal(t, "Must be a valid email address", err.Entries[0].Message)
	assert.Equal(t, CodeBadRequest, err.Entries[0].Extensions["code"])
	assert.Equal(t, []any{"request", "body", "email"}, err.Entries[0].Path)
	assert.Equal(t, "use a corporate domain", err.Entries[0].Extensions["hint"])
}

func TestValidation_ForcesValidationFailedCode(t *testing.T) {
	err := NewValidation(
		Entry{Message: "Must be set", Path: []any{"config", "address"}},
		Entry{
			Message:    "Must be set",
			Path:       []any{"config", "database"},
			Extensions: map[string]any{"code": "OVERRIDDEN"},
		},
	)

	require.Len(t, err.Entries, 2)
	for _, entry := range err.Entries {
		assert.Equal(t, CodeValidationFailed, entry.Extensions["code"])
	}
}

func TestValidation_PreservesCustomExtensions(t *testing.T) {
	err := NewValidation(Entry{
		Message:    "Must be a valid email address",
		Path:       []any{"request", "body", "email"},
		Extensions: map[string]any{"hint": "use a corporate domain"},
	})

	assert.Equal(t, CodeValidationFailed, err.Entries[0].Extensions["code"])
	assert.Equal(t, "use a corporate domain", err.Entries[0].Extensions["hint"])
}

func TestValidation_EmptyFallsBackToInternalError(t *testing.T) {
	err := NewValidation()

	require.Len(t, err.Entries, 1)
	assert.Equal(t, CodeInternalError, err.Entries[0].Extensions["code"])
}

func TestWrap_NilReturnsNil(t *testing.T) {
	assert.Nil(t, Wrap(nil, "Failed to validate configuration"))
}

func TestWrap_NilThroughErrorInterface(t *testing.T) {
	// Regression: Wrap must return Go's untyped nil when cause is nil, even
	// when funneled through a function typed `error`. Returning *Error here
	// would surface the typed-nil-through-interface trap (a nil *Error wrapped
	// in error compares non-nil at the call site), masking clean shutdowns as
	// failures — see integration/temporal/integration_worker.go.
	makeErr := func(in error) error {
		return Wrap(in, "Failed to start server")
	}

	assert.Nil(t, makeErr(nil))
	assert.False(t, makeErr(nil) != nil, "Wrap(nil, …) must compare equal to nil through error interface")
}

func TestWrap_PreservesCause(t *testing.T) {
	sentinel := errors.New("connection refused")

	wrapped := Wrap(sentinel, "Failed to initialize integration")

	assert.True(t, errors.Is(wrapped, sentinel))
	assert.Equal(t, sentinel, errors.Unwrap(wrapped))
}

func TestWrap_ErrorsAs(t *testing.T) {
	inner := New("Failed to validate configuration")
	wrapped := Wrap(inner, "Failed to initialize integration")

	var target *Error
	assert.True(t, errors.As(wrapped, &target))
	assert.Equal(t, "Failed to initialize integration", target.Entries[0].Message)
}

func TestWrap_MultiLevelChain(t *testing.T) {
	// Wrapping multiple times must preserve the full chain: errors.Is finds
	// the root cause through all levels, errors.As surfaces the outermost
	// *Error (the most recent context), and Error() includes the root cause
	// text at the tail.
	root := errors.New("connection refused")
	l1 := Wrap(root, "Failed to dial database")
	l2 := Wrap(l1, "Failed to initialize integration")
	l3 := Wrap(l2, "Failed to start service")

	assert.True(t, errors.Is(l3, root), "errors.Is traverses the full Unwrap chain to the root cause")

	var target *Error
	require.True(t, errors.As(l3, &target))
	assert.Equal(t, "Failed to start service", target.Entries[0].Message, "errors.As surfaces the outermost *Error")

	assert.Contains(t, l3.Error(), "connection refused", "Error() includes the root cause text")
}

func TestEntriesOf_MultiLevelChainReturnsOutermost(t *testing.T) {
	// errors.As finds the first *Error in the chain; since Wrap returns an
	// *Error, EntriesOf on a wrapped *Error must return the OUTER entries,
	// not unwrap further to the inner. Locks in the documented behavior.
	inner := New("Inner failure", WithCode(CodeNotFound))
	outer := Wrap(inner, "Outer context", WithCode(CodeInternalError))

	entries := EntriesOf(outer)

	require.Len(t, entries, 1)
	assert.Equal(t, "Outer context", entries[0].Message)
	assert.Equal(t, CodeInternalError, entries[0].Extensions["code"])
}

func TestAppend_Chainable(t *testing.T) {
	err := New("Failed to validate configuration")
	result := err.Append(Entry{Message: "Must be set"})

	assert.Same(t, err, result)
	assert.Len(t, err.Entries, 2)
}

func TestSetExtension_TopLevel(t *testing.T) {
	err := New("Failed to handle request").SetExtension("trace_id", "abc-123")

	assert.Equal(t, "abc-123", err.Extensions["trace_id"])
}

func TestError_String(t *testing.T) {
	testcases := []struct {
		name     string
		input    *Error
		expected string
	}{
		{
			name:     "single entry, no path",
			input:    New("Resource does not exist", WithCode(CodeNotFound)),
			expected: "NOT_FOUND: Resource does not exist",
		},
		{
			name: "validation entries with paths",
			input: NewValidation(
				Entry{Message: "Must be set", Path: []any{"config", "address"}},
				Entry{Message: "Must be set", Path: []any{"config", "database"}},
			),
			expected: "VALIDATION_FAILED: Must be set (config.address); Must be set (config.database)",
		},
		{
			name:     "default code",
			input:    New("Failed to handle request"),
			expected: "INTERNAL_ERROR: Failed to handle request",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.input.Error())
		})
	}
}

func TestError_StringEmptyEntries(t *testing.T) {
	// A manually-constructed *Error with no entries must still produce a
	// sensible log line. The fallback branch in Error() emits a canonical
	// INTERNAL_ERROR string; when a cause is attached, its text is appended.
	t.Run("no cause", func(t *testing.T) {
		assert.Equal(t, "INTERNAL_ERROR: Internal server error", (&Error{}).Error())
	})

	t.Run("with cause", func(t *testing.T) {
		err := &Error{cause: errors.New("disk full")}
		assert.Equal(t, "INTERNAL_ERROR: Internal server error: disk full", err.Error())
	})
}

func TestMarshalJSON_SingleEntry(t *testing.T) {
	err := New("Resource does not exist", WithCode(CodeNotFound))

	b, marshalErr := json.Marshal(err)

	require.NoError(t, marshalErr)
	assert.JSONEq(t, `{
		"errors": [
			{"message": "Resource does not exist", "extensions": {"code": "NOT_FOUND"}}
		]
	}`, string(b))
}

func TestMarshalJSON_MultipleEntries(t *testing.T) {
	err := NewValidation(
		Entry{Message: "Must be a valid email address", Path: []any{"request", "body", "email"}},
		Entry{Message: "Must be set", Path: []any{"request", "body", "name"}},
	)

	b, marshalErr := json.Marshal(err)

	require.NoError(t, marshalErr)
	assert.JSONEq(t, `{
		"errors": [
			{
				"message": "Must be a valid email address",
				"path": ["request", "body", "email"],
				"extensions": {"code": "VALIDATION_FAILED"}
			},
			{
				"message": "Must be set",
				"path": ["request", "body", "name"],
				"extensions": {"code": "VALIDATION_FAILED"}
			}
		]
	}`, string(b))
}

func TestMarshalJSON_WithTopLevelExtensions(t *testing.T) {
	err := New("Resource does not exist", WithCode(CodeNotFound)).
		SetExtension("metadata", map[string]any{"trace_id": "abc-123"})

	b, marshalErr := json.Marshal(err)

	require.NoError(t, marshalErr)
	assert.JSONEq(t, `{
		"errors": [
			{"message": "Resource does not exist", "extensions": {"code": "NOT_FOUND"}}
		],
		"extensions": {"metadata": {"trace_id": "abc-123"}}
	}`, string(b))
}

func TestMarshalJSON_AlwaysIncludesErrorsField(t *testing.T) {
	err := &Error{}

	b, marshalErr := json.Marshal(err)

	require.NoError(t, marshalErr)
	assert.Contains(t, string(b), `"errors"`)
}

func TestEntry_PathSerializesAsLowerSnakeCase(t *testing.T) {
	entry := Entry{
		Message: "Must be set",
		Path:    []any{"request", "headers", "x_api_key"},
	}

	b, err := json.Marshal(entry)

	require.NoError(t, err)
	assert.JSONEq(t, `{
		"message": "Must be set",
		"path": ["request", "headers", "x_api_key"]
	}`, string(b))
}

func TestMarshalJSON_NormalizesMissingCode(t *testing.T) {
	err := New("Failed to handle request").Append(Entry{Message: "Must be set"})

	b, marshalErr := json.Marshal(err)

	require.NoError(t, marshalErr)
	assert.JSONEq(t, `{
		"errors": [
			{"message": "Failed to handle request", "extensions": {"code": "INTERNAL_ERROR"}},
			{"message": "Must be set", "extensions": {"code": "INTERNAL_ERROR"}}
		]
	}`, string(b))
}

func TestEntry_PathSupportsIntegerIndexes(t *testing.T) {
	// Per Path doc: segments may be lower_snake_case strings or integer
	// indexes (e.g. when a list element validation fails).
	entry := Entry{
		Message: "Must be set",
		Path:    []any{"items", 0, "name"},
	}

	b, err := json.Marshal(entry)

	require.NoError(t, err)
	assert.JSONEq(t, `{
		"message": "Must be set",
		"path": ["items", 0, "name"]
	}`, string(b))
}

func TestError_StringWithIntegerPathSegments(t *testing.T) {
	err := New("Must be set", WithPath("items", 0, "name"))

	assert.Equal(t, "INTERNAL_ERROR: Must be set (items.0.name)", err.Error())
}

func TestSetExtension_DoesNotMutatePerEntryCode(t *testing.T) {
	// SetExtension targets the response-level (top-level) Extensions map,
	// not per-entry extensions. Calling SetExtension("code", ...) must not
	// override the per-entry code.
	err := New("Resource does not exist", WithCode(CodeNotFound)).
		SetExtension("code", "TOPLEVEL_OVERRIDE")

	assert.Equal(t, "TOPLEVEL_OVERRIDE", err.Extensions["code"], "top-level extension is set")
	assert.Equal(t, CodeNotFound, err.Entries[0].Extensions["code"], "per-entry code is unaffected")
}

func TestEntriesOf_TypedNilStarErrorReturnsNil(t *testing.T) {
	// EntriesOf must handle a typed-nil *Error stored in an error
	// interface — errors.As succeeds but the inner pointer is nil, which
	// would panic if dereferenced.
	var typed *Error
	var iface error = typed

	assert.Nil(t, EntriesOf(iface))
}

func TestEntriesOf_NilReturnsNil(t *testing.T) {
	assert.Nil(t, EntriesOf(nil))
}

func TestEntriesOf_TypedNilDoesNotPanic(t *testing.T) {
	var typed *Error
	var iface error = typed

	assert.NotPanics(t, func() { _ = EntriesOf(iface) })
	assert.Nil(t, EntriesOf(iface))
}

func TestEntriesOf_ErrorReturnsEntries(t *testing.T) {
	err := New("Resource does not exist", WithCode(CodeNotFound))

	entries := EntriesOf(err)

	require.Len(t, entries, 1)
	assert.Equal(t, "Resource does not exist", entries[0].Message)
	assert.Equal(t, CodeNotFound, entries[0].Extensions["code"])
}

func TestEntriesOf_PlainErrorFallsBackToInternal(t *testing.T) {
	entries := EntriesOf(errors.New("connection refused"))

	require.Len(t, entries, 1)
	assert.Equal(t, "connection refused", entries[0].Message)
	assert.Equal(t, CodeInternalError, entries[0].Extensions["code"])
}

func TestHTTPStatusToCode_AllSupportedCodes(t *testing.T) {
	testcases := []struct {
		status   int
		expected string
	}{
		{http.StatusBadRequest, CodeBadRequest},
		{http.StatusUnauthorized, CodeUnauthorized},
		{http.StatusPaymentRequired, CodePaymentRequired},
		{http.StatusForbidden, CodeForbidden},
		{http.StatusNotFound, CodeNotFound},
		{http.StatusMethodNotAllowed, CodeMethodNotAllowed},
		{http.StatusConflict, CodeConflict},
		{http.StatusRequestEntityTooLarge, CodePayloadTooLarge},
		{http.StatusTooManyRequests, CodeTooManyRequests},
		{http.StatusInternalServerError, CodeInternalError},
		{http.StatusNotImplemented, CodeNotImplemented},
		{http.StatusBadGateway, CodeBadGateway},
		{http.StatusServiceUnavailable, CodeServiceUnavailable},
		{http.StatusGatewayTimeout, CodeGatewayTimeout},
	}

	for _, tc := range testcases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			assert.Equal(t, tc.expected, HTTPStatusToCode(tc.status))
		})
	}
}

func TestHTTPStatusToCode_UnknownReturnsInternalError(t *testing.T) {
	assert.Equal(t, CodeInternalError, HTTPStatusToCode(418))
}
