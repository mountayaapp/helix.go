package valkey

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	valkeycontainer "github.com/testcontainers/testcontainers-go/modules/valkey"
	"github.com/valkey-io/valkey-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// startValkey brings up a Valkey server for the duration of the test and returns
// its host:port.
//
// A real server rather than a stub: what this package emits depends on which
// commands actually reach the wire and whether they fail, and a stub would let
// a test pass while describing something the integration never does.
//
// Call it *before* servicetest.New, and do not touch the container afterwards.
// testcontainers-go instruments its own Docker API calls with otelhttp against
// the global tracer provider, which servicetest installs; starting the container
// while the Recorder is live puts several dozen Docker API spans in the middle of
// what the test records.
func startValkey(tb testing.TB) string {
	tb.Helper()

	container, err := valkeycontainer.Run(tb.Context(), "valkey/valkey:8-alpine")
	testcontainers.CleanupContainer(tb, container)
	require.NoError(tb, err)

	raw, err := container.ConnectionString(tb.Context())
	require.NoError(tb, err)

	parsed, err := url.Parse(raw)
	require.NoError(tb, err)

	return parsed.Host
}

// connectTest starts a Valkey server and a recording Service, and connects the
// first to the second with cfg, whose Address it fills in.
func connectTest(t *testing.T, cfg Config) (*connection, *servicetest.Recorder) {
	t.Helper()

	cfg.Address = startValkey(t)
	svc, rec := servicetest.New(t)

	v, err := Connect(t.Context(), svc, cfg)
	require.NoError(t, err)

	return v.(*connection), rec
}

// spansNamed returns the ended spans called name, in the order they ended.
func spansNamed(rec *servicetest.Recorder, name string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, span := range rec.Spans() {
		if span.Name() == name {
			out = append(out, span)
		}
	}

	return out
}

// lastSpan returns the last ended span called name, failing the test if there is
// none.
func lastSpan(t *testing.T, rec *servicetest.Recorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()

	spans := spansNamed(rec, name)
	require.NotEmpty(t, spans, "no span named %q was recorded", name)

	return spans[len(spans)-1]
}

// attributeOf returns the value of the attribute key on span, and whether it was
// set.
func attributeOf(span sdktrace.ReadOnlySpan, key attribute.Key) (attribute.Value, bool) {
	for _, attr := range span.Attributes() {
		if attr.Key == key {
			return attr.Value, true
		}
	}

	return attribute.Value{}, false
}

// pttl returns the remaining time to live of key in milliseconds, as the server
// reports it: -1 for a key without expiry, -2 for a missing key.
func pttl(t *testing.T, conn *connection, key string) int64 {
	t.Helper()

	ms, err := conn.client.Do(t.Context(), conn.client.B().Pttl().Key(key).Build()).AsInt64()
	require.NoError(t, err)

	return ms
}

// codeOf returns the extensions.code of the first entry of err.
func codeOf(t *testing.T, err error) string {
	t.Helper()

	entries := errorstack.EntriesOf(err)
	require.NotEmpty(t, entries)

	code, _ := entries[0].Extensions["code"].(string)
	return code
}

// TestValkey_Get pins how Get tells a missing key from a failure, which used to
// be one branch: a Valkey outage produced spans with no recorded error at all.
func TestValkey_Get(t *testing.T) {
	conn, rec := connectTest(t, Config{})
	ctx := t.Context()

	t.Run("missing key returns ErrNotFound without a span error", func(t *testing.T) {
		value, err := conn.Get(ctx, "get:missing", nil)

		assert.Nil(t, value)
		assert.ErrorIs(t, err, ErrNotFound)
		assert.True(t, IsNotFound(err))

		// Code written against the driver's sentinel keeps working.
		assert.True(t, valkey.IsValkeyNil(err))

		assert.Equal(t, codes.Unset, lastSpan(t, rec, "Valkey: Get").Status().Code)
	})

	t.Run("missing key is a span error when asked for", func(t *testing.T) {
		_, err := conn.Get(ctx, "get:missing", &OptionsGet{ErrorRecordOnNotFound: true})

		assert.True(t, IsNotFound(err))
		assert.Equal(t, codes.Error, lastSpan(t, rec, "Valkey: Get").Status().Code)
	})

	t.Run("a failure that is not a missing key is always a span error", func(t *testing.T) {
		require.NoError(t, conn.Do(ctx, conn.B().Lpush().Key("get:list").Element("a").Build()).Error())

		_, err := conn.Get(ctx, "get:list", nil)

		require.Error(t, err)
		assert.False(t, IsNotFound(err))
		assert.Equal(t, codes.Error, lastSpan(t, rec, "Valkey: Get").Status().Code)
	})

	t.Run("the value returned is the caller's own", func(t *testing.T) {
		require.NoError(t, conn.Set(ctx, "get:value", []byte("abc"), nil))

		first, err := conn.Get(ctx, "get:value", nil)
		require.NoError(t, err)
		first[0] = 'x'

		second, err := conn.Get(ctx, "get:value", nil)
		require.NoError(t, err)
		assert.Equal(t, []byte("abc"), second)
	})
}

// TestValkey_MGet pins that an empty value is a value. MGet used to read the
// reply as strings, where a missing key and an empty value are both "", and drop
// both.
func TestValkey_MGet(t *testing.T) {
	conn, _ := connectTest(t, Config{})
	ctx := t.Context()

	require.NoError(t, conn.Set(ctx, "mget:a", []byte("1"), nil))
	require.NoError(t, conn.Set(ctx, "mget:empty", []byte{}, nil))
	require.NoError(t, conn.Set(ctx, "mget:c", []byte("3"), nil))

	entries, err := conn.MGet(ctx, []string{"mget:c", "mget:missing", "mget:empty", "mget:a"})
	require.NoError(t, err)

	assert.Equal(t, []Entry{
		{Key: "mget:c", Value: []byte("3")},
		{Key: "mget:empty", Value: []byte{}},
		{Key: "mget:a", Value: []byte("1")},
	}, entries)

	entries, err = conn.MGet(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// TestValkey_TTL pins the TTL rules. TTLs used to be sent in whole seconds,
// truncated, so a TTL under a second was rejected by the server and 1.5s became
// 1s; and Expire silently turned a TTL of zero or less into one second.
func TestValkey_TTL(t *testing.T) {
	conn, rec := connectTest(t, Config{})
	ctx := t.Context()

	t.Run("Set with a TTL under a second expires the key", func(t *testing.T) {
		require.NoError(t, conn.Set(ctx, "ttl:short", []byte("v"), &OptionsSet{TTL: 500 * time.Microsecond}))

		assert.Eventually(t, func() bool {
			_, err := conn.Get(ctx, "ttl:short", nil)
			return IsNotFound(err)
		}, time.Second, 10*time.Millisecond)
	})

	t.Run("Set keeps millisecond precision", func(t *testing.T) {
		require.NoError(t, conn.Set(ctx, "ttl:precise", []byte("v"), &OptionsSet{TTL: 1500 * time.Millisecond}))

		ms := pttl(t, conn, "ttl:precise")
		assert.Greater(t, ms, int64(1000))
		assert.LessOrEqual(t, ms, int64(1500))
	})

	t.Run("Set with no TTL clears the one the key had", func(t *testing.T) {
		for _, ttl := range []time.Duration{0, -time.Second} {
			require.NoError(t, conn.Set(ctx, "ttl:none", []byte("v"), &OptionsSet{TTL: time.Minute}))
			require.NoError(t, conn.Set(ctx, "ttl:none", []byte("v"), &OptionsSet{TTL: ttl}))

			assert.Equal(t, int64(-1), pttl(t, conn, "ttl:none"), "ttl=%s", ttl)
		}
	})

	t.Run("SetNX takes the same TTL as Set", func(t *testing.T) {
		set, err := conn.SetNX(ctx, "ttl:nx", []byte("v"), &OptionsSet{TTL: 1500 * time.Millisecond})
		require.NoError(t, err)
		assert.True(t, set)

		ms := pttl(t, conn, "ttl:nx")
		assert.Greater(t, ms, int64(1000))
		assert.LessOrEqual(t, ms, int64(1500))

		set, err = conn.SetNX(ctx, "ttl:nx", []byte("w"), &OptionsSet{TTL: time.Minute})
		require.NoError(t, err)
		assert.False(t, set)
		assert.LessOrEqual(t, pttl(t, conn, "ttl:nx"), int64(1500))

		set, err = conn.SetNX(ctx, "ttl:nx-short", []byte("v"), &OptionsSet{TTL: time.Microsecond})
		require.NoError(t, err)
		assert.True(t, set)
	})

	t.Run("Expire keeps millisecond precision", func(t *testing.T) {
		require.NoError(t, conn.Set(ctx, "ttl:expire", []byte("v"), nil))
		require.NoError(t, conn.Expire(ctx, "ttl:expire", 1500*time.Millisecond))

		ms := pttl(t, conn, "ttl:expire")
		assert.Greater(t, ms, int64(1000))
		assert.LessOrEqual(t, ms, int64(1500))

		require.NoError(t, conn.Expire(ctx, "ttl:expire", 500*time.Microsecond))
		assert.Eventually(t, func() bool {
			return pttl(t, conn, "ttl:expire") == -2
		}, time.Second, 10*time.Millisecond)
	})

	t.Run("Expire rejects a TTL of zero or less and leaves the key alone", func(t *testing.T) {
		require.NoError(t, conn.Set(ctx, "ttl:reject", []byte("v"), nil))

		for _, ttl := range []time.Duration{0, -time.Second} {
			err := conn.Expire(ctx, "ttl:reject", ttl)

			require.Error(t, err)
			assert.Equal(t, errorstack.CodeValidationFailed, codeOf(t, err))
			assert.Equal(t, int64(-1), pttl(t, conn, "ttl:reject"), "ttl=%s", ttl)
			assert.Equal(t, codes.Error, lastSpan(t, rec, "Valkey: Expire").Status().Code)
		}
	})
}

// TestValkey_IncrementDecrement pins that both return the value they left, which
// they used to drop.
func TestValkey_IncrementDecrement(t *testing.T) {
	conn, _ := connectTest(t, Config{})
	ctx := t.Context()

	value, err := conn.Increment(ctx, "counter", 5)
	require.NoError(t, err)
	assert.Equal(t, int64(5), value)

	value, err = conn.Decrement(ctx, "counter", 7)
	require.NoError(t, err)
	assert.Equal(t, int64(-2), value)
}

// TestValkey_IncrementTTL pins the counter whose time-to-live is set in the same
// round trip: the increment creating the key sets it, later ones leave it alone,
// and a key left without one gets one on its next increment.
func TestValkey_IncrementTTL(t *testing.T) {
	conn, rec := connectTest(t, Config{})
	ctx := t.Context()

	t.Run("the increment creating the key sets its time-to-live", func(t *testing.T) {
		value, err := conn.IncrementTTL(ctx, "window:create", 3, 1500*time.Millisecond)
		require.NoError(t, err)
		assert.Equal(t, int64(3), value)

		ms := pttl(t, conn, "window:create")
		assert.Greater(t, ms, int64(1000))
		assert.LessOrEqual(t, ms, int64(1500))
	})

	t.Run("later increments do not slide the window", func(t *testing.T) {
		_, err := conn.IncrementTTL(ctx, "window:fixed", 1, 1500*time.Millisecond)
		require.NoError(t, err)

		value, err := conn.IncrementTTL(ctx, "window:fixed", 2, time.Hour)
		require.NoError(t, err)
		assert.Equal(t, int64(3), value)
		assert.LessOrEqual(t, pttl(t, conn, "window:fixed"), int64(1500))
	})

	t.Run("a key without a time-to-live gets one", func(t *testing.T) {
		_, err := conn.Increment(ctx, "window:immortal", 1)
		require.NoError(t, err)
		require.Equal(t, int64(-1), pttl(t, conn, "window:immortal"))

		value, err := conn.IncrementTTL(ctx, "window:immortal", 1, time.Minute)
		require.NoError(t, err)
		assert.Equal(t, int64(2), value)
		assert.Greater(t, pttl(t, conn, "window:immortal"), int64(0))
	})

	t.Run("the window ends with the key", func(t *testing.T) {
		_, err := conn.IncrementTTL(ctx, "window:short", 1, 500*time.Microsecond)
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			return pttl(t, conn, "window:short") == -2
		}, time.Second, 10*time.Millisecond)

		value, err := conn.IncrementTTL(ctx, "window:short", 1, time.Minute)
		require.NoError(t, err)
		assert.Equal(t, int64(1), value, "a new window starts from zero")
	})

	t.Run("a TTL of zero or less is rejected and nothing is sent", func(t *testing.T) {
		for _, ttl := range []time.Duration{0, -time.Second} {
			value, err := conn.IncrementTTL(ctx, "window:reject", 1, ttl)

			require.Error(t, err)
			assert.Zero(t, value)
			assert.Equal(t, errorstack.CodeValidationFailed, codeOf(t, err))
			assert.Equal(t, int64(-2), pttl(t, conn, "window:reject"), "ttl=%s", ttl)
			assert.Equal(t, codes.Error, lastSpan(t, rec, "Valkey: IncrementTTL").Status().Code)
		}
	})

	t.Run("a value that is not an integer fails the call", func(t *testing.T) {
		require.NoError(t, conn.Set(ctx, "window:text", []byte("text"), nil))

		_, err := conn.IncrementTTL(ctx, "window:text", 1, time.Minute)
		require.Error(t, err)
		assert.Equal(t, codes.Error, lastSpan(t, rec, "Valkey: IncrementTTL").Status().Code)
	})
}

// TestValkey_Sets pins the three set commands: adding reports the members that
// were new, a positive TTL is set with every add and refreshes the set's life, no
// TTL leaves its expiry alone, removing the last member deletes the set, a missing
// set reads as empty, and an empty member list sends nothing.
func TestValkey_Sets(t *testing.T) {
	conn, rec := connectTest(t, Config{})
	ctx := t.Context()

	t.Run("adding reports the members that were new", func(t *testing.T) {
		added, err := conn.SAdd(ctx, "set:members", []string{"a", "b"}, nil)
		require.NoError(t, err)
		assert.Equal(t, int64(2), added)

		added, err = conn.SAdd(ctx, "set:members", []string{"b", "c"}, nil)
		require.NoError(t, err)
		assert.Equal(t, int64(1), added)

		members, err := conn.SMembers(ctx, "set:members")
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"a", "b", "c"}, members)
		assert.Equal(t, int64(-1), pttl(t, conn, "set:members"), "no TTL asked, none set")
	})

	t.Run("a TTL is set with every add, and refreshed by the next", func(t *testing.T) {
		_, err := conn.SAdd(ctx, "set:ttl", []string{"a"}, &OptionsSAdd{TTL: 1500 * time.Millisecond})
		require.NoError(t, err)
		assert.LessOrEqual(t, pttl(t, conn, "set:ttl"), int64(1500))

		added, err := conn.SAdd(ctx, "set:ttl", []string{"a"}, &OptionsSAdd{TTL: time.Hour})
		require.NoError(t, err)
		assert.Zero(t, added, "a member already in the set is not new")
		assert.Greater(t, pttl(t, conn, "set:ttl"), int64(time.Minute/time.Millisecond), "a known member still refreshes the set")

		_, err = conn.SAdd(ctx, "set:ttl", []string{"b"}, nil)
		require.NoError(t, err)
		assert.Greater(t, pttl(t, conn, "set:ttl"), int64(time.Minute/time.Millisecond), "an add without a TTL leaves it alone")
	})

	t.Run("the set expires with its TTL", func(t *testing.T) {
		_, err := conn.SAdd(ctx, "set:short", []string{"a"}, &OptionsSAdd{TTL: 500 * time.Microsecond})
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			members, err := conn.SMembers(ctx, "set:short")
			return err == nil && len(members) == 0
		}, time.Second, 10*time.Millisecond)
	})

	t.Run("removing the last member deletes the set", func(t *testing.T) {
		_, err := conn.SAdd(ctx, "set:remove", []string{"a", "b"}, nil)
		require.NoError(t, err)

		removed, err := conn.SRem(ctx, "set:remove", []string{"a", "missing"})
		require.NoError(t, err)
		assert.Equal(t, int64(1), removed)

		removed, err = conn.SRem(ctx, "set:remove", []string{"b"})
		require.NoError(t, err)
		assert.Equal(t, int64(1), removed)
		assert.Equal(t, int64(-2), pttl(t, conn, "set:remove"))
	})

	t.Run("a missing set is empty, not an error", func(t *testing.T) {
		members, err := conn.SMembers(ctx, "set:none")
		require.NoError(t, err)
		assert.NotNil(t, members)
		assert.Empty(t, members)

		removed, err := conn.SRem(ctx, "set:none", []string{"a"})
		require.NoError(t, err)
		assert.Zero(t, removed)
	})

	t.Run("no members sends nothing", func(t *testing.T) {
		added, err := conn.SAdd(ctx, "set:empty", nil, &OptionsSAdd{TTL: time.Minute})
		require.NoError(t, err)
		assert.Zero(t, added)
		assert.Equal(t, int64(-2), pttl(t, conn, "set:empty"))

		removed, err := conn.SRem(ctx, "set:empty", nil)
		require.NoError(t, err)
		assert.Zero(t, removed)
	})

	t.Run("a key that is not a set fails the call", func(t *testing.T) {
		require.NoError(t, conn.Set(ctx, "set:text", []byte("text"), nil))

		_, err := conn.SAdd(ctx, "set:text", []string{"a"}, nil)
		require.Error(t, err)
		assert.Equal(t, codes.Error, lastSpan(t, rec, "Valkey: SAdd").Status().Code)

		_, err = conn.SMembers(ctx, "set:text")
		require.Error(t, err)
		assert.Equal(t, codes.Error, lastSpan(t, rec, "Valkey: SMembers").Status().Code)

		_, err = conn.SRem(ctx, "set:text", []string{"a"})
		require.Error(t, err)
		assert.Equal(t, codes.Error, lastSpan(t, rec, "Valkey: SRem").Status().Code)
	})
}

// TestValkey_SScan pins the set scan: a large set is read a page at a time, the
// pages together holding every member, each page about the size asked; Match keeps
// only the members it matches; SCard counts the members without reading them; a
// small set is one page; a missing set is done at once and counts none; and a key
// that is not a set fails both calls.
func TestValkey_SScan(t *testing.T) {
	conn, rec := connectTest(t, Config{})
	ctx := t.Context()

	// scanAll scans key to the end, and returns every member seen, how many pages
	// it took, and the largest page.
	scanAll := func(t *testing.T, key string, opts *OptionsSScan) (map[string]int, int, int) {
		t.Helper()

		seen := map[string]int{}
		var cursor uint64
		var pages, largest int
		for {
			page, next, err := conn.SScan(ctx, key, cursor, opts)
			require.NoError(t, err)
			require.NotNil(t, page)

			pages++
			largest = max(largest, len(page))
			for _, member := range page {
				seen[member]++
			}

			cursor = next
			if cursor == 0 {
				return seen, pages, largest
			}

			require.Less(t, pages, 10000, "the scan never ended")
		}
	}

	members := make([]string, 0, 5000)
	for i := range 5000 {
		members = append(members, fmt.Sprintf("member:%04d", i))
	}
	_, err := conn.SAdd(ctx, "sscan:large", members, nil)
	require.NoError(t, err)

	t.Run("a large set is read a page at a time, every member at least once", func(t *testing.T) {
		seen, pages, largest := scanAll(t, "sscan:large", &OptionsSScan{Count: 100})

		assert.Len(t, seen, len(members))
		for _, member := range members {
			assert.Positive(t, seen[member], "member %s never returned", member)
		}
		assert.Greater(t, pages, 10, "the set was read in too few pages")
		assert.Less(t, largest, 1000, "a page holds far more than Count")
	})

	t.Run("the default page is about a hundred members", func(t *testing.T) {
		_, pages, largest := scanAll(t, "sscan:large", nil)

		assert.Greater(t, pages, 10)
		assert.Less(t, largest, 1000)
	})

	t.Run("Match keeps only the members it matches", func(t *testing.T) {
		seen, _, _ := scanAll(t, "sscan:large", &OptionsSScan{Match: "member:00*", Count: 500})

		assert.Len(t, seen, 100)
		for member := range seen {
			assert.True(t, strings.HasPrefix(member, "member:00"), member)
		}
	})

	t.Run("SCard counts the members without reading them", func(t *testing.T) {
		count, err := conn.SCard(ctx, "sscan:large")
		require.NoError(t, err)
		assert.Equal(t, int64(len(members)), count)

		count, err = conn.SCard(ctx, "sscan:none")
		require.NoError(t, err)
		assert.Zero(t, count, "a missing set is empty")
	})

	t.Run("a small set is one page", func(t *testing.T) {
		_, err := conn.SAdd(ctx, "sscan:small", []string{"a", "b", "c"}, nil)
		require.NoError(t, err)

		page, next, err := conn.SScan(ctx, "sscan:small", 0, &OptionsSScan{Count: 1})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"a", "b", "c"}, page)
		assert.Zero(t, next)
	})

	t.Run("a missing set is done at once", func(t *testing.T) {
		page, next, err := conn.SScan(ctx, "sscan:none", 0, nil)
		require.NoError(t, err)
		assert.NotNil(t, page)
		assert.Empty(t, page)
		assert.Zero(t, next)
	})

	t.Run("a key that is not a set fails the call", func(t *testing.T) {
		require.NoError(t, conn.Set(ctx, "sscan:text", []byte("text"), nil))

		_, _, err := conn.SScan(ctx, "sscan:text", 0, nil)
		require.Error(t, err)
		assert.Equal(t, codes.Error, lastSpan(t, rec, "Valkey: SScan").Status().Code)

		_, err = conn.SCard(ctx, "sscan:text")
		require.Error(t, err)
		assert.Equal(t, codes.Error, lastSpan(t, rec, "Valkey: SCard").Status().Code)
	})
}

// TestValkey_CompareAndSet pins that a value is only replaced when the key holds
// the one expected, and that the scripts survive a server that forgot them.
func TestValkey_CompareAndSet(t *testing.T) {
	conn, _ := connectTest(t, Config{})
	ctx := t.Context()

	value := func(t *testing.T, key string) []byte {
		t.Helper()

		v, err := conn.Get(ctx, key, nil)
		require.NoError(t, err)

		return v
	}

	t.Run("replaces the value the key holds as expected", func(t *testing.T) {
		require.NoError(t, conn.Set(ctx, "cas:match", []byte("claim-1"), nil))

		set, err := conn.CompareAndSet(ctx, "cas:match", []byte("claim-1"), []byte("answer"), 1500*time.Millisecond)
		require.NoError(t, err)
		assert.True(t, set)
		assert.Equal(t, []byte("answer"), value(t, "cas:match"))

		ms := pttl(t, conn, "cas:match")
		assert.Greater(t, ms, int64(1000))
		assert.LessOrEqual(t, ms, int64(1500))
	})

	t.Run("leaves a key holding anything else alone", func(t *testing.T) {
		require.NoError(t, conn.Set(ctx, "cas:other", []byte("claim-2"), &OptionsSet{TTL: time.Minute}))

		set, err := conn.CompareAndSet(ctx, "cas:other", []byte("claim-1"), []byte("answer"), time.Hour)
		require.NoError(t, err)
		assert.False(t, set)
		assert.Equal(t, []byte("claim-2"), value(t, "cas:other"))
		assert.LessOrEqual(t, pttl(t, conn, "cas:other"), int64(time.Minute/time.Millisecond))
	})

	t.Run("a missing key matches nothing, not even an empty value", func(t *testing.T) {
		for _, expected := range [][]byte{nil, {}, []byte("claim-1")} {
			set, err := conn.CompareAndSet(ctx, "cas:missing", expected, []byte("answer"), time.Minute)
			require.NoError(t, err)
			assert.False(t, set)
			assert.Equal(t, int64(-2), pttl(t, conn, "cas:missing"))
		}
	})

	t.Run("an empty value is a value", func(t *testing.T) {
		require.NoError(t, conn.Set(ctx, "cas:empty", []byte{}, nil))

		set, err := conn.CompareAndSet(ctx, "cas:empty", nil, []byte("answer"), time.Minute)
		require.NoError(t, err)
		assert.True(t, set)
	})

	t.Run("a TTL of zero or less clears the one the key had", func(t *testing.T) {
		for _, ttl := range []time.Duration{0, -time.Second} {
			require.NoError(t, conn.Set(ctx, "cas:forever", []byte("claim"), &OptionsSet{TTL: time.Minute}))

			set, err := conn.CompareAndSet(ctx, "cas:forever", []byte("claim"), []byte("answer"), ttl)
			require.NoError(t, err)
			assert.True(t, set)
			assert.Equal(t, int64(-1), pttl(t, conn, "cas:forever"), "ttl=%s", ttl)
		}
	})

	t.Run("compares and stores binary values byte for byte", func(t *testing.T) {
		claim := []byte{0, 1, 0xff, 0}
		require.NoError(t, conn.Set(ctx, "cas:binary", claim, nil))

		set, err := conn.CompareAndSet(ctx, "cas:binary", []byte{0, 1, 0xff}, []byte("answer"), time.Minute)
		require.NoError(t, err)
		assert.False(t, set, "a prefix of the value is not the value")

		set, err = conn.CompareAndSet(ctx, "cas:binary", claim, []byte{0xfe, 0}, time.Minute)
		require.NoError(t, err)
		assert.True(t, set)
		assert.Equal(t, []byte{0xfe, 0}, value(t, "cas:binary"))
	})

	t.Run("works once the server has forgotten the scripts", func(t *testing.T) {
		require.NoError(t, conn.Do(ctx, conn.B().ScriptFlush().Build()).Error())
		require.NoError(t, conn.Set(ctx, "cas:flushed", []byte("claim"), nil))

		set, err := conn.CompareAndSet(ctx, "cas:flushed", []byte("claim"), []byte("answer"), time.Minute)
		require.NoError(t, err)
		assert.True(t, set)

		require.NoError(t, conn.Do(ctx, conn.B().ScriptFlush().Build()).Error())

		deleted, err := conn.CompareAndDelete(ctx, "cas:flushed", []byte("answer"))
		require.NoError(t, err)
		assert.True(t, deleted)
	})
}

// TestValkey_CompareAndDelete pins that a key is only deleted when it holds the
// value expected.
func TestValkey_CompareAndDelete(t *testing.T) {
	conn, rec := connectTest(t, Config{})
	ctx := t.Context()

	t.Run("deletes the key holding the value expected", func(t *testing.T) {
		require.NoError(t, conn.Set(ctx, "cad:match", []byte("claim-1"), nil))

		deleted, err := conn.CompareAndDelete(ctx, "cad:match", []byte("claim-1"))
		require.NoError(t, err)
		assert.True(t, deleted)
		assert.Equal(t, int64(-2), pttl(t, conn, "cad:match"))
	})

	t.Run("leaves a key holding anything else alone", func(t *testing.T) {
		require.NoError(t, conn.Set(ctx, "cad:other", []byte("claim-2"), nil))

		deleted, err := conn.CompareAndDelete(ctx, "cad:other", []byte("claim-1"))
		require.NoError(t, err)
		assert.False(t, deleted)

		exists, err := conn.Exists(ctx, "cad:other")
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("a missing key is not deleted", func(t *testing.T) {
		deleted, err := conn.CompareAndDelete(ctx, "cad:missing", nil)
		require.NoError(t, err)
		assert.False(t, deleted)
	})

	t.Run("a key that is not a string fails the call", func(t *testing.T) {
		_, err := conn.XAdd(ctx, "cad:stream", 0, map[string]string{"f": "v"})
		require.NoError(t, err)

		_, err = conn.CompareAndDelete(ctx, "cad:stream", []byte("v"))
		require.Error(t, err)
		assert.Equal(t, codes.Error, lastSpan(t, rec, "Valkey: CompareAndDelete").Status().Code)
	})
}

// TestValkey_Scan pins the scan options: a COUNT per round trip, and a Limit that
// stops the scan rather than buffering the whole key space.
func TestValkey_Scan(t *testing.T) {
	conn, _ := connectTest(t, Config{})
	ctx := t.Context()

	var want []string
	for i := range 25 {
		key := "scan:" + strconv.Itoa(i)
		want = append(want, key)
		require.NoError(t, conn.Set(ctx, key, []byte("v"), nil))
	}
	require.NoError(t, conn.Set(ctx, "other", []byte("v"), nil))

	t.Run("returns every match once", func(t *testing.T) {
		keys, err := conn.Scan(ctx, "scan:*", nil)
		require.NoError(t, err)
		assert.ElementsMatch(t, want, keys)
	})

	t.Run("a small count takes more round trips, not fewer keys", func(t *testing.T) {
		keys, err := conn.Scan(ctx, "scan:*", &OptionsScan{Count: 1})
		require.NoError(t, err)
		assert.ElementsMatch(t, want, keys)
	})

	t.Run("limit stops the scan", func(t *testing.T) {
		keys, err := conn.Scan(ctx, "scan:*", &OptionsScan{Count: 1, Limit: 10})
		require.NoError(t, err)

		assert.Len(t, keys, 10)
		assert.Subset(t, want, keys)
	})

	t.Run("an empty pattern matches every key", func(t *testing.T) {
		keys, err := conn.Scan(ctx, "", nil)
		require.NoError(t, err)
		assert.ElementsMatch(t, append(slices.Clone(want), "other"), keys)
	})
}

// TestValkey_Streams pins the stream options, and that an entry's fields are
// stored in an order that does not depend on map iteration.
func TestValkey_Streams(t *testing.T) {
	conn, rec := connectTest(t, Config{})
	ctx := t.Context()

	fields := map[string]string{"c": "3", "a": "1", "d": "4", "b": "2", "e": "5"}
	for range 10 {
		_, err := conn.XAdd(ctx, "stream", 0, fields)
		require.NoError(t, err)
	}

	// Read the raw reply: AsXRange decodes the fields into a map, which would hide
	// the order they were stored in.
	raw, err := conn.Do(ctx, conn.B().Xrange().Key("stream").Start("-").End("+").Build()).ToArray()
	require.NoError(t, err)
	require.Len(t, raw, 10)
	for _, entry := range raw {
		parts, err := entry.ToArray()
		require.NoError(t, err)

		pairs, err := parts[1].AsStrSlice()
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "1", "b", "2", "c", "3", "d", "4", "e", "5"}, pairs)
	}

	t.Run("XRange honours Count", func(t *testing.T) {
		entries, err := conn.XRange(ctx, "stream", "-", "+", &OptionsXRange{Count: 3})
		require.NoError(t, err)

		require.Len(t, entries, 3)
		assert.Equal(t, fields, entries[0].Fields)

		entries, err = conn.XRange(ctx, "stream", "-", "+", nil)
		require.NoError(t, err)
		assert.Len(t, entries, 10)
	})

	t.Run("XAdd rejects an empty entry before sending it", func(t *testing.T) {
		id, err := conn.XAdd(ctx, "stream:empty", 0, nil)

		assert.Empty(t, id)
		assert.Equal(t, errorstack.CodeValidationFailed, codeOf(t, err))
		assert.Equal(t, codes.Error, lastSpan(t, rec, "Valkey: XAdd").Status().Code)

		exists, err := conn.Exists(ctx, "stream:empty")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("XAdd caps the stream", func(t *testing.T) {
		for range 300 {
			_, err := conn.XAdd(ctx, "stream:capped", 10, fields)
			require.NoError(t, err)
		}

		length, err := conn.Do(ctx, conn.B().Xlen().Key("stream:capped").Build()).AsInt64()
		require.NoError(t, err)
		assert.Less(t, length, int64(300))
	})
}

// TestValkey_BuildSetCopiesValue pins that the command Set and SetNX send holds a
// copy of the value. It used to alias the caller's buffer, which the driver's
// writer can still be reading after Do returned on a cancelled context.
func TestValkey_BuildSetCopiesValue(t *testing.T) {
	conn, _ := connectTest(t, Config{})

	testcases := []struct {
		name     string
		opts     *OptionsSet
		nx       bool
		expected []string
	}{
		{
			name:     "plain",
			expected: []string{"SET", "k", "value"},
		},
		{
			name:     "with a TTL under a millisecond",
			opts:     &OptionsSet{TTL: 500 * time.Microsecond},
			expected: []string{"SET", "k", "value", "PX", "1"},
		},
		{
			name:     "if not exists, with a TTL",
			opts:     &OptionsSet{TTL: 1500 * time.Millisecond},
			nx:       true,
			expected: []string{"SET", "k", "value", "NX", "PX", "1500"},
		},
		{
			name:     "if not exists, without expiry",
			opts:     &OptionsSet{TTL: -time.Second},
			nx:       true,
			expected: []string{"SET", "k", "value", "NX"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			value := []byte("value")
			cmd := buildSet(conn.client.B(), "k", value, tc.opts, tc.nx)

			copy(value, "xxxxx")

			assert.Equal(t, tc.expected, cmd.Commands())
		})
	}
}

// TestValkey_Subscribe pins the shape of a subscription's telemetry: a setup
// operation that ends once the server confirms, and a consumer span per message
// that the handler's ctx carries. It used to be one span lasting the whole
// subscription, measured as a single operation, with messages handled outside
// any span.
func TestValkey_Subscribe(t *testing.T) {
	conn, rec := connectTest(t, Config{RecordKeys: true})

	type received struct {
		msg  PubSubMessage
		span oteltrace.SpanContext
	}

	messages := make(chan received, 2)
	subCtx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- conn.Subscribe(subCtx, "events", func(ctx context.Context, msg PubSubMessage) {
			messages <- received{msg: msg, span: oteltrace.SpanContextFromContext(ctx)}
		})
	}()

	// The setup ending is what says the server confirmed the subscription, so a
	// message published from here on is delivered.
	require.Eventually(t, func() bool {
		return len(spansNamed(rec, "Valkey: Subscribe")) == 1
	}, 5*time.Second, 10*time.Millisecond)

	ctx := t.Context()
	require.NoError(t, conn.Publish(ctx, "events", []byte("one")))
	require.NoError(t, conn.Publish(ctx, "events", []byte("two")))

	var got []received
	for range 2 {
		select {
		case r := <-messages:
			got = append(got, r)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "message not received")
		}
	}

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err, "a subscription ended through ctx is not a failure")
	case <-time.After(5 * time.Second):
		require.FailNow(t, "Subscribe did not return after ctx was cancelled")
	}

	assert.Equal(t, PubSubMessage{Channel: "events", Payload: []byte("one")}, got[0].msg)
	assert.Equal(t, PubSubMessage{Channel: "events", Payload: []byte("two")}, got[1].msg)

	setup := spansNamed(rec, "Valkey: Subscribe")
	require.Len(t, setup, 1, "the setup is one operation, however long the subscription lasts")
	assert.Equal(t, oteltrace.SpanKindClient, setup[0].SpanKind())
	assert.Equal(t, codes.Unset, setup[0].Status().Code)

	receives := spansNamed(rec, "Valkey: Receive")
	require.Len(t, receives, 2)
	for i, span := range receives {
		assert.Equal(t, oteltrace.SpanKindConsumer, span.SpanKind())
		assert.Equal(t, span.SpanContext().SpanID(), got[i].span.SpanID(), "the handler's ctx carries its message's span")
		assert.NotEqual(t, setup[0].SpanContext().SpanID(), span.Parent().SpanID(), "a message is not a child of the setup")

		channel, ok := attributeOf(span, attrKeyChannel)
		assert.True(t, ok)
		assert.Equal(t, "events", channel.AsString())
	}

	publish := lastSpan(t, rec, "Valkey: Publish")
	assert.Equal(t, oteltrace.SpanKindProducer, publish.SpanKind())

	operations := operationPoints(t, rec)
	assert.Contains(t, operations, "Subscribe/ok")
	assert.NotContains(t, operations, "Receive/ok", "handling a message is the application's time, not Valkey's")
}

// TestValkey_Do pins the escape hatch: a traced command of the caller's own, whose
// nil reply is not a failure.
func TestValkey_Do(t *testing.T) {
	conn, rec := connectTest(t, Config{})
	ctx := t.Context()

	require.NoError(t, conn.Do(ctx, conn.B().Hset().Key("hash").FieldValue().FieldValue("f", "v").Build()).Error())

	value, err := conn.Do(ctx, conn.B().Hget().Key("hash").Field("f").Build()).ToString()
	require.NoError(t, err)
	assert.Equal(t, "v", value)

	span := lastSpan(t, rec, "Valkey: Do")
	command, ok := attributeOf(span, attrKeyCommand)
	require.True(t, ok)
	assert.Equal(t, "HGET", command.AsString())

	err = conn.Do(ctx, conn.B().Hget().Key("hash").Field("missing").Build()).Error()
	assert.True(t, IsNotFound(err))
	assert.Equal(t, codes.Unset, lastSpan(t, rec, "Valkey: Do").Status().Code)

	err = conn.Do(ctx, conn.B().Get().Key("hash").Build()).Error()
	require.Error(t, err)
	assert.Equal(t, codes.Error, lastSpan(t, rec, "Valkey: Do").Status().Code)
}

// TestValkey_RecordKeysOffByDefault pins that what a command is given stays off
// its span unless Config.RecordKeys says otherwise. Keys routinely embed session
// tokens and user identifiers, and every span used to carry them.
func TestValkey_RecordKeysOffByDefault(t *testing.T) {
	conn, rec := connectTest(t, Config{})
	ctx := t.Context()

	require.NoError(t, conn.Set(ctx, "session:secret", []byte("v"), nil))
	_, err := conn.Get(ctx, "session:secret", nil)
	require.NoError(t, err)
	_, err = conn.MGet(ctx, []string{"session:secret"})
	require.NoError(t, err)
	_, err = conn.Scan(ctx, "session:*", nil)
	require.NoError(t, err)
	require.NoError(t, conn.Publish(ctx, "user:42", []byte("hi")))
	require.NoError(t, conn.Delete(ctx, []string{"session:secret"}))

	recorded := map[attribute.Key]bool{attrKeyKey: true, attrKeyKeys: true, attrKeyPattern: true, attrKeyChannel: true}
	for _, span := range rec.Spans() {
		for _, attr := range span.Attributes() {
			assert.False(t, recorded[attr.Key], "span %q carries %s", span.Name(), attr.Key)
		}
	}
}

// operationPoints returns the valkey.operation.duration data points recorded so
// far, as "<operation>/<outcome>" with their count, after checking that each is
// keyed on the connection's name.
func operationPoints(t *testing.T, rec *servicetest.Recorder) map[string]uint64 {
	t.Helper()

	out := map[string]uint64{}
	for _, m := range rec.Metrics() {
		if m.Name != "valkey.operation.duration" {
			continue
		}

		assert.Equal(t, "s", m.Unit)

		histogram, ok := m.Data.(metricdata.Histogram[float64])
		require.True(t, ok, "valkey.operation.duration is a %T, not a float64 histogram", m.Data)

		for _, point := range histogram.DataPoints {
			name, _ := point.Attributes.Value("valkey.name")
			assert.Equal(t, identifier, name.AsString())

			operation, _ := point.Attributes.Value("valkey.operation")
			outcome, _ := point.Attributes.Value("valkey.outcome")
			out[fmt.Sprintf("%s/%s", operation.AsString(), outcome.AsString())] += point.Count
		}
	}

	return out
}

// TestValkey_Metrics pins that every operation is measured, under its outcome.
func TestValkey_Metrics(t *testing.T) {
	conn, rec := connectTest(t, Config{})
	ctx := t.Context()

	require.NoError(t, conn.Set(ctx, "a", []byte("1"), nil))
	_, err := conn.Get(ctx, "a", nil)
	require.NoError(t, err)
	_, err = conn.Get(ctx, "missing", nil)
	require.True(t, IsNotFound(err))
	require.Error(t, conn.Expire(ctx, "a", 0))

	operations := operationPoints(t, rec)
	assert.Equal(t, uint64(1), operations["Set/ok"])
	assert.Equal(t, uint64(2), operations["Get/ok"], "a missing key is not a failed operation")
	assert.Equal(t, uint64(1), operations["Expire/error"])
}

// TestValkey_CheckHealthAndClose pins the lifecycle the Service drives.
func TestValkey_CheckHealthAndClose(t *testing.T) {
	conn, _ := connectTest(t, Config{})
	dep := dependency{conn: conn}

	require.NoError(t, dep.CheckHealth(t.Context()))
	require.NoError(t, dep.Close(t.Context()))

	err := dep.CheckHealth(t.Context())
	require.Error(t, err)
	assert.Equal(t, errorstack.CodeServiceUnavailable, codeOf(t, err))
	assert.True(t, errors.Is(err, valkey.ErrClosing))
}
