package valkey

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mountaya/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	otellog "go.opentelemetry.io/otel/log"
)

// The method set the graphql integration takes an APQ cache as: gqlgen's
// graphql.Cache[string], restated rather than imported so that this module does
// not depend on gqlgen. A change to either side shows up here.
var _ interface {
	Get(ctx context.Context, key string) (value string, ok bool)
	Add(ctx context.Context, key string, value string)
} = (*QueryCache)(nil)

// failingValkey is a Valkey whose Get and Set fail with err. Its other methods
// are not implemented and panic if called.
type failingValkey struct {
	Valkey
	err error
}

// Get fails with err.
func (v failingValkey) Get(context.Context, string, *OptionsGet) ([]byte, error) {
	return nil, v.err
}

// Set fails with err.
func (v failingValkey) Set(context.Context, string, []byte, *OptionsSet) error {
	return v.err
}

func TestNewQueryCache_Defaults(t *testing.T) {
	cache := NewQueryCache(failingValkey{}, OptionsQueryCache{TTL: -time.Second, MaxQueryBytes: -1})

	assert.Equal(t, "apq:", cache.prefix)
	assert.Equal(t, time.Hour, cache.ttl)
	assert.Equal(t, 64<<10, cache.maxQueryBytes)

	cache = NewQueryCache(failingValkey{}, OptionsQueryCache{Prefix: "q:", TTL: time.Minute, MaxQueryBytes: 10})

	assert.Equal(t, "q:", cache.prefix)
	assert.Equal(t, time.Minute, cache.ttl)
	assert.Equal(t, 10, cache.maxQueryBytes)
}

func TestQueryCache(t *testing.T) {
	conn, _ := connectTest(t, Config{})
	ctx := t.Context()

	cache := NewQueryCache(conn, OptionsQueryCache{TTL: time.Minute, MaxQueryBytes: 32})

	t.Run("an unknown hash misses", func(t *testing.T) {
		query, ok := cache.Get(ctx, "unknown")

		assert.False(t, ok)
		assert.Empty(t, query)
	})

	t.Run("an added query is found, under the prefix and for the TTL", func(t *testing.T) {
		cache.Add(ctx, "hash", "{ viewer { id } }")

		query, ok := cache.Get(ctx, "hash")
		assert.True(t, ok)
		assert.Equal(t, "{ viewer { id } }", query)

		ms := pttl(t, conn, "apq:hash")
		assert.Positive(t, ms)
		assert.LessOrEqual(t, ms, time.Minute.Milliseconds())
	})

	t.Run("a query over MaxQueryBytes is not cached", func(t *testing.T) {
		cache.Add(ctx, "large", strings.Repeat("x", 33))

		_, ok := cache.Get(ctx, "large")
		assert.False(t, ok)

		exists, err := conn.Exists(ctx, "apq:large")
		require.NoError(t, err)
		assert.False(t, exists)
	})
}

// TestQueryCache_FailuresAreLogged pins that a failing Valkey degrades the cache
// to a miss, logged, rather than a panic or a silent nothing.
func TestQueryCache_FailuresAreLogged(t *testing.T) {
	_, rec := servicetest.New(t)
	cache := NewQueryCache(failingValkey{err: errors.New("connection refused")}, OptionsQueryCache{})

	cache.Add(t.Context(), "hash", "{ viewer { id } }")
	_, ok := cache.Get(t.Context(), "hash")
	assert.False(t, ok)

	var warnings []string
	for _, record := range rec.Records() {
		if record.Severity() == otellog.SeverityWarn {
			warnings = append(warnings, record.Body().AsString())
		}
	}

	assert.Equal(t, []string{
		"Failed to cache persisted query in Valkey",
		"Failed to read persisted query from Valkey",
	}, warnings)
}

// TestQueryCache_MissIsNotLogged pins that a hash the cache has never seen — the
// first request for every query — is not worth a warning.
func TestQueryCache_MissIsNotLogged(t *testing.T) {
	_, rec := servicetest.New(t)
	cache := NewQueryCache(failingValkey{err: ErrNotFound}, OptionsQueryCache{})

	_, ok := cache.Get(t.Context(), "hash")
	assert.False(t, ok)
	assert.Empty(t, rec.Records())
}
