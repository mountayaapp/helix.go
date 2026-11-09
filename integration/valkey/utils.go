package valkey

import (
	"sync"
	"time"

	"github.com/mountayaapp/helix.go/integration"

	"github.com/valkey-io/valkey-go"
)

// Pre-computed attribute keys to avoid allocations on every call.
//
// Only what a command is given varies per operation, and it is recorded only
// under Config.RecordKeys. The server address and database are fixed for the life
// of the connection, so they are handed to the Instrument once and stamped by it.
var (
	attrKeyKey     = integration.Key(identifier, "key")
	attrKeyKeys    = integration.Key(identifier, "keys")
	attrKeyPattern = integration.Key(identifier, "pattern")
	attrKeyChannel = integration.Key(identifier, "channel")
	attrKeyCommand = integration.Key(identifier, "command")
)

// defaultScanCount is the COUNT a scan sends when OptionsScan leaves it unset. The
// server's own default, 10, makes a scan for a rare pattern across a large key
// space take thousands of round trips.
const defaultScanCount = 1000

// defaultSScanCount is the COUNT a set scan sends when OptionsSScan leaves it
// unset: a page of about a hundred members, a few kilobytes to a few hundred
// depending on the members, rather than the server's own ten.
const defaultSScanCount = 100

// compareAndSetScript sets KEYS[1] to ARGV[2] when it holds ARGV[1], with a
// time-to-live of ARGV[3] milliseconds when that is positive and none otherwise,
// and returns 1; it returns 0 and leaves the key alone when it holds anything
// else. A missing key reads as false, which equals no string.
//
// The scripts are built on first use: the driver computes a script's SHA-1, for
// EVALSHA, as it builds it, and a process running in FIPS 140-only mode panics on
// SHA-1. Built when the package is loaded, they would stop every such process
// importing it, whether it compares anything or not. The driver sends EVALSHA,
// and EVAL when the server does not know the script yet.
var compareAndSetScript = sync.OnceValue(func() *valkey.Lua {
	return valkey.NewLuaScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return 0
end
if tonumber(ARGV[3]) > 0 then
  redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
else
  redis.call('SET', KEYS[1], ARGV[2])
end
return 1
`)
})

// compareAndDeleteScript deletes KEYS[1] when it holds ARGV[1] and returns 1; it
// returns 0 and leaves the key alone when it holds anything else or is missing.
// It is built on first use, like compareAndSetScript.
var compareAndDeleteScript = sync.OnceValue(func() *valkey.Lua {
	return valkey.NewLuaScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return 0
end
return redis.call('DEL', KEYS[1])
`)
})

// milliseconds returns ttl in whole milliseconds, rounded up: truncating would turn
// a positive TTL below a millisecond into PX 0, which the server rejects.
func milliseconds(ttl time.Duration) int64 {
	ms := int64(ttl / time.Millisecond)
	if ttl%time.Millisecond > 0 {
		ms++
	}

	return ms
}

// buildSet builds the SET command Set and SetNX send.
//
// The value is copied into the command rather than aliased. The command can still
// be queued for writing when Do returns early on a cancelled context, so a
// command pointing at the caller's buffer would be read by the driver's writer
// while the caller, rightly considering the call over, reuses it.
func buildSet(b valkey.Builder, key string, value []byte, opts *OptionsSet, nx bool) valkey.Completed {
	var ms int64
	if opts != nil && opts.TTL > 0 {
		ms = milliseconds(opts.TTL)
	}

	cmd := b.Set().Key(key).Value(string(value))
	switch {
	case nx && ms > 0:
		return cmd.Nx().PxMilliseconds(ms).Build()
	case nx:
		return cmd.Nx().Build()
	case ms > 0:
		return cmd.PxMilliseconds(ms).Build()
	default:
		return cmd.Build()
	}
}
