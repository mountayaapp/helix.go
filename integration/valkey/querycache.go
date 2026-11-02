package valkey

import (
	"context"
	"time"

	"github.com/mountayaapp/helix.go/telemetry/log"
)

// Defaults NewQueryCache applies to the fields OptionsQueryCache leaves unset.
const (
	defaultQueryCachePrefix        = "apq:"
	defaultQueryCacheTTL           = time.Hour
	defaultQueryCacheMaxQueryBytes = 64 << 10
)

// OptionsQueryCache is used to configure a QueryCache.
type OptionsQueryCache struct {

	// Prefix is prepended to every key the cache stores, so that its entries can
	// share a database with other data and be told apart from it.
	//
	// Default:
	//
	//   "apq:"
	Prefix string `json:"prefix,omitempty"`

	// TTL is how long a query stays cached after it was last added. Zero or
	// negative applies the default.
	//
	// Default:
	//
	//   1h
	TTL time.Duration `json:"ttl,omitzero"`

	// MaxQueryBytes is the size, in bytes, above which a query is not cached. Any
	// client can have a query of its choosing stored, so this bounds what one
	// request can make the cache hold. A query left uncached still runs: its
	// client is told the hash is unknown and sends the query again in full. Zero
	// or negative applies the default.
	//
	// Default:
	//
	//   65536 (64 KiB)
	MaxQueryBytes int `json:"max_query_bytes,omitzero"`
}

// QueryCache is the Valkey-backed automatic-persisted-query (APQ) cache for the
// graphql integration. It maps the hash a client sends in place of a query to the
// query document it stands for.
//
// Its method set is that of gqlgen's graphql.Cache[string], the interface the
// graphql integration takes an APQ cache as, so a *QueryCache is passed to it
// as-is. Neither package depends on the other for it.
//
// A failure to read or write the cache is never returned — the interface has no
// room for one — and never fatal: APQ degrades to the client sending its query in
// full. It is recorded on the operation's span and logged as a warning instead.
//
// A QueryCache is safe for concurrent use.
type QueryCache struct {
	valkey        Valkey
	prefix        string
	ttl           time.Duration
	maxQueryBytes int
}

// NewQueryCache returns a QueryCache storing its queries in v, which must not be
// nil, with opts' unset fields defaulted.
func NewQueryCache(v Valkey, opts OptionsQueryCache) *QueryCache {
	if opts.Prefix == "" {
		opts.Prefix = defaultQueryCachePrefix
	}

	if opts.TTL <= 0 {
		opts.TTL = defaultQueryCacheTTL
	}

	if opts.MaxQueryBytes <= 0 {
		opts.MaxQueryBytes = defaultQueryCacheMaxQueryBytes
	}

	return &QueryCache{
		valkey:        v,
		prefix:        opts.Prefix,
		ttl:           opts.TTL,
		maxQueryBytes: opts.MaxQueryBytes,
	}
}

// Get returns the query cached under key, and whether there was one.
func (c *QueryCache) Get(ctx context.Context, key string) (string, bool) {
	value, err := c.valkey.Get(ctx, c.prefix+key, nil)
	if err != nil {
		if !IsNotFound(err) {
			log.Warn(ctx, "Failed to read persisted query from Valkey", log.Err(err))
		}

		return "", false
	}

	return string(value), true
}

// Add caches query under key for the cache's TTL, unless the query is larger
// than its MaxQueryBytes.
func (c *QueryCache) Add(ctx context.Context, key, query string) {
	if len(query) > c.maxQueryBytes {
		return
	}

	err := c.valkey.Set(ctx, c.prefix+key, []byte(query), &OptionsSet{TTL: c.ttl})
	if err != nil {
		log.Warn(ctx, "Failed to cache persisted query in Valkey", log.Err(err))
	}
}
