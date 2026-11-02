package graphql

import (
	"context"
	"strings"

	gqlgen "github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// codeDepthLimitExceeded is the extensions.code of an operation refused for its
// depth. It is spelled like gqlgen's own COMPLEXITY_LIMIT_EXCEEDED, the refusal
// it sits next to.
const codeDepthLimitExceeded = "DEPTH_LIMIT_EXCEEDED"

// Ensure depthLimit satisfies the gqlgen hooks it claims.
var (
	_ gqlgen.HandlerExtension        = depthLimit{}
	_ gqlgen.OperationContextMutator = depthLimit{}
)

// depthLimit is the gqlgen extension that refuses an operation whose selections
// nest deeper than limit, before any of it executes. gqlgen ships a complexity
// limit but no depth limit, and complexity alone does not catch a deep document:
// under the default cost of one per field, a hundred levels of a self-referencing
// type cost a hundred.
type depthLimit struct {
	limit int
}

// ExtensionName identifies the extension in gqlgen's own stats and logging.
func (d depthLimit) ExtensionName() string {
	return "helix.go/depth-limit"
}

// Validate has nothing to check: Config.sanitize already refused a non-positive
// limit.
func (d depthLimit) Validate(_ gqlgen.ExecutableSchema) error {
	return nil
}

// MutateOperationContext refuses the operation when it is too deep. gqlgen runs
// it once the document has parsed and validated, so fragment spreads are
// resolved and free of cycles.
func (d depthLimit) MutateOperationContext(_ context.Context, opCtx *gqlgen.OperationContext) *gqlerror.Error {
	if opCtx.Operation == nil || !exceedsDepth(opCtx.Operation.SelectionSet, 1, d.limit) {
		return nil
	}

	err := gqlerror.Errorf("Operation exceeds the maximum depth of %d", d.limit)
	errcode.Set(err, codeDepthLimitExceeded)

	return err
}

// exceedsDepth reports whether set, whose fields sit at depth, nests deeper than
// limit. It stops descending as soon as the answer is known, so a document many
// times deeper than the limit costs no more than one just past it.
//
// Introspection fields are skipped: gqlparser bounds their depth with a rule of
// its own, and the introspection query every GraphQL IDE sends nests deeper than
// most applications ever need their own operations to.
func exceedsDepth(set ast.SelectionSet, depth, limit int) bool {
	for _, selection := range set {
		switch s := selection.(type) {
		case *ast.Field:
			if strings.HasPrefix(s.Name, "__") {
				continue
			}

			if depth > limit {
				return true
			}

			if exceedsDepth(s.SelectionSet, depth+1, limit) {
				return true
			}

		case *ast.InlineFragment:
			if exceedsDepth(s.SelectionSet, depth, limit) {
				return true
			}

		case *ast.FragmentSpread:
			if s.Definition != nil && exceedsDepth(s.Definition.SelectionSet, depth, limit) {
				return true
			}
		}
	}

	return false
}
