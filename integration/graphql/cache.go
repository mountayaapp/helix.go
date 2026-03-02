package graphql

import (
	"context"
	"time"

	"github.com/mountayaapp/helix.go/integration/valkey"
)

/*
valkeyCache adapts a valkey.Valkey instance to gqlgen's graphql.Cache interface
for Automatic Persisted Queries.
*/
type valkeyCache struct {
	prefix string
	ttl    time.Duration
	client valkey.Valkey
}

/*
Add stores a query string in Valkey keyed by its SHA-256 hash.
*/
func (c *valkeyCache) Add(ctx context.Context, hash string, query string) {
	c.client.Set(ctx, c.prefix+hash, []byte(query), &valkey.OptionsSet{
		TTL: c.ttl,
	})
}

/*
Get retrieves a cached query string from Valkey by its SHA-256 hash.
*/
func (c *valkeyCache) Get(ctx context.Context, hash string) (string, bool) {
	val, err := c.client.Get(ctx, c.prefix+hash, nil)
	if err != nil {
		return "", false
	}

	return string(val), true
}
