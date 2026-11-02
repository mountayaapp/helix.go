package graphql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"

	gqlgen "github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// TestErrorPresenter_RawErrorIsMasked pins the P1: an error nobody wrote for a
// client — a driver's, here — used to reach the client verbatim, host and port
// included.
func TestErrorPresenter_RawErrorIsMasked(t *testing.T) {
	raw := errors.New("dial tcp 10.0.0.7:5432: connection refused")

	testcases := map[string]error{
		"presented directly": raw,
		"wrapped":            fmt.Errorf("query things: %w", raw),

		// What gqlgen actually hands the presenter for a resolver's error: wrapped in
		// a *gqlerror.Error carrying the field's path.
		"wrapped by gqlgen": gqlerror.WrapPath(ast.Path{ast.PathName("things")}, raw),
	}

	for name, err := range testcases {
		t.Run(name, func(t *testing.T) {
			gqlErr := errorPresenter(context.Background(), err)

			require.NotNil(t, gqlErr)
			assert.Equal(t, "Internal server error", gqlErr.Message)
			assert.Equal(t, map[string]any{"code": errorstack.CodeInternalError}, gqlErr.Extensions)
			assert.ErrorIs(t, gqlErr.Err, raw, "the error itself is kept, unserialized, for the operation span")

			b, jsonErr := json.Marshal(gqlErr)
			require.NoError(t, jsonErr)
			assert.NotContains(t, string(b), "10.0.0.7")
		})
	}

	t.Run("keeps the field's path", func(t *testing.T) {
		gqlErr := errorPresenter(context.Background(), gqlerror.WrapPath(ast.Path{ast.PathName("things"), ast.PathIndex(0)}, raw))

		assert.Equal(t, ast.Path{ast.PathName("things"), ast.PathIndex(0)}, gqlErr.Path)
	})
}

// TestErrorPresenter_GQLErrorPassesThrough covers what is not masked: an error
// gqlgen built itself for a parse or validation failure, and one a resolver built
// deliberately for its client.
func TestErrorPresenter_GQLErrorPassesThrough(t *testing.T) {
	validation := gqlerror.Errorf("Cannot query field \"nme\" on type \"Query\".")
	errcode.Set(validation, errcode.ValidationFailed)

	gqlErr := errorPresenter(context.Background(), validation)

	assert.Same(t, validation, gqlErr)
	assert.Equal(t, errcode.ValidationFailed, gqlErr.Extensions["code"])
}

func TestErrorPresenter_GQLErrorWithoutCode(t *testing.T) {
	t.Run("refusing the request is a bad request", func(t *testing.T) {
		gqlErr := errorPresenter(context.Background(), gqlerror.Errorf("unsupported APQ version"))

		assert.Equal(t, errorstack.CodeBadRequest, gqlErr.Extensions["code"])
	})

	// Inside a field it is a resolver's own error, whose code is its business.
	t.Run("returned by a resolver is left alone", func(t *testing.T) {
		ctx := fieldContext(context.Background(), "Query", "name", true)

		gqlErr := errorPresenter(ctx, gqlerror.Errorf("Name is not available"))

		assert.NotContains(t, gqlErr.Extensions, "code")
	})
}

func TestErrorPresenter_ErrorstackSingleEntry(t *testing.T) {
	err := errorstack.New("Resource does not exist", errorstack.WithCode(errorstack.CodeNotFound))

	gqlErr := errorPresenter(context.Background(), err)

	require.NotNil(t, gqlErr)
	assert.Equal(t, "Resource does not exist", gqlErr.Message)
	assert.Equal(t, errorstack.CodeNotFound, gqlErr.Extensions["code"])
	assert.Nil(t, gqlErr.Extensions["additional_errors"])
}

// TestErrorPresenter_EntryPath_GoesToExtensions pins GQL-15: errors[].path is the
// path of the response field that errored, which the spec reserves it for. An
// entry's Path names a part of the request, so it goes in extensions.input_path.
func TestErrorPresenter_EntryPath_GoesToExtensions(t *testing.T) {
	resolverPath := ast.Path{ast.PathName("createThing")}
	err := gqlerror.WrapPath(resolverPath, errorstack.New("Must be a valid email address",
		errorstack.WithCode(errorstack.CodeValidationFailed),
		errorstack.WithPath("input", "items", 0, "email"),
	))

	gqlErr := errorPresenter(context.Background(), err)

	require.NotNil(t, gqlErr)
	assert.Equal(t, resolverPath, gqlErr.Path, "errors[].path stays the resolver path")
	assert.Equal(t, []any{"input", "items", 0, "email"}, gqlErr.Extensions[extensionInputPath])
	assert.Equal(t, errorstack.CodeValidationFailed, gqlErr.Extensions["code"])
	_, hasExtensionPath := gqlErr.Extensions["path"]
	assert.False(t, hasExtensionPath)
}

func TestErrorPresenter_EntryExtensions_AreCopied(t *testing.T) {
	err := errorstack.New("Resource does not exist",
		errorstack.WithCode(errorstack.CodeNotFound),
		errorstack.WithPath("id"),
	)

	gqlErr := errorPresenter(context.Background(), err)
	gqlErr.Extensions["mutated"] = true

	assert.NotContains(t, err.Entries[0].Extensions, "mutated", "the presented error must not share the entry's map")
	assert.NotContains(t, err.Entries[0].Extensions, extensionInputPath)
}

func TestErrorPresenter_ErrorstackMultipleEntries(t *testing.T) {
	// Returning a multi-entry *errorstack.Error from a resolver surfaces
	// entries[0] as the returned *gqlerror.Error and fans entries[1:] into
	// gqlgen's collector via gqlgen.AddError, producing one wire-level
	// errors[] element per entry. No additional_errors aggregation.
	ctx, getErrors := errorListContext()
	resolverPath := ast.Path{ast.PathName("createThing")}

	err := gqlerror.WrapPath(resolverPath, errorstack.NewValidation(
		errorstack.Entry{Message: "Must be a valid email address", Path: []any{"input", "email"}},
		errorstack.Entry{Message: "Must be set", Path: []any{"input", "name"}},
	))

	gqlErr := errorPresenter(ctx, err)

	require.NotNil(t, gqlErr)
	assert.Equal(t, "Must be a valid email address", gqlErr.Message)
	assert.Equal(t, errorstack.CodeValidationFailed, gqlErr.Extensions["code"])
	assert.Equal(t, resolverPath, gqlErr.Path)
	assert.Equal(t, []any{"input", "email"}, gqlErr.Extensions[extensionInputPath])
	_, hasAdditional := gqlErr.Extensions["additional_errors"]
	assert.False(t, hasAdditional, "entries[1:] are fanned out via gqlgen.AddError, not aggregated")

	collected := getErrors()
	require.Len(t, collected, 1, "errorPresenter fans only entries[1:] into the collector")
	assert.Equal(t, "Must be set", collected[0].Message)
	assert.Equal(t, resolverPath, collected[0].Path)
	assert.Equal(t, []any{"input", "name"}, collected[0].Extensions[extensionInputPath])
	assert.Equal(t, errorstack.CodeValidationFailed, collected[0].Extensions["code"])
}

func TestErrorPresenter_TopLevelExtensions_ForwardedToResponse(t *testing.T) {
	// The top-level *errorstack.Error.Extensions map is forwarded onto
	// gqlgen's response-level extensions via gqlgen.RegisterExtension —
	// mirroring how REST surfaces err.SetExtension at the response top
	// level. Per-error gqlErr.Extensions stays scoped to the entry.
	ctx, _ := errorListContext()

	err := errorstack.New("Resource does not exist", errorstack.WithCode(errorstack.CodeNotFound)).
		SetExtension("trace_id", "abc-123")

	gqlErr := errorPresenter(ctx, err)

	require.NotNil(t, gqlErr)
	assert.Equal(t, errorstack.CodeNotFound, gqlErr.Extensions["code"], "per-entry code is copied onto gqlErr")
	_, leaked := gqlErr.Extensions["trace_id"]
	assert.False(t, leaked, "top-level Error.Extensions stays out of the per-error gqlErr.Extensions")

	assert.Equal(t, "abc-123", gqlgen.GetExtensions(ctx)["trace_id"], "top-level Error.Extensions are forwarded to response-level extensions")
}

func TestErrorPresenter_TopLevelExtensions_FirstValueWins(t *testing.T) {
	ctx, _ := errorListContext()

	first := errorstack.New("Resource does not exist").SetExtension("key", "first").SetExtension("only_first", 1)
	second := errorstack.New("Resource does not exist").SetExtension("key", "second").SetExtension("only_second", 2)

	require.NotPanics(t, func() {
		errorPresenter(ctx, first)
		errorPresenter(ctx, second)
	})

	assert.Equal(t, map[string]any{"key": "first", "only_first": 1, "only_second": 2}, gqlgen.GetExtensions(ctx),
		"a duplicate key is skipped without skipping the keys after it")
}

func TestErrorPresenter_TopLevelExtensions_OutsideResponseContext_NoPanic(t *testing.T) {
	// Calling the presenter outside a gqlgen response context (e.g. the
	// HTTP-layer error path before GraphQL execution) must not panic when
	// the error carries top-level Extensions — forwarding silently skips.
	err := errorstack.New("Resource does not exist", errorstack.WithCode(errorstack.CodeNotFound)).
		SetExtension("trace_id", "abc-123")

	assert.NotPanics(t, func() {
		_ = errorPresenter(context.Background(), err)
	})
}

func TestErrorPresenter_WrappedErrorChain(t *testing.T) {
	inner := errorstack.New("Resource does not exist", errorstack.WithCode(errorstack.CodeNotFound))
	wrapped := errorstack.Wrap(inner, "Failed to handle request")

	gqlErr := errorPresenter(context.Background(), wrapped)

	require.NotNil(t, gqlErr)
	assert.Equal(t, "Failed to handle request", gqlErr.Message)
	assert.Equal(t, errorstack.CodeInternalError, gqlErr.Extensions["code"])
}

func TestErrorPresenter_Nil(t *testing.T) {
	assert.Nil(t, errorPresenter(context.Background(), nil))
}

// errorListContext wraps a context with a minimal gqlgen error collector so
// AddErrors can fan out without a full gqlgen request. Returns the context
// and a getter for the collected errors.
func errorListContext() (context.Context, func() gqlerror.List) {
	ctx := gqlgen.WithOperationContext(context.Background(), &gqlgen.OperationContext{
		ResolverMiddleware: func(ctx context.Context, next gqlgen.Resolver) (any, error) {
			return next(ctx)
		},
	})
	ctx = gqlgen.WithResponseContext(ctx, errorPresenter, func(ctx context.Context, err any) error {
		return nil
	})

	return ctx, func() gqlerror.List {
		return gqlgen.GetErrors(ctx)
	}
}

func TestAddErrors_FansOutOnePerEntry(t *testing.T) {
	// AddErrors fans out each entry as its own *gqlerror.Error so the response
	// surfaces every entry as a top-level errors[] element.
	ctx, getErrors := errorListContext()
	ctx = gqlgen.WithFieldContext(ctx, &gqlgen.FieldContext{
		Field: gqlgen.CollectedField{Field: &ast.Field{Alias: "createThing"}},
	})

	err := errorstack.NewValidation(
		errorstack.Entry{Message: "Must be a valid email address", Path: []any{"input", "email"}},
		errorstack.Entry{Message: "Must be set", Path: []any{"input", "name"}},
	)

	AddErrors(ctx, err)

	got := getErrors()
	require.Len(t, got, 2, "AddErrors emits one *gqlerror.Error per entry")
	assert.Equal(t, "Must be a valid email address", got[0].Message)
	assert.Equal(t, ast.Path{ast.PathName("createThing")}, got[0].Path, "errors[].path is the resolver path")
	assert.Equal(t, []any{"input", "email"}, got[0].Extensions[extensionInputPath])
	assert.Equal(t, errorstack.CodeValidationFailed, got[0].Extensions["code"])
	_, hasAdditional := got[0].Extensions["additional_errors"]
	assert.False(t, hasAdditional, "AddErrors path emits per-entry; no additional_errors aggregation")

	assert.Equal(t, "Must be set", got[1].Message)
	assert.Equal(t, []any{"input", "name"}, got[1].Extensions[extensionInputPath])
}

func TestAddErrors_EntryWithoutPath_HasNoInputPath(t *testing.T) {
	ctx, getErrors := errorListContext()

	err := errorstack.New("Internal failure", errorstack.WithCode(errorstack.CodeInternalError))

	AddErrors(ctx, err)

	got := getErrors()
	require.Len(t, got, 1)
	assert.Equal(t, "Internal failure", got[0].Message)
	assert.Nil(t, got[0].Path, "without a resolver path, Path stays nil")
	assert.NotContains(t, got[0].Extensions, extensionInputPath)
}

func TestAddErrors_NilError_NoOp(t *testing.T) {
	ctx, getErrors := errorListContext()

	AddErrors(ctx, nil)

	assert.Empty(t, getErrors())
}
