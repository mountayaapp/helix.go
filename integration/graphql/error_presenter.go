package graphql

import (
	"context"
	"errors"
	"maps"
	"net/http"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/internal/locales"

	gqlgen "github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// extensionInputPath is the per-error extension an entry's Path is rendered
// under. The spec reserves errors[].path for the path of the response field that
// errored, and an entry's Path names something else: the part of the request —
// an input object's field, typically — that was wrong.
const extensionInputPath = "input_path"

// errorPresenter is the gqlgen error presenter wired in graphql.New. It decides
// what of an error reaches the client:
//
//   - An *errorstack.Error in the chain is rendered entry by entry, each as its
//     own element of the spec errors[] array, keeping the wire shape consistent
//     with REST. Its messages are written for the client by construction.
//   - A *gqlerror.Error wrapping nothing is passed through. That is what gqlgen
//     itself builds for a parse or validation failure or a refused limit, and
//     what a resolver builds when it means its message for the client. One that
//     refused the request before execution and carries no code is given
//     BAD_REQUEST.
//   - Anything else — a driver error, a wrapped fmt.Errorf — was written for a
//     log, and routinely names hosts, users and tables. It is masked as an
//     internal error. The error itself is kept on the presented one, where it
//     is never serialized, for the operation span to record.
//
// An entry's Path goes in extensions.input_path, and errors[].path stays the
// resolver path gqlgen derived. Per-entry extensions are copied onto each error.
//
// Multi-entry behavior: returning a multi-entry *errorstack.Error from a
// resolver surfaces entries[0] as the returned *gqlerror.Error and fans
// entries[1:] into gqlgen's error collector via gqlgen.AddError, so the
// canonical errors[] array always has one element per Entry — no
// additional_errors aggregation.
//
// Top-level *errorstack.Error.Extensions are forwarded onto the response-level
// extensions map via gqlgen.RegisterExtension, mirroring how REST surfaces
// err.SetExtension(k, v) at the response top level. A key already registered on
// the response keeps its first value.
func errorPresenter(ctx context.Context, err error) *gqlerror.Error {
	if err == nil {
		return nil
	}

	path, locations := position(ctx, err)

	if stack, ok := errors.AsType[*errorstack.Error](err); ok && len(stack.Entries) > 0 {
		first := stack.Entries[0]
		presented := &gqlerror.Error{
			Message:    first.Message,
			Path:       path,
			Locations:  locations,
			Extensions: entryExtensions(first),
			Err:        cause(err),
		}

		for _, entry := range stack.Entries[1:] {
			gqlgen.AddError(ctx, &gqlerror.Error{
				Message:    entry.Message,
				Path:       path,
				Locations:  locations,
				Extensions: entryExtensions(entry),
			})
		}

		forwardTopLevelExtensions(ctx, stack.Extensions)

		return presented
	}

	if gqlErr, ok := errors.AsType[*gqlerror.Error](err); ok && gqlErr.Err == nil {

		// Outside a field, an error refuses the request before anything executed.
		// gqlgen gives most of those a code, but not the ones its APQ extension
		// returns for a malformed persisted query, and a client switching on
		// extensions.code should not have to special-case them.
		if _, ok := gqlErr.Extensions["code"]; !ok && gqlgen.GetFieldContext(ctx) == nil {
			errcode.Set(gqlErr, errorstack.CodeBadRequest)
		}

		return gqlErr
	}

	return &gqlerror.Error{
		Message:    internalErrorMessage(ctx),
		Path:       path,
		Locations:  locations,
		Extensions: map[string]any{"code": errorstack.CodeInternalError},
		Err:        cause(err),
	}
}

// position returns where in the response err belongs. gqlgen hands the presenter
// a resolver's error already wrapped in a *gqlerror.Error carrying the field's
// path and location; anything presented outside a resolver falls back to the
// path of ctx, which is empty there.
func position(ctx context.Context, err error) (ast.Path, []gqlerror.Location) {
	if gqlErr, ok := errors.AsType[*gqlerror.Error](err); ok {
		return gqlErr.Path, gqlErr.Locations
	}

	return gqlgen.GetPath(ctx), nil
}

// cause returns the error a resolver actually returned, stripped of the
// *gqlerror.Error gqlgen wraps it in to carry a path. It is what the operation
// span records, and gqlgen's wrapper adds nothing to it but noise.
func cause(err error) error {
	if gqlErr, ok := errors.AsType[*gqlerror.Error](err); ok && gqlErr.Err != nil {
		return gqlErr.Err
	}

	return err
}

// entryExtensions returns the extensions an entry is rendered with: its own, and
// its Path under extensions.input_path. The map is a copy: the entry's own is
// still the caller's, and may be shared with other entries or errors.
func entryExtensions(entry errorstack.Entry) map[string]any {
	ext := make(map[string]any, len(entry.Extensions)+1)
	maps.Copy(ext, entry.Extensions)

	if len(entry.Path) > 0 {
		ext[extensionInputPath] = entry.Path
	}

	return ext
}

// internalErrorMessage returns the message a masked error is presented with,
// localized from the request's Accept-Language like the HTTP-layer errors are.
func internalErrorMessage(ctx context.Context) string {
	var req *http.Request
	if gqlgen.HasOperationContext(ctx) {
		req = &http.Request{Header: gqlgen.GetOperationContext(ctx).Headers}
	}

	return locales.Message(req, http.StatusInternalServerError)
}

// forwardTopLevelExtensions copies an *errorstack.Error's response-level
// extensions onto gqlgen's response-level extensions map via
// gqlgen.RegisterExtension.
//
// Resolvers run concurrently, and so does the presenter, so the map must not be
// read to look for a key before registering it: gqlgen.GetExtensions hands out
// the live map without its lock. Each key is registered on its own instead, and
// gqlgen's panic on a key already registered is recovered — which keeps the
// first value. So is its panic outside a gqlgen response context (a test using
// context.Background, the HTTP-layer error path before the GraphQL execution
// layer), where there is no response to forward onto.
func forwardTopLevelExtensions(ctx context.Context, ext map[string]any) {
	for key, value := range ext {
		registerExtension(ctx, key, value)
	}
}

// registerExtension is gqlgen.RegisterExtension, recovered. See
// forwardTopLevelExtensions.
func registerExtension(ctx context.Context, key string, value any) {
	defer func() { _ = recover() }()

	gqlgen.RegisterExtension(ctx, key, value)
}

// AddErrors fans out err's entries into the gqlgen error collector via
// gqlgen.AddError, producing one *gqlerror.Error per entry in the response.
// This is the canonical path for resolvers to surface multi-entry validation
// errors so each appears as its own element of the spec errors[] array.
//
// Each emitted error carries the resolver's path in errors[].path, and the
// entry's Path, when set, in extensions.input_path. Callers should typically
// return nil (or a sentinel) from the resolver after calling AddErrors to avoid
// double-reporting via the error presenter.
func AddErrors(ctx context.Context, err *errorstack.Error) {
	if err == nil {
		return
	}

	path := gqlgen.GetPath(ctx)
	for _, entry := range err.Entries {
		gqlgen.AddError(ctx, &gqlerror.Error{
			Message:    entry.Message,
			Path:       path,
			Extensions: entryExtensions(entry),
		})
	}
}
