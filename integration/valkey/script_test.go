package valkey

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
	"go.opentelemetry.io/otel/codes"
)

// countHits is the script the tests below run: it increments KEYS[1] by ARGV[1]
// and returns the new count.
var countHits = NewScript("count_hits", `return redis.call('INCRBY', KEYS[1], ARGV[1])`)

// evalCalls returns how many EVAL commands the server has run since its
// statistics were last reset.
func evalCalls(t *testing.T, conn *connection) int64 {
	t.Helper()

	info, err := conn.client.Do(t.Context(), conn.client.B().Info().Section("commandstats").Build()).ToString()
	require.NoError(t, err)

	for line := range strings.SplitSeq(info, "\n") {
		stats, found := strings.CutPrefix(strings.TrimSpace(line), "cmdstat_eval:")
		if !found {
			continue
		}

		for field := range strings.SplitSeq(stats, ",") {
			if value, ok := strings.CutPrefix(field, "calls="); ok {
				calls, err := strconv.ParseInt(value, 10, 64)
				require.NoError(t, err)

				return calls
			}
		}
	}

	return 0
}

// TestScript_RunsByItsSHA1 pins how a script reaches the server: whole with EVAL
// the first time, when the server does not know it, then by its SHA-1 alone with
// EVALSHA, and whole again only once the server has forgotten it, here after a
// SCRIPT FLUSH, which is what a restart or a failover to a server that never ran
// it amounts to. Each run answers what the script returns.
func TestScript_RunsByItsSHA1(t *testing.T) {
	conn, _ := connectTest(t, Config{})

	require.NoError(t, conn.client.Do(t.Context(), conn.client.B().ConfigResetstat().Build()).Error())

	run := func() int64 {
		count, err := countHits.Run(t.Context(), conn, []string{"page_views"}, []string{"1"}).AsInt64()
		require.NoError(t, err)

		return count
	}

	assert.Equal(t, int64(1), run())
	assert.Equal(t, int64(1), evalCalls(t, conn), "the first run sends the script whole")

	assert.Equal(t, int64(2), run())
	assert.Equal(t, int64(3), run())
	assert.Equal(t, int64(1), evalCalls(t, conn), "a script the server knows is sent by its SHA-1 alone")

	require.NoError(t, conn.client.Do(t.Context(), conn.client.B().ScriptFlush().Build()).Error())

	assert.Equal(t, int64(4), run(), "a script the server forgot still runs")
	assert.Equal(t, int64(2), evalCalls(t, conn), "NOSCRIPT is answered by sending the script whole")
}

// A script's errors and replies are the server's, as EVAL's are: an error the
// script raises is the run's error, and a nil reply is the driver's nil, which is
// no failure.
func TestScript_RepliesAsTheServerDoes(t *testing.T) {
	conn, rec := connectTest(t, Config{})

	refused := NewScript("refuse_seat", `return redis.error_reply('ERR seat 7B is taken')`)
	err := refused.Run(t.Context(), conn, []string{"seats:42"}, nil).Error()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seat 7B is taken")

	broken := NewScript("broken", `return redis.call('NOSUCHCOMMAND')`)
	require.Error(t, broken.Run(t.Context(), conn, []string{"seats:42"}, nil).Error())

	nothing := NewScript("nothing", `return nil`)
	assert.True(t, valkey.IsValkeyNil(nothing.Run(t.Context(), conn, []string{"seats:42"}, nil).Error()))

	spans := spansNamed(rec, "Valkey: Script")
	require.Len(t, spans, 3)
	assert.Equal(t, codes.Error, spans[0].Status().Code)
	assert.Equal(t, codes.Error, spans[1].Status().Code)
	assert.Equal(t, codes.Unset, spans[2].Status().Code, "a nil reply is no failure")
}

// A script that writes, run on a read-only replica, is refused as EVAL is there,
// with READONLY, and one that only reads still runs.
func TestScript_OnAReadOnlyReplica(t *testing.T) {
	conn, _ := connectTest(t, Config{})

	_, err := countHits.Run(t.Context(), conn, []string{"page_views"}, []string{"1"}).AsInt64()
	require.NoError(t, err)

	// A replica of a primary that is nowhere: the server refuses every write from
	// then on, whether it ever reaches the primary or not.
	require.NoError(t, conn.client.Do(t.Context(), conn.client.B().Replicaof().Host("127.0.0.1").Port(1).Build()).Error())
	t.Cleanup(func() {
		_ = conn.client.Do(context.Background(), conn.client.B().Replicaof().No().One().Build()).Error()
	})

	err = countHits.Run(t.Context(), conn, []string{"page_views"}, []string{"1"}).Error()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "READONLY")

	reads := NewScript("read_views", `return redis.call('GET', KEYS[1])`)
	views, err := reads.Run(t.Context(), conn, []string{"page_views"}, nil).ToString()
	require.NoError(t, err)
	assert.Equal(t, "1", views)
}

// TestScript_RecordsItsNameAndNothingItIsGiven pins the span of a run: named for
// the operation, carrying the script's name, and neither its source nor its
// arguments, nor its keys unless Config.RecordKeys says so.
func TestScript_RecordsItsNameAndNothingItIsGiven(t *testing.T) {
	for _, recordKeys := range []bool{false, true} {
		t.Run("RecordKeys "+strconv.FormatBool(recordKeys), func(t *testing.T) {
			conn, rec := connectTest(t, Config{RecordKeys: recordKeys})

			_, err := countHits.Run(t.Context(), conn, []string{"page_views"}, []string{"987654321"}).AsInt64()
			require.NoError(t, err)

			span := lastSpan(t, rec, "Valkey: Script")

			name, ok := attributeOf(span, attrKeyScript)
			require.True(t, ok)
			assert.Equal(t, "count_hits", name.AsString())

			_, keyed := attributeOf(span, attrKeyKeys)
			assert.Equal(t, recordKeys, keyed)

			for _, attr := range span.Attributes() {
				assert.NotContains(t, attr.Value.String(), "INCRBY", "the source is recorded under %s", attr.Key)
				assert.NotContains(t, attr.Value.String(), "987654321", "an argument is recorded under %s", attr.Key)
			}
		})
	}
}

// commandRecorder is a Valkey that is no ScriptRunner: it records every command
// sent through its Do, and sends it on.
type commandRecorder struct {
	Valkey

	commands [][]string
}

// Do records cmd's words, then sends it on.
func (r *commandRecorder) Do(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
	r.commands = append(r.commands, append([]string(nil), cmd.Commands()...))

	return r.Valkey.Do(ctx, cmd)
}

// A Valkey that is no ScriptRunner, a test's in-memory one or a wrapper written
// elsewhere, is sent the script whole with EVAL through its Do, the command a
// caller builds by hand.
func TestScript_OnAnotherValkeyIsAnEvalThroughDo(t *testing.T) {
	conn, _ := connectTest(t, Config{})
	recorder := &commandRecorder{Valkey: conn}

	count, err := countHits.Run(t.Context(), recorder, []string{"page_views"}, []string{"5"}).AsInt64()
	require.NoError(t, err)
	assert.Equal(t, int64(5), count)

	require.Len(t, recorder.commands, 1)
	assert.Equal(t, []string{"EVAL", countHits.Source(), "1", "page_views", "5"}, recorder.commands[0])
}
