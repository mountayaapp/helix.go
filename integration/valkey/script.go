package valkey

import (
	"context"
	"sync"

	"github.com/mountaya/helix.go/telemetry/trace"

	"github.com/valkey-io/valkey-go"
)

// Script is a Lua script the server runs, built once from its source and run
// with keys and arguments as often as needed.
//
//	var reserve = valkey.NewScript("reserve_seat", `
//	if redis.call('SISMEMBER', KEYS[1], ARGV[1]) == 1 then
//	  return 0
//	end
//	return redis.call('SADD', KEYS[1], ARGV[1])
//	`)
//
//	added, err := reserve.Run(ctx, store, []string{"seats:42"}, []string{"7B"}).AsInt64()
//
// On the Valkey Connect returns it is sent with EVALSHA, which names the script
// by its SHA-1, and whole with EVAL only when the server answers NOSCRIPT, not
// knowing it yet: the first run on a server, and the first after a restart, a
// SCRIPT FLUSH or a failover to a server that never ran it. What the server
// answers is the reply and the error EVAL gets: what the script returns, an error
// it raises, a command it runs refused (a write on a read-only replica is
// refused with READONLY), all as they are.
//
// A Script is safe for concurrent use.
type Script struct {
	name   string
	source string
	lua    func() *valkey.Lua
}

// NewScript returns the script named name, whose Lua source is source. The name
// is what the span of each run records, and must not vary with what a run is
// given; the source, the keys under Config.RecordKeys aside, and the arguments are
// never recorded.
//
// It computes nothing from source. The SHA-1 EVALSHA names the script by is
// computed on its first run: a process running in FIPS 140-only mode panics on
// SHA-1, and a script whose SHA-1 were computed when its package is loaded would
// stop every such process importing that package, whether it runs the script or
// not. So a Script can be a package-level variable.
func NewScript(name, source string) *Script {
	return &Script{
		name:   name,
		source: source,
		lua: sync.OnceValue(func() *valkey.Lua {
			return valkey.NewLuaScript(source)
		}),
	}
}

// Name returns the script's name.
func (s *Script) Name() string {
	return s.name
}

// Source returns the script's Lua source.
func (s *Script) Source() string {
	return s.source
}

// Run runs the script with keys and args on v, and returns the server's reply as
// Do returns one.
//
// On a Valkey that is a ScriptRunner, the one Connect returns among them, it is
// sent with EVALSHA, and with EVAL when the server answers NOSCRIPT, under a
// "Valkey: Script" span recording the script's name. On any other Valkey it is
// sent whole with EVAL through v.Do, the command a caller would build by hand, so
// an implementation of Valkey written elsewhere, a test's in-memory one among
// them, is asked what it always was.
func (s *Script) Run(ctx context.Context, v Valkey, keys []string, args []string) valkey.ValkeyResult {
	if runner, ok := v.(ScriptRunner); ok {
		return runner.RunScript(ctx, s, keys, args)
	}

	return v.Do(ctx, v.B().Eval().Script(s.source).Numkeys(int64(len(keys))).Key(keys...).Arg(args...).Build())
}

// ScriptRunner is implemented by the Valkey Connect returns: it runs a Script
// with EVALSHA, falling back to EVAL on NOSCRIPT. Script.Run asks for it, and a
// caller rarely needs to.
//
// It is an interface of its own rather than a method of Valkey, so that an
// implementation of Valkey written elsewhere, a test's in-memory one among them,
// does not have to change: Script.Run sends a script through Do to any Valkey
// that is not one.
type ScriptRunner interface {

	// RunScript runs script with keys and args, and returns the server's reply as
	// Do returns one. See Script.Run.
	RunScript(ctx context.Context, script *Script, keys []string, args []string) valkey.ValkeyResult
}

// The connection Connect returns runs scripts by their SHA-1.
var _ ScriptRunner = (*connection)(nil)

// RunScript runs script with EVALSHA, and with EVAL when the server does not know
// it yet.
//
// It automatically handles tracing and error recording.
func (conn *connection) RunScript(ctx context.Context, script *Script, keys []string, args []string) valkey.ValkeyResult {
	attrs := append(conn.keyListAttrs(keys), attrKeyScript.String(script.name))

	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "Script", attrs...)
	defer op.End(ctx)

	result := script.lua().Exec(ctx, conn.client, keys, args)
	if err := result.Error(); err != nil && !valkey.IsValkeyNil(err) {
		op.RecordError("failed to run script", err)
	}

	return result
}
