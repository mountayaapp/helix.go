package graphql

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"strings"
	"sync"
	"testing"

	"github.com/mountaya/helix.go/errorstack"

	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ensure gqlgen's in-process cache satisfies Cache as is, as documented.
var _ Cache = lru.New[string](1)

// memoryCache is a Cache whose content a test can inspect.
type memoryCache struct {
	mu      sync.Mutex
	queries map[string]string
}

func (c *memoryCache) Get(_ context.Context, key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	query, ok := c.queries[key]
	return query, ok
}

func (c *memoryCache) Add(_ context.Context, key, query string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.queries == nil {
		c.queries = map[string]string{}
	}

	c.queries[key] = query
}

// len returns how many queries the cache holds.
func (c *memoryCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.queries)
}

// persisted encodes an APQ request body: the hash of document, with document
// itself when withQuery is set.
func persisted(document string, withQuery bool) string {
	sum := sha256.Sum256([]byte(document))

	body := map[string]any{
		"extensions": map[string]any{
			"persistedQuery": map[string]any{"version": 1, "sha256Hash": hex.EncodeToString(sum[:])},
		},
	}
	if withQuery {
		body["query"] = document
	}

	b, _ := json.Marshal(body)
	return string(b)
}

func TestAPQ(t *testing.T) {
	newHandler := func(t *testing.T, maxQueryLength int) (*memoryCache, func(body string) response) {
		cache := &memoryCache{}
		cfg := testConfig(map[string]resolverFunc{"name": constant("helix")})
		cfg.DepthLimit = 2
		cfg.APQ = ConfigAPQ{Enabled: true, Cache: cache, MaxQueryLength: maxQueryLength}
		h, _ := newTestHandler(t, cfg)

		return cache, func(body string) response {
			return decode(t, postGraphQL(h, body))
		}
	}

	t.Run("a valid query is stored and served from its hash", func(t *testing.T) {
		cache, post := newHandler(t, 0)

		assert.Empty(t, post(persisted("{ name }", true)).Errors)
		assert.Equal(t, 1, cache.len())

		resp := post(persisted("{ name }", false))
		assert.Empty(t, resp.Errors)
		assert.Equal(t, "helix", resp.Data["name"])
	})

	t.Run("an unknown hash asks for the query", func(t *testing.T) {
		_, post := newHandler(t, 0)

		resp := post(persisted("{ name }", false))

		require.Len(t, resp.Errors, 1)
		assert.Equal(t, "PERSISTED_QUERY_NOT_FOUND", resp.Errors[0].Extensions["code"])
	})

	// GQL-5: gqlgen's extension stores a query before parsing it, so any client
	// could fill the shared store with whatever it liked.
	for name, document := range map[string]string{
		"a query that does not parse is not stored":    "{ name",
		"a query that does not validate is not stored": "{ unknown }",
		"a query past the depth limit is not stored":   "{ thing { child { name } } }",
	} {
		t.Run(name, func(t *testing.T) {
			cache, post := newHandler(t, 0)

			assert.NotEmpty(t, post(persisted(document, true)).Errors)
			assert.Zero(t, cache.len())
		})
	}

	// gqlgen's APQ extension refuses a malformed persisted query with an error
	// carrying no code.
	t.Run("a hash that does not match the query is a bad request", func(t *testing.T) {
		cache, post := newHandler(t, 0)
		body := strings.Replace(persisted("{ name }", true), `"{ name }"`, `"{ other }"`, 1)

		resp := post(body)

		require.Len(t, resp.Errors, 1)
		assert.Equal(t, "provided APQ hash does not match query", resp.Errors[0].Message)
		assert.Equal(t, errorstack.CodeBadRequest, resp.Errors[0].Extensions["code"])
		assert.Zero(t, cache.len())
	})

	t.Run("a query longer than the maximum is executed but not stored", func(t *testing.T) {
		cache, post := newHandler(t, 16)
		document := "{ name" + strings.Repeat(" ", 16) + "}"

		resp := post(persisted(document, true))

		assert.Empty(t, resp.Errors)
		assert.Equal(t, "helix", resp.Data["name"])
		assert.Zero(t, cache.len())
	})
}
