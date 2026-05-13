package graphql

import (
	"context"
	"errors"
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"

	gqlgen "github.com/99designs/gqlgen/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestErrorPresenter_PlainErrorPassesThrough(t *testing.T) {
	err := errors.New("connection refused")

	gqlErr := errorPresenter(context.Background(), err)

	require.NotNil(t, gqlErr)
	assert.Equal(t, "connection refused", gqlErr.Message)
}

func TestErrorPresenter_ErrorstackSingleEntry(t *testing.T) {
	err := errorstack.New("Resource does not exist", errorstack.WithCode(errorstack.CodeNotFound))

	gqlErr := errorPresenter(context.Background(), err)

	require.NotNil(t, gqlErr)
	assert.Equal(t, "Resource does not exist", gqlErr.Message)
	assert.Equal(t, errorstack.CodeNotFound, gqlErr.Extensions["code"])
	assert.Nil(t, gqlErr.Extensions["additional_errors"])
}

func TestErrorPresenter_EntryPath_PromotesToSpecLevel(t *testing.T) {
	// User-supplied Entry.Path must be promoted to the spec-level
	// errors[].path slot, not buried under extensions.path. This mirrors
	// REST behavior so wire shapes stay consistent across protocols.
	err := errorstack.New("Must be a valid email address",
		errorstack.WithCode(errorstack.CodeValidationFailed),
		errorstack.WithPath("request", "body", "email"),
	)

	gqlErr := errorPresenter(context.Background(), err)

	require.NotNil(t, gqlErr)
	require.NotNil(t, gqlErr.Path)
	assert.Equal(t, ast.Path{ast.PathName("request"), ast.PathName("body"), ast.PathName("email")}, gqlErr.Path)
	_, hasExtensionPath := gqlErr.Extensions["path"]
	assert.False(t, hasExtensionPath, "Entry.Path must not leak into extensions.path")
}

func TestErrorPresenter_EntryPath_IntegerSegmentsConvertToPathIndex(t *testing.T) {
	err := errorstack.New("Item is invalid",
		errorstack.WithPath("items", 0, "name"),
	)

	gqlErr := errorPresenter(context.Background(), err)

	require.NotNil(t, gqlErr)
	require.NotNil(t, gqlErr.Path)
	assert.Equal(t, ast.Path{ast.PathName("items"), ast.PathIndex(0), ast.PathName("name")}, gqlErr.Path)
}

func TestErrorPresenter_ErrorstackMultipleEntries(t *testing.T) {
	// Returning a multi-entry *errorstack.Error from a resolver surfaces
	// entries[0] as the returned *gqlerror.Error and fans entries[1:] into
	// gqlgen's collector via gqlgen.AddError, producing one wire-level
	// errors[] element per entry. No additional_errors aggregation.
	ctx, getErrors := errorListContext()

	err := errorstack.NewValidation(
		errorstack.Entry{Message: "Must be a valid email address", Path: []any{"request", "body", "email"}},
		errorstack.Entry{Message: "Must be set", Path: []any{"request", "body", "name"}},
	)

	gqlErr := errorPresenter(ctx, err)

	require.NotNil(t, gqlErr)
	assert.Equal(t, "Must be a valid email address", gqlErr.Message)
	assert.Equal(t, errorstack.CodeValidationFailed, gqlErr.Extensions["code"])
	assert.Equal(t,
		ast.Path{ast.PathName("request"), ast.PathName("body"), ast.PathName("email")},
		gqlErr.Path,
		"first entry's Path is promoted to spec-level Path",
	)
	_, hasAdditional := gqlErr.Extensions["additional_errors"]
	assert.False(t, hasAdditional, "entries[1:] are fanned out via gqlgen.AddError, not aggregated")

	collected := getErrors()
	require.Len(t, collected, 1, "errorPresenter fans only entries[1:] into the collector")
	assert.Equal(t, "Must be set", collected[0].Message)
	assert.Equal(t,
		ast.Path{ast.PathName("request"), ast.PathName("body"), ast.PathName("name")},
		collected[0].Path,
	)
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

// errorListContext wraps a context with a minimal gqlgen error collector so
// AddErrors can fan out without a full gqlgen request. Returns the context
// and a getter for the collected errors.
func errorListContext() (context.Context, func() gqlerror.List) {
	var errs gqlerror.List
	ctx := gqlgen.WithOperationContext(context.Background(), &gqlgen.OperationContext{
		ResolverMiddleware: func(ctx context.Context, next gqlgen.Resolver) (any, error) {
			return next(ctx)
		},
	})
	ctx = gqlgen.WithResponseContext(ctx, func(ctx context.Context, err error) *gqlerror.Error {
		return errorPresenter(ctx, err)
	}, func(ctx context.Context, err any) error {
		return nil
	})

	return ctx, func() gqlerror.List {
		// Drain whatever AddErrors emitted into the response context.
		errs = gqlgen.GetErrors(ctx)
		return errs
	}
}

func TestAddErrors_FansOutOnePerEntry(t *testing.T) {
	// M3.2 — AddErrors fans out each entry as its own *gqlerror.Error so
	// the response surfaces every entry as a top-level errors[] element.
	ctx, getErrors := errorListContext()

	err := errorstack.NewValidation(
		errorstack.Entry{Message: "Must be a valid email address", Path: []any{"request", "body", "email"}},
		errorstack.Entry{Message: "Must be set", Path: []any{"request", "body", "name"}},
	)

	AddErrors(ctx, err)

	got := getErrors()
	require.Len(t, got, 2, "AddErrors emits one *gqlerror.Error per entry")
	assert.Equal(t, "Must be a valid email address", got[0].Message)
	assert.Equal(t, ast.Path{ast.PathName("request"), ast.PathName("body"), ast.PathName("email")}, got[0].Path)
	assert.Equal(t, errorstack.CodeValidationFailed, got[0].Extensions["code"])
	_, hasExt := got[0].Extensions["path"]
	assert.False(t, hasExt, "Entry.Path must not leak into extensions.path")
	_, hasAdditional := got[0].Extensions["additional_errors"]
	assert.False(t, hasAdditional, "AddErrors path emits per-entry; no additional_errors aggregation")

	assert.Equal(t, "Must be set", got[1].Message)
	assert.Equal(t, ast.Path{ast.PathName("request"), ast.PathName("body"), ast.PathName("name")}, got[1].Path)
}

func TestAddErrors_EntryWithoutPath_FallsBackToResolverPath(t *testing.T) {
	// When the entry has no Path, AddErrors falls back to gqlgen's
	// resolver-derived path. With Background context (no resolver), that's
	// nil — verify behavior is consistent.
	ctx, getErrors := errorListContext()

	err := errorstack.New("Internal failure", errorstack.WithCode(errorstack.CodeInternalError))

	AddErrors(ctx, err)

	got := getErrors()
	require.Len(t, got, 1)
	assert.Equal(t, "Internal failure", got[0].Message)
	assert.Nil(t, got[0].Path, "without a resolver path or entry path, Path stays nil")
}

func TestAddErrors_NilError_NoOp(t *testing.T) {
	ctx, getErrors := errorListContext()

	AddErrors(ctx, nil)

	assert.Empty(t, getErrors())
}
