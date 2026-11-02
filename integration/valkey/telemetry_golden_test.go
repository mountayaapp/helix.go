package valkey

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// TestTelemetryGolden pins what this integration emits, so that changing how a
// span is produced cannot silently change what is produced. It records span
// names, emitting scope, kind and attribute keys - never values, timings or IDs,
// which differ on every run.
//
// A diff here is not automatically a failure: it is a change to the library's
// observable output, and it has to be an intentional one. Update the golden in
// the same commit that changes the instrumentation, never separately.
//
// It runs with Config.RecordKeys on, to pin the keys that records. Without it no
// span carries valkey.key, valkey.keys, valkey.pattern or valkey.channel; see
// TestValkey_RecordKeysOffByDefault.
func TestTelemetryGolden(t *testing.T) {
	address := startValkey(t)
	svc, rec := servicetest.New(t)

	cache, err := Connect(t.Context(), svc, Config{Name: "golden", Address: address, RecordKeys: true})
	require.NoError(t, err)

	ctx := t.Context()
	require.NoError(t, cache.Set(ctx, "a", []byte("1"), &OptionsSet{TTL: time.Minute}))
	_, err = cache.Get(ctx, "a", nil)
	require.NoError(t, err)
	_, err = cache.Exists(ctx, "a")
	require.NoError(t, err)
	_, err = cache.Increment(ctx, "n", 1)
	require.NoError(t, err)
	_, err = cache.Decrement(ctx, "n", 1)
	require.NoError(t, err)
	_, err = cache.IncrementTTL(ctx, "n", 1, time.Minute)
	require.NoError(t, err)
	_, err = cache.CompareAndSet(ctx, "a", []byte("1"), []byte("2"), time.Minute)
	require.NoError(t, err)
	_, err = cache.CompareAndDelete(ctx, "nx", []byte("1"))
	require.NoError(t, err)
	require.NoError(t, cache.Expire(ctx, "a", time.Minute))
	_, err = cache.Scan(ctx, "*", nil)
	require.NoError(t, err)
	_, err = cache.MGet(ctx, []string{"a"})
	require.NoError(t, err)
	_, err = cache.SetNX(ctx, "nx", []byte("1"), &OptionsSet{TTL: time.Minute})
	require.NoError(t, err)
	require.NoError(t, cache.Publish(ctx, "chan", []byte("hi")))
	_, err = cache.XAdd(ctx, "stream", 10, map[string]string{"f": "v"})
	require.NoError(t, err)
	_, err = cache.XRange(ctx, "stream", "-", "+", nil)
	require.NoError(t, err)
	require.NoError(t, cache.Do(ctx, cache.B().Ping().Build()).Error())
	_, err = cache.SAdd(ctx, "set", []string{"m"}, &OptionsSAdd{TTL: time.Minute})
	require.NoError(t, err)
	_, err = cache.SMembers(ctx, "set")
	require.NoError(t, err)
	_, err = cache.SRem(ctx, "set", []string{"m"})
	require.NoError(t, err)
	require.NoError(t, cache.Delete(ctx, []string{"a", "n"}))

	// A subscription, last: its setup ends when the server confirms it, and the
	// message published once it has is handled under a span of its own. The
	// handler holds until Publish returns, so that the two spans end in an order
	// the golden can pin.
	subCtx, cancel := context.WithCancel(ctx)
	published := make(chan struct{})
	received := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- cache.Subscribe(subCtx, "events", func(context.Context, PubSubMessage) {
			<-published
			close(received)
		})
	}()

	require.Eventually(t, func() bool {
		return len(spansNamed(rec, "Valkey: Subscribe")) == 1
	}, 5*time.Second, 10*time.Millisecond)

	require.NoError(t, cache.Publish(ctx, "events", []byte("hi")))
	close(published)
	<-received
	cancel()
	require.NoError(t, <-done)

	var got []string
	for _, s := range servicetest.Summarize(rec.Spans()) {
		got = append(got, s.String())
	}

	// Every span carries valkey.address, valkey.database and valkey.name. What a
	// command is given — valkey.key, valkey.keys, valkey.pattern, valkey.channel —
	// is only there because RecordKeys is on.
	want := []string{
		"github.com/mountayaapp/helix.go | client | Valkey: Set | valkey.address,valkey.database,valkey.key,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: Get | valkey.address,valkey.database,valkey.key,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: Exists | valkey.address,valkey.database,valkey.key,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: Increment | valkey.address,valkey.database,valkey.key,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: Decrement | valkey.address,valkey.database,valkey.key,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: IncrementTTL | valkey.address,valkey.database,valkey.key,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: CompareAndSet | valkey.address,valkey.database,valkey.key,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: CompareAndDelete | valkey.address,valkey.database,valkey.key,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: Expire | valkey.address,valkey.database,valkey.key,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: Scan | valkey.address,valkey.database,valkey.name,valkey.pattern",
		"github.com/mountayaapp/helix.go | client | Valkey: MGet | valkey.address,valkey.database,valkey.keys,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: SetNX | valkey.address,valkey.database,valkey.key,valkey.name",
		"github.com/mountayaapp/helix.go | producer | Valkey: Publish | valkey.address,valkey.channel,valkey.database,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: XAdd | valkey.address,valkey.database,valkey.key,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: XRange | valkey.address,valkey.database,valkey.key,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: Do | valkey.address,valkey.command,valkey.database,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: SAdd | valkey.address,valkey.database,valkey.key,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: SMembers | valkey.address,valkey.database,valkey.key,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: SRem | valkey.address,valkey.database,valkey.key,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: Delete | valkey.address,valkey.database,valkey.keys,valkey.name",
		"github.com/mountayaapp/helix.go | client | Valkey: Subscribe | valkey.address,valkey.channel,valkey.database,valkey.name",
		"github.com/mountayaapp/helix.go | producer | Valkey: Publish | valkey.address,valkey.channel,valkey.database,valkey.name",
		"github.com/mountayaapp/helix.go | consumer | Valkey: Receive | valkey.address,valkey.channel,valkey.database,valkey.name",
	}

	assert.Equal(t, want, got)

	servicetest.AssertConvention(t, rec.Spans())

	// One histogram, keyed on the connection's name, the operation and its
	// outcome — nothing whose cardinality a time series could not carry. Every
	// operation is in it but Receive: handling a message is the application's
	// time, not Valkey's.
	var metrics []string
	var operations []string
	for _, m := range rec.Metrics() {
		metrics = append(metrics, m.Name)
		if m.Name != "valkey.operation.duration" {
			continue
		}

		for _, point := range m.Data.(metricdata.Histogram[float64]).DataPoints {
			var keys []string
			for _, attr := range point.Attributes.ToSlice() {
				keys = append(keys, string(attr.Key))
			}
			assert.Equal(t, []string{"valkey.name", "valkey.operation", "valkey.outcome"}, keys)

			operation, _ := point.Attributes.Value("valkey.operation")
			operations = append(operations, operation.AsString())
		}
	}

	assert.Equal(t, []string{"valkey.operation.duration"}, metrics)

	slices.Sort(operations)
	assert.Equal(t, []string{
		"CompareAndDelete", "CompareAndSet", "Decrement", "Delete", "Do", "Exists",
		"Expire", "Get", "Increment", "IncrementTTL", "MGet", "Publish", "SAdd", "SMembers",
		"SRem", "Scan", "Set", "SetNX", "Subscribe", "XAdd", "XRange",
	}, operations)
}
