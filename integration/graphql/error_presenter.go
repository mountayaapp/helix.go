package graphql

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/mountayaapp/helix.go/errorstack"

	gqlgen "github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

/*
errorPresenter is the gqlgen error presenter wired in graphql.New. It detects
*errorstack.Error in the chain and renders every entry as its own element of
the spec errors[] array, keeping the wire shape consistent with REST.

Path resolution: when an entry carries a non-empty Path, it is promoted to
the spec-level errors[].path slot (overriding the gqlgen-derived resolver
path). The gqlgen-derived resolver path remains the fallback when an entry
has no Path. Per-entry extensions are copied onto each gqlErr.Extensions.

Multi-entry behavior: returning a multi-entry *errorstack.Error from a
resolver surfaces entries[0] as the returned *gqlerror.Error and fans
entries[1:] into gqlgen's error collector via gqlgen.AddError, so the
canonical errors[] array always has one element per Entry — no
additional_errors aggregation.

Top-level *errorstack.Error.Extensions are forwarded onto the response-level
extensions map via gqlgen.RegisterExtension, mirroring how REST surfaces
err.SetExtension(k, v) at the response top level. Duplicate keys already
registered on the response are skipped to avoid gqlgen's panic.
*/
func errorPresenter(ctx context.Context, err error) *gqlerror.Error {
	gqlErr := gqlgen.DefaultErrorPresenter(ctx, err)

	var stack *errorstack.Error
	if !errors.As(err, &stack) || len(stack.Entries) == 0 {
		return gqlErr
	}

	resolverPath := gqlErr.Path

	first := stack.Entries[0]
	gqlErr.Message = first.Message

	if len(first.Path) > 0 {
		gqlErr.Path = entryPathToASTPath(first.Path)
	}

	if gqlErr.Extensions == nil {
		gqlErr.Extensions = map[string]any{}
	}

	maps.Copy(gqlErr.Extensions, first.Extensions)

	for _, entry := range stack.Entries[1:] {
		extra := &gqlerror.Error{
			Message:    entry.Message,
			Extensions: map[string]any{},
		}

		if len(entry.Path) > 0 {
			extra.Path = entryPathToASTPath(entry.Path)
		} else {
			extra.Path = resolverPath
		}

		maps.Copy(extra.Extensions, entry.Extensions)

		gqlgen.AddError(ctx, extra)
	}

	forwardTopLevelExtensions(ctx, stack.Extensions)

	return gqlErr
}

/*
forwardTopLevelExtensions copies an *errorstack.Error's response-level
extensions onto gqlgen's response-level extensions map via
gqlgen.RegisterExtension. Duplicate keys are skipped (gqlgen panics on
re-register). When called outside a gqlgen response context (e.g. from a
test using context.Background or from the HTTP-layer error path before
the GraphQL execution layer), the forwarding is silently skipped — the
caller did not opt into response-context surfacing.
*/
func forwardTopLevelExtensions(ctx context.Context, ext map[string]any) {
	if len(ext) == 0 {
		return
	}

	defer func() { _ = recover() }()

	existing := gqlgen.GetExtensions(ctx)
	for key, value := range ext {
		if _, dup := existing[key]; dup {
			continue
		}
		gqlgen.RegisterExtension(ctx, key, value)
	}
}

/*
AddErrors fans out err's entries into the gqlgen error collector via
gqlgen.AddError, producing one *gqlerror.Error per entry in the response.
This is the canonical path for resolvers to surface multi-entry validation
errors so each appears as its own element of the spec errors[] array.

Each emitted gqlerror's spec-level Path is the entry's Path when set, falling
back to the gqlgen-derived resolver path when the entry has none. Callers
should typically return nil (or a sentinel) from the resolver after calling
AddErrors to avoid double-reporting via errorPresenter.
*/
func AddErrors(ctx context.Context, err *errorstack.Error) {
	if err == nil {
		return
	}

	resolverPath := gqlgen.GetPath(ctx)
	for _, entry := range err.Entries {
		gqlErr := &gqlerror.Error{
			Message:    entry.Message,
			Extensions: map[string]any{},
		}

		if len(entry.Path) > 0 {
			gqlErr.Path = entryPathToASTPath(entry.Path)
		} else {
			gqlErr.Path = resolverPath
		}

		maps.Copy(gqlErr.Extensions, entry.Extensions)

		gqlgen.AddError(ctx, gqlErr)
	}
}

/*
entryPathToASTPath converts an errorstack.Entry.Path ([]any of strings and
ints) into gqlparser's ast.Path representation (PathName for strings,
PathIndex for ints, fmt.Sprintf fallback for any other type). Returns nil for
empty input.
*/
func entryPathToASTPath(segments []any) ast.Path {
	if len(segments) == 0 {
		return nil
	}

	out := make(ast.Path, 0, len(segments))
	for _, segment := range segments {
		switch v := segment.(type) {
		case string:
			out = append(out, ast.PathName(v))
		case int:
			out = append(out, ast.PathIndex(v))
		case int8:
			out = append(out, ast.PathIndex(int(v)))
		case int16:
			out = append(out, ast.PathIndex(int(v)))
		case int32:
			out = append(out, ast.PathIndex(int(v)))
		case int64:
			out = append(out, ast.PathIndex(int(v)))
		case uint:
			out = append(out, ast.PathIndex(int(v)))
		case uint8:
			out = append(out, ast.PathIndex(int(v)))
		case uint16:
			out = append(out, ast.PathIndex(int(v)))
		case uint32:
			out = append(out, ast.PathIndex(int(v)))
		case uint64:
			out = append(out, ast.PathIndex(int(v)))
		default:
			out = append(out, ast.PathName(fmt.Sprintf("%v", v)))
		}
	}

	return out
}
