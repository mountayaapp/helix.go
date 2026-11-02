package graphql

import (
	"testing"

	gqlgen "github.com/99designs/gqlgen/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/validator/rules"
)

// operation parses and validates document against the test schema, as gqlgen
// does before the depth limit runs.
func operation(t *testing.T, document string) *gqlgen.OperationContext {
	t.Helper()

	schema := gqlparser.MustLoadSchema(&ast.Source{Name: "test", Input: testSDL})
	doc, errs := gqlparser.LoadQueryWithRules(schema, document, rules.NewDefaultRules())
	require.Empty(t, errs)

	return &gqlgen.OperationContext{Doc: doc, Operation: doc.Operations[0]}
}

func TestDepthLimit(t *testing.T) {
	testcases := []struct {
		name     string
		document string
		limit    int
		refused  bool
	}{
		{name: "a root field is at depth one", document: "{ name }", limit: 1},
		{name: "at the limit", document: "{ thing { child { name } } }", limit: 3},
		{name: "past the limit", document: "{ thing { child { name } } }", limit: 2, refused: true},
		{name: "the deepest branch counts", document: "{ name thing { child { child { name } } } }", limit: 3, refused: true},
		{
			name:     "inline fragments do not add a level",
			document: "{ thing { ... on Thing { child { name } } } }",
			limit:    3,
		},
		{
			name:     "fragment spreads are followed",
			document: "{ thing { ...F } } fragment F on Thing { child { child { name } } }",
			limit:    3,
			refused:  true,
		},
		{
			name:     "introspection is not counted",
			document: "{ __schema { types { fields { type { ofType { ofType { name } } } } } } }",
			limit:    1,
		},
		{
			name:     "__typename is not counted",
			document: "{ thing { __typename } }",
			limit:    1,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			err := depthLimit{limit: tc.limit}.MutateOperationContext(t.Context(), operation(t, tc.document))

			if !tc.refused {
				assert.Nil(t, err)
				return
			}

			require.NotNil(t, err)
			assert.Equal(t, codeDepthLimitExceeded, err.Extensions["code"])
		})
	}
}
