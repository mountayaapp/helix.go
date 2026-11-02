package graphql

import (
	"context"

	gqlgen "github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// Cache stores the queries of Automatic Persisted Queries, keyed by the SHA-256
// hash of their text.
//
// It is the method set of gqlgen's graphql.Cache[string], so gqlgen's in-process
// lru.New[string](size) satisfies it as is. The valkey integration provides one
// shared by every replica of a service: valkey.NewQueryCache.
//
// Get and Add are called concurrently, by every request carrying a persisted
// query. Neither can report a failure, and neither needs to: a failed Get is a
// miss, which makes the client send the full query again, and a failed Add costs
// one such round trip later. An implementation backed by a remote store logs its
// own failures, which would otherwise go unnoticed.
type Cache interface {

	// Get returns the query stored under key, and whether there was one.
	Get(ctx context.Context, key string) (string, bool)

	// Add stores query under key.
	Add(ctx context.Context, key, query string)
}

// apqPendingKey is the operation stats extension a held-back write is kept
// under, between apqCache.Add and apqCommit.
const apqPendingKey = "helix.go/apq"

// apqPending is a write apqCache held back.
type apqPending struct {
	key   string
	query string
}

// apqCache is the Cache gqlgen's APQ extension is given.
//
// The extension stores a query the moment a client sends it with its hash —
// before the query has been parsed, let alone validated — so any client could
// fill the store with whatever it liked. apqCache holds the write back instead,
// for apqCommit to make once the operation is known to be one worth keeping.
type apqCache struct {
	cache          Cache
	maxQueryLength int
}

// Get reads through to the configured Cache.
func (c apqCache) Get(ctx context.Context, key string) (string, bool) {
	return c.cache.Get(ctx, key)
}

// Add holds the write back for apqCommit. A query longer than the configured
// maximum is never stored: it is still executed, and its client is merely asked
// for the full text again the next time it sends the hash alone.
func (c apqCache) Add(ctx context.Context, key, query string) {
	if len(query) > c.maxQueryLength || !gqlgen.HasOperationContext(ctx) {
		return
	}

	gqlgen.GetOperationContext(ctx).Stats.SetExtension(apqPendingKey, apqPending{key: key, query: query})
}

// Ensure apqCommit satisfies the gqlgen hooks it claims.
var (
	_ gqlgen.HandlerExtension        = apqCommit{}
	_ gqlgen.OperationContextMutator = apqCommit{}
)

// apqCommit is the gqlgen extension that makes the write apqCache held back.
//
// gqlgen runs the operation context mutators once the document has parsed and
// validated, in the order they were registered, and stops at the first one that
// refuses the operation. Registered after the depth and complexity limits, this
// one is reached only by an operation that passed every check.
type apqCommit struct {
	cache Cache
}

// ExtensionName identifies the extension in gqlgen's own stats and logging.
func (c apqCommit) ExtensionName() string {
	return "helix.go/apq"
}

// Validate has nothing to check: Config.sanitize already refused a nil Cache.
func (c apqCommit) Validate(_ gqlgen.ExecutableSchema) error {
	return nil
}

// MutateOperationContext stores the query apqCache held back, if any.
func (c apqCommit) MutateOperationContext(ctx context.Context, opCtx *gqlgen.OperationContext) *gqlerror.Error {
	if pending, ok := opCtx.Stats.GetExtension(apqPendingKey).(apqPending); ok {
		c.cache.Add(ctx, pending.key, pending.query)
	}

	return nil
}
