package valkey

import (
	"context"
	"crypto/tls"
	"errors"
	"maps"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"
	"github.com/mountaya/helix.go/service"
	"github.com/mountaya/helix.go/telemetry/trace"

	"github.com/valkey-io/valkey-go"
	"go.opentelemetry.io/otel/attribute"
)

// ErrNotFound is the error Get returns when the key does not exist. Test for it
// with IsNotFound or errors.Is.
//
// It is the driver's nil reply itself rather than an error of this package, so
// that code already testing Get's error with the driver's IsValkeyNil keeps
// working; new code has no reason to import the driver for it.
var ErrNotFound error = valkey.Nil

// IsNotFound reports whether err means that the key did not exist.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// Entry represents a key/value pair in Valkey.
type Entry struct {
	Key   string `json:"key"`
	Value []byte `json:"value"`
}

// PubSubMessage is a single message received on a subscribed channel.
type PubSubMessage struct {
	Channel string `json:"channel"`
	Payload []byte `json:"payload"`
}

// StreamEntry is a single entry read from a stream, identified by its server-assigned
// id and carrying the field/value pairs of the entry.
type StreamEntry struct {
	ID     string            `json:"id"`
	Fields map[string]string `json:"fields"`
}

// Valkey exposes an opinionated way to interact with Valkey, by bringing automatic
// distributed tracing as well as error recording within traces.
//
// Every method but B opens a span named "Valkey: <Method>" and measures its
// duration in valkey.operation.duration; Subscribe measures only its setup. The
// keys, patterns and channels a method is given are recorded on its span only
// under Config.RecordKeys.
//
// It works the same against a standalone server and a cluster: commands spanning
// several keys are split by hash slot, and scans walk every node.
type Valkey interface {

	// Exists reports whether key exists.
	Exists(ctx context.Context, key string) (bool, error)

	// Get returns the value stored at key.
	//
	// A missing key returns ErrNotFound — test it with IsNotFound — and is not
	// recorded as an error on the span unless opts.ErrorRecordOnNotFound says so.
	// Any other failure always is.
	Get(ctx context.Context, key string, opts *OptionsGet) ([]byte, error)

	// Set stores value at key, overwriting whatever was there. opts.TTL sets its
	// expiry; see OptionsSet for how it is applied. The value is copied before the
	// call returns, so the caller may reuse it at once.
	Set(ctx context.Context, key string, value []byte, opts *OptionsSet) error

	// SetNX stores value at key only if the key does not already exist, with the
	// same options as Set. Returns true when the value was set — the caller won the
	// race — and false when the key already existed, which is not an error.
	SetNX(ctx context.Context, key string, value []byte, opts *OptionsSet) (bool, error)

	// Increment adds increment to the integer stored at key and returns the
	// result. A missing key counts as 0.
	Increment(ctx context.Context, key string, increment int64) (int64, error)

	// Decrement subtracts decrement from the integer stored at key and returns the
	// result. A missing key counts as 0.
	Decrement(ctx context.Context, key string, decrement int64) (int64, error)

	// IncrementTTL adds increment to the integer stored at key and returns the
	// result, like Increment, and sets ttl as the key's time-to-live, in
	// milliseconds rounded up, when the key has none. The two commands, INCRBY and
	// PEXPIRE with NX, are sent in one round trip.
	//
	// The time-to-live is only set on a key without one: the increment creating the
	// key sets it, and later ones leave it alone, so a fixed window does not slide.
	// And since every call sets it when it is missing, a key is never left without
	// one past its next increment, whatever happened to an earlier call.
	//
	// The NX option of PEXPIRE needs Valkey, or Redis 7.0 or later: an older server
	// fails every call. A ttl of zero or less is rejected with a validation error,
	// and nothing is sent. When the expiry fails, the error is returned although the
	// increment may have been applied.
	IncrementTTL(ctx context.Context, key string, increment int64, ttl time.Duration) (int64, error)

	// CompareAndSet stores value at key only if the key holds expected, in one
	// atomic step on the server. It returns true when the value was replaced, and
	// false when the key holds anything else, which is not an error. A missing key
	// holds nothing, and matches no expected value, an empty one included: SetNX is
	// how a missing key is claimed.
	//
	// ttl is the key's new time-to-live, in milliseconds rounded up. Zero or less
	// stores the value without expiry, which, like Set, clears the one the key had.
	// Both values are copied before the call returns.
	//
	// It runs as a Lua script, which a server with scripting disabled, or an ACL
	// denying EVAL and EVALSHA, refuses.
	CompareAndSet(ctx context.Context, key string, expected, value []byte, ttl time.Duration) (bool, error)

	// CompareAndDelete deletes key only if it holds expected, in one atomic step on
	// the server. It returns true when the key was deleted, and false when it holds
	// anything else or does not exist, which is not an error.
	//
	// It is how the holder of a claim releases it without deleting the claim of
	// another holder, taken since its own expired. Like CompareAndSet, it runs as a
	// Lua script.
	CompareAndDelete(ctx context.Context, key string, expected []byte) (bool, error)

	// Expire sets or refreshes the time-to-live of key, in milliseconds rounded up.
	// Subsequent calls reset it relative to the new call, which is the natural fit
	// for fixed-window rate limiters where the key is re-created at the start of
	// each window. Expiring a missing key does nothing.
	//
	// A ttl of zero or less is rejected with a validation error, and the key is
	// left alone: the server would delete the key, which is what Delete is for, and
	// a zero ttl is far more often a TTL nobody configured than a request to delete.
	Expire(ctx context.Context, key string, ttl time.Duration) error

	// Scan returns the keys matching the glob-style pattern; an empty pattern
	// matches every key. It walks every node of a cluster, and never returns a key
	// twice. opts sets how much work each round trip does and caps the result; see
	// OptionsScan. Scan returns either keys or an error, never both.
	//
	// Scanning visits the whole key space whatever the pattern, so it is meant for
	// maintenance and tooling, not for a request path.
	Scan(ctx context.Context, pattern string, opts *OptionsScan) ([]string, error)

	// MGet returns the entries of the keys that exist, in the order the keys were
	// given. A missing key is left out; a key holding an empty value is not.
	MGet(ctx context.Context, keys []string) ([]Entry, error)

	// Delete deletes the keys. Missing keys are ignored.
	//
	// In a cluster the keys are deleted one by one, in a single round trip, so a
	// failure may leave some of them deleted; the error of the first failing key
	// is returned.
	Delete(ctx context.Context, keys []string) error

	// SAdd adds members to the set stored at key, creating it when missing, and
	// returns how many of them were not already in it. opts.TTL, when positive,
	// sets the set's time-to-live in the same round trip, on every call, so a set
	// written to keeps living and one nobody writes to expires. No members sends
	// nothing and returns 0.
	//
	// A set is how a group of keys is listed without Scan: each key is added to
	// the set naming the group when it is written, and the group is read back
	// with SMembers and MGet.
	SAdd(ctx context.Context, key string, members []string, opts *OptionsSAdd) (int64, error)

	// SRem removes members from the set stored at key and returns how many of
	// them were in it. Removing the last member deletes the set. A missing key
	// is an empty set, and no members sends nothing; both return 0.
	SRem(ctx context.Context, key string, members []string) (int64, error)

	// SMembers returns every member of the set stored at key, in no particular
	// order. A missing key is an empty set, not an error. A set that can grow large
	// is read with SScan instead, a page at a time.
	SMembers(ctx context.Context, key string) ([]string, error)

	// SScan returns one page of the members of the set stored at key, and the
	// cursor to pass for the next page: a scan starts at cursor 0, and is done once
	// the cursor returned is 0 again. A missing key is an empty set, done at once.
	// See OptionsSScan for matching members and sizing pages.
	//
	// Only a page is held at a time, whatever the set's size. A member in the set
	// for the whole scan is returned at least once, and may be returned more than
	// once, across pages; one added or removed during the scan may be returned or
	// not. A page may be empty while the scan is not done.
	SScan(ctx context.Context, key string, cursor uint64, opts *OptionsSScan) ([]string, uint64, error)

	// SCard returns how many members the set stored at key holds, without reading
	// them. A missing key is an empty set: 0, not an error.
	SCard(ctx context.Context, key string) (int64, error)

	// Publish publishes message to channel for live fan-out to the subscribers
	// connected at that moment. The message is copied before the call returns.
	Publish(ctx context.Context, channel string, message []byte) error

	// Subscribe subscribes to channel and calls handler for every message until ctx
	// is done. It blocks for the lifetime of the subscription, so callers run it in
	// a goroutine and cancel ctx to tear it down.
	//
	// Subscriptions share the connection's pipelined connection, and every local
	// Subscribe of one channel shares one server subscription: the first opens it
	// and the last to leave unsubscribes it. Each message is queued for each
	// subscriber without waiting on its handler, so a slow handler holds up
	// nobody else, and a subscriber that falls further behind than
	// Config.SubscriberBacklog or Config.SubscriberBacklogBytes is ended with
	// ErrSubscriberTooSlow.
	//
	// Each message is handled under its own "Valkey: Receive" consumer span, which
	// the ctx handed to handler carries, and handler is called on Subscribe's
	// goroutine, one message at a time. That ctx is done once the subscription
	// has ended for any reason, so a handler waiting to pass a message on stops
	// waiting.
	//
	// It returns nil once ctx is done. A subscription is not resumed in silence
	// after the connection carrying it is lost, since what was published in
	// between is gone: Subscribe returns ErrSubscriptionInterrupted then, as when
	// the server ends the subscription or the Service closes the connection at
	// shutdown, and the error that ended it otherwise.
	Subscribe(ctx context.Context, channel string, handler func(ctx context.Context, msg PubSubMessage)) error

	// XAdd appends an entry of field/value pairs to stream and returns the
	// server-assigned entry id. When maxLen is greater than zero the stream is
	// capped to approximately that many entries (MAXLEN ~).
	//
	// Fields are sent in sorted order, so that an entry reads back the same way
	// every time. An empty fields map is rejected with a validation error, which
	// the server would otherwise return as a syntax error.
	XAdd(ctx context.Context, stream string, maxLen int64, fields map[string]string) (string, error)

	// XRange returns the entries of stream within the inclusive id range
	// [start, end]. Pass "-" and "+" for the full range, or an exclusive "(id"
	// start to resume after a known id. opts.Count caps how many are returned.
	XRange(ctx context.Context, stream, start, end string, opts *OptionsXRange) ([]StreamEntry, error)

	// B returns the driver's command builder, for the commands this interface does
	// not cover. Commands built from it are sent with Do. In a cluster, a command
	// whose keys fall in different hash slots panics when it is built.
	B() valkey.Builder

	// Do sends a command built from B and returns the driver's result. It opens a
	// "Valkey: Do" span recording the command's name, and records a failure on it;
	// a nil reply is not one. The command must not be reused afterwards.
	//
	// It is the escape hatch, kept narrow on purpose: the connection itself — and
	// with it Close — stays with the Service.
	Do(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult
}

// ConfirmedSubscriber is implemented by the Valkey Connect returns. Its
// SubscribeConfirmed is Subscribe, telling the caller when the server holds the
// subscription.
//
// Subscribe is asked for on the connection the subscriptions share, and the
// server confirms it some time after the call: a command the caller sends
// meanwhile, on another of the client's connections, can reach the server first.
// A caller that reads a durable copy of a channel's messages, the history of a
// stream, and relies on the channel for whatever comes after it must not read
// before the server holds the subscription, or a message published in between is
// in neither:
//
//	subscriber, ok := v.(valkey.ConfirmedSubscriber)
//	if !ok {
//	  return errors.New("cannot tell when a subscription is held")
//	}
//
//	confirmed := make(chan struct{})
//	ended := make(chan error, 1)
//	go func() {
//	  ended <- subscriber.SubscribeConfirmed(ctx, "orders", func() { close(confirmed) }, handle)
//	}()
//
//	select {
//	case <-confirmed:
//	  // Read the history: every message published from now on reaches handle.
//	case err := <-ended:
//	  // Never subscribed: err says why, nil when ctx ended first.
//	}
//
// It is an interface of its own rather than a method of Valkey, so that an
// implementation of Valkey written elsewhere, a test's in-memory one among them,
// does not have to change: a caller asserts it, and decides what to do with a
// Valkey that cannot say.
type ConfirmedSubscriber interface {

	// SubscribeConfirmed subscribes to channel and calls handler for every message
	// until ctx is done, exactly as Subscribe does, and calls confirmed once the
	// server has confirmed the subscription. A Subscribe joining a channel whose
	// server subscription is already confirmed is confirmed at once.
	//
	// From the moment confirmed is called, a message published on the channel
	// after any command the caller sends is delivered to handler, unless the
	// subscription ends first, for the reasons and with the errors Subscribe
	// gives. confirmed is called at most once, on the goroutine running
	// SubscribeConfirmed and before handler is first called, so it must return
	// promptly: no message is handed over while it runs.
	//
	// confirmed is not called when the subscription ends before the server
	// confirms it. SubscribeConfirmed then returns nil when ctx ended first, and
	// otherwise the error that ended it: the connection lost or refused, or the
	// server refusing the command. A caller waiting for the confirmation waits for
	// the call's return beside it, and for nothing else.
	SubscribeConfirmed(ctx context.Context, channel string, confirmed func(), handler func(ctx context.Context, msg PubSubMessage)) error
}

// The connection Connect returns says when its subscriptions are held.
var _ ConfirmedSubscriber = (*connection)(nil)

// connection is what Connect hands back: the Valkey implementation. Its
// lifecycle is not reachable from it; see dependency.
type connection struct {

	// inst names this connection to the telemetry pipeline. Every span below is
	// opened through it, which is what keeps the naming the same here as in the
	// eight other integrations.
	inst *integration.Instrument

	// client is the connection made with the Valkey client.
	client valkey.Client

	// recordKeys is Config.RecordKeys.
	recordKeys bool

	// pubsub shares a server subscription between the Subscribes of a channel
	// and queues each subscriber's messages.
	pubsub *pubsub
}

// Connect tries to create a Valkey client given the Config. Returns an error if
// Config is not valid or if the connection could not be established.
//
// The client connects before Connect returns, under ctx: Connect returns once ctx
// is done, whatever the connection's own timeouts.
func Connect(ctx context.Context, svc *service.Service, cfg Config) (Valkey, error) {
	if svc == nil {
		return nil, errorstack.NewValidation(errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"service"},
		})
	}

	// No need to continue if Config is not valid.
	err := cfg.sanitize()
	if err != nil {
		return nil, err
	}

	// TLS is converted before anything is dialed, and a configuration that does
	// not convert stops here. The client would otherwise go ahead without a TLS
	// config, and send HELLO, credentials included, over plain TCP.
	tlsConfig, entries := cfg.TLS.ToStandardTLS()
	if len(entries) > 0 {
		return nil, errorstack.NewValidation(entries...)
	}

	inst, err := integration.NewInstrument(integration.InstrumentConfig{
		Identifier: identifier,
		Humanized:  humanized,
		Name:       cfg.name(),

		// The address and the database are the config-derived facts worth stamping:
		// together they say which keys a command reached.
		Attributes: []attribute.KeyValue{
			integration.Key(identifier, "address").String(cfg.Address),
			integration.Key(identifier, "database").Int(cfg.Database),
		},

		TracerProvider: service.TracerProvider(svc),
		MeterProvider:  service.MeterProvider(svc),
		Propagator:     service.Propagator(svc),
	})
	if err != nil {
		return nil, err
	}

	// A TLS config whose ServerName is empty is completed per connection by the
	// TLS dialer, from the host of the address dialed.
	client, err := dial(ctx, valkey.ClientOption{
		InitAddress:      []string{cfg.Address},
		Username:         cfg.User,
		Password:         cfg.Password,
		SelectDB:         cfg.Database,
		ClientName:       cfg.ClientName,
		TLSConfig:        tlsConfig,
		Dialer:           net.Dialer{Timeout: cfg.DialTimeout},
		ConnWriteTimeout: cfg.WriteTimeout,

		// Client-side caching cannot be reached through this integration's API, and
		// enabling it makes the driver send CLIENT TRACKING on every connection — a
		// command an ACL or a proxy may refuse, which would fail Connect for a
		// feature nothing uses.
		DisableCache: true,
	})
	if err != nil {
		return nil, errorstack.Wrap(err, "Failed to connect to Valkey",
			errorstack.WithCode(errorstack.CodeServiceUnavailable),
			errorstack.WithExtension("reason", integration.NormalizeErrorMessage(err)),
		)
	}

	conn := &connection{
		inst:       inst,
		client:     client,
		recordKeys: cfg.RecordKeys,
		pubsub:     newPubSub(cfg.SubscriberBacklog, int64(cfg.SubscriberBacklogBytes)),
	}

	// Hand the connection to the Service, which owns its lifecycle from here. A
	// connection the Service did not take is nobody's to close but ours.
	if err := service.Attach(svc, cfg.name(), dependency{conn: conn}); err != nil {
		client.Close()
		return nil, err
	}

	return conn, nil
}

// dial builds the client under ctx.
//
// valkey.NewClient takes no context: it dials and handshakes under its own
// timeouts. Two things bound it by ctx instead. Every connection it opens dials
// under ctx as well, so a Connect whose caller has given up does not go on to
// dial — and authenticate — anyway. And Connect stops waiting once ctx is done,
// since a handshake already under way only ends at its own timeout; a client that
// arrives after that is closed rather than leaked.
//
// The binding ends with NewClient. The client dials again for the rest of its
// life — reconnecting, reaching new cluster nodes — and those dials belong to the
// commands that cause them, not to a Connect long returned.
func dial(ctx context.Context, opts valkey.ClientOption) (valkey.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}

	connected := make(chan struct{})
	opts.DialCtxFn = func(dialCtx context.Context, addr string, dialer *net.Dialer, tlsConfig *tls.Config) (net.Conn, error) {
		select {
		case <-connected:
		default:
			var cancel context.CancelCauseFunc
			dialCtx, cancel = context.WithCancelCause(dialCtx)
			defer cancel(nil)

			stop := context.AfterFunc(ctx, func() { cancel(context.Cause(ctx)) })
			defer stop()
		}

		// What the driver does without a DialCtxFn.
		if tlsConfig != nil {
			return (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(dialCtx, "tcp", addr)
		}

		return dialer.DialContext(dialCtx, "tcp", addr)
	}

	type result struct {
		client valkey.Client
		err    error
	}

	// Unbuffered, so that exactly one side owns the client: Connect when the
	// result is received, this goroutine when Connect has stopped waiting.
	results := make(chan result)
	go func() {
		client, err := valkey.NewClient(opts)
		close(connected)

		select {
		case results <- result{client: client, err: err}:
		case <-ctx.Done():
			if client != nil {
				client.Close()
			}
		}
	}()

	select {
	case r := <-results:
		return r.client, r.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// Exists checks if a key exists in Valkey.
//
// It automatically handles tracing and error recording.
func (conn *connection) Exists(ctx context.Context, key string) (bool, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "Exists", conn.keyAttrs(key)...)
	defer op.End(ctx)

	count, err := conn.client.Do(ctx, conn.client.B().Exists().Key(key).Build()).AsInt64()
	if err != nil {
		op.RecordError("failed to check key existence", err)
		return false, err
	}

	return count > 0, nil
}

// Get reads the value at key and returns its byte representation.
//
// It automatically handles tracing and error recording.
func (conn *connection) Get(ctx context.Context, key string, opts *OptionsGet) ([]byte, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "Get", conn.keyAttrs(key)...)
	defer op.End(ctx)

	// The value is copied out of the reply: the driver hands back bytes aliasing
	// the reply's string, which a caller writing to them would corrupt.
	value, err := conn.client.Do(ctx, conn.client.B().Get().Key(key).Build()).ToString()

	// A missing key is not a failure unless the caller says it is, but everything
	// else is: a reset connection, a timeout or an auth failure has to reach the
	// span whatever the options say. Gating all of them on the not-found option is
	// how a Valkey outage used to produce spans with no recorded error at all.
	if err != nil {
		if !valkey.IsValkeyNil(err) || (opts != nil && opts.ErrorRecordOnNotFound) {
			op.RecordError("failed to get key", err)
		}

		return nil, err
	}

	return []byte(value), nil
}

// Set writes bytes representation of the value, with some optional options.
//
// It automatically handles tracing and error recording.
func (conn *connection) Set(ctx context.Context, key string, value []byte, opts *OptionsSet) error {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "Set", conn.keyAttrs(key)...)
	defer op.End(ctx)

	err := conn.client.Do(ctx, buildSet(conn.client.B(), key, value, opts, false)).Error()
	if err != nil {
		op.RecordError("failed to set key", err)
	}

	return err
}

// SetNX sets key to value only if it does not already exist.
//
// It automatically handles tracing and error recording.
func (conn *connection) SetNX(ctx context.Context, key string, value []byte, opts *OptionsSet) (bool, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "SetNX", conn.keyAttrs(key)...)
	defer op.End(ctx)

	err := conn.client.Do(ctx, buildSet(conn.client.B(), key, value, opts, true)).Error()
	if err != nil {
		if valkey.IsValkeyNil(err) {
			return false, nil
		}

		op.RecordError("failed to set key if not exists", err)
		return false, err
	}

	return true, nil
}

// Increment increments the value of a key.
//
// It automatically handles tracing and error recording.
func (conn *connection) Increment(ctx context.Context, key string, increment int64) (int64, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "Increment", conn.keyAttrs(key)...)
	defer op.End(ctx)

	value, err := conn.client.Do(ctx, conn.client.B().Incrby().Key(key).Increment(increment).Build()).AsInt64()
	if err != nil {
		op.RecordError("failed to increment value", err)
		return 0, err
	}

	return value, nil
}

// Decrement decrements the value of a key.
//
// It automatically handles tracing and error recording.
func (conn *connection) Decrement(ctx context.Context, key string, decrement int64) (int64, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "Decrement", conn.keyAttrs(key)...)
	defer op.End(ctx)

	value, err := conn.client.Do(ctx, conn.client.B().Decrby().Key(key).Decrement(decrement).Build()).AsInt64()
	if err != nil {
		op.RecordError("failed to decrement value", err)
		return 0, err
	}

	return value, nil
}

// IncrementTTL increments the value of a key, and sets its time-to-live when it
// has none.
//
// It automatically handles tracing and error recording.
func (conn *connection) IncrementTTL(ctx context.Context, key string, increment int64, ttl time.Duration) (int64, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "IncrementTTL", conn.keyAttrs(key)...)
	defer op.End(ctx)

	if ttl <= 0 {
		err := errorstack.NewValidation(errorstack.Entry{
			Message: "Must be a positive duration",
			Path:    []any{"ttl"},
		})

		op.RecordError("invalid key expiration", err)
		return 0, err
	}

	// One round trip for both. Sent as two, a call whose context is cancelled once
	// the first is answered never sends the second, and leaves the key without
	// expiry.
	results := conn.client.DoMulti(ctx,
		conn.client.B().Incrby().Key(key).Increment(increment).Build(),
		conn.client.B().Pexpire().Key(key).Milliseconds(milliseconds(ttl)).Nx().Build(),
	)

	value, err := results[0].AsInt64()
	if err != nil {
		op.RecordError("failed to increment value", err)
		return 0, err
	}

	if err := results[1].Error(); err != nil {
		op.RecordError("failed to set key expiration", err)
		return 0, err
	}

	return value, nil
}

// CompareAndSet sets key to value only if it holds expected.
//
// It automatically handles tracing and error recording.
func (conn *connection) CompareAndSet(ctx context.Context, key string, expected, value []byte, ttl time.Duration) (bool, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "CompareAndSet", conn.keyAttrs(key)...)
	defer op.End(ctx)

	var ms int64
	if ttl > 0 {
		ms = milliseconds(ttl)
	}

	// Copied for the reason buildSet copies a value.
	args := []string{string(expected), string(value), strconv.FormatInt(ms, 10)}
	set, err := compareAndSetScript.lua().Exec(ctx, conn.client, []string{key}, args).AsInt64()
	if err != nil {
		op.RecordError("failed to compare and set key", err)
		return false, err
	}

	return set == 1, nil
}

// CompareAndDelete deletes key only if it holds expected.
//
// It automatically handles tracing and error recording.
func (conn *connection) CompareAndDelete(ctx context.Context, key string, expected []byte) (bool, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "CompareAndDelete", conn.keyAttrs(key)...)
	defer op.End(ctx)

	// Copied for the reason buildSet copies a value.
	args := []string{string(expected)}
	deleted, err := compareAndDeleteScript.lua().Exec(ctx, conn.client, []string{key}, args).AsInt64()
	if err != nil {
		op.RecordError("failed to compare and delete key", err)
		return false, err
	}

	return deleted == 1, nil
}

// Expire sets or refreshes the time-to-live of a key.
//
// It automatically handles tracing and error recording.
func (conn *connection) Expire(ctx context.Context, key string, ttl time.Duration) error {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "Expire", conn.keyAttrs(key)...)
	defer op.End(ctx)

	if ttl <= 0 {
		err := errorstack.NewValidation(errorstack.Entry{
			Message: "Must be a positive duration",
			Path:    []any{"ttl"},
		})

		op.RecordError("invalid key expiration", err)
		return err
	}

	cmd := conn.client.B().Pexpire().Key(key).Milliseconds(milliseconds(ttl))
	err := conn.client.Do(ctx, cmd.Build()).Error()
	if err != nil {
		op.RecordError("failed to set key expiration", err)
	}

	return err
}

// Scan looks for and returns the keys matching a pattern.
//
// It automatically handles tracing and error recording.
func (conn *connection) Scan(ctx context.Context, pattern string, opts *OptionsScan) ([]string, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "Scan", conn.patternAttrs(pattern)...)
	defer op.End(ctx)

	if pattern == "" {
		pattern = "*"
	}

	count := int64(defaultScanCount)
	var limit int
	if opts != nil {
		if opts.Count > 0 {
			count = opts.Count
		}

		limit = max(opts.Limit, 0)
	}

	// A SCAN carries no key, so a cluster client sends it to whichever node it
	// likes, and the result is that one shard's keys. Every node is scanned
	// instead. The driver does not say which nodes are replicas, so those are
	// scanned too, and a key seen twice — on a replica, or from a node rehashing
	// mid-scan, which SCAN allows — is kept once.
	nodes := conn.client.Nodes()
	seen := make(map[string]struct{})
	keys := make([]string, 0)
	for _, addr := range slices.Sorted(maps.Keys(nodes)) {
		node := nodes[addr]

		var cursor uint64
		for {
			cmd := node.B().Scan().Cursor(cursor).Match(pattern).Count(count)
			entry, err := node.Do(ctx, cmd.Build()).AsScanEntry()
			if err != nil {
				op.RecordError("failed to scan keys", err)
				return nil, err
			}

			for _, key := range entry.Elements {
				if _, ok := seen[key]; ok {
					continue
				}

				seen[key] = struct{}{}
				keys = append(keys, key)
				if limit > 0 && len(keys) == limit {
					return keys, nil
				}
			}

			cursor = entry.Cursor
			if cursor == 0 {
				break
			}
		}
	}

	return keys, nil
}

// MGet returns key/value pairs for the keys that exist.
//
// It automatically handles tracing and error recording.
func (conn *connection) MGet(ctx context.Context, keys []string) ([]Entry, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "MGet", conn.keyListAttrs(keys)...)
	defer op.End(ctx)

	if len(keys) == 0 {
		return []Entry{}, nil
	}

	// A single MGET across a cluster's hash slots panics in the command builder.
	// The driver's helper sends one MGET per slot instead, and one MGET in all
	// against a standalone server.
	values, err := valkey.MGet(conn.client, ctx, keys)
	if err != nil {
		op.RecordError("failed to get multiple keys", err)
		return nil, err
	}

	result := make([]Entry, 0, len(keys))
	for _, key := range keys {
		msg, ok := values[key]
		if !ok || msg.IsNil() {
			continue
		}

		value, err := msg.ToString()
		if err != nil {
			op.RecordError("failed to get multiple keys", err)
			return nil, err
		}

		result = append(result, Entry{
			Key:   key,
			Value: []byte(value),
		})
	}

	return result, nil
}

// Delete deletes a set of keys.
//
// It automatically handles tracing and error recording.
func (conn *connection) Delete(ctx context.Context, keys []string) error {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "Delete", conn.keyListAttrs(keys)...)
	defer op.End(ctx)

	if len(keys) == 0 {
		return nil
	}

	// A single DEL across a cluster's hash slots panics in the command builder,
	// like MGET. The driver's helper sends one DEL per key instead, pipelined, and
	// one DEL in all against a standalone server.
	results := valkey.MDel(conn.client, ctx, keys)
	for _, key := range keys {
		if err := results[key]; err != nil {
			op.RecordError("failed to delete keys", err)
			return err
		}
	}

	return nil
}

// SAdd adds members to a set, and refreshes its time-to-live when asked to.
//
// It automatically handles tracing and error recording.
func (conn *connection) SAdd(ctx context.Context, key string, members []string, opts *OptionsSAdd) (int64, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "SAdd", conn.keyAttrs(key)...)
	defer op.End(ctx)

	if len(members) == 0 {
		return 0, nil
	}

	add := conn.client.B().Sadd().Key(key).Member(members...).Build()
	if opts == nil || opts.TTL <= 0 {
		added, err := conn.client.Do(ctx, add).AsInt64()
		if err != nil {
			op.RecordError("failed to add set members", err)
			return 0, err
		}

		return added, nil
	}

	// One round trip for both, as IncrementTTL sends its two: sent apart, a call
	// whose context is cancelled once the first is answered never sends the
	// second, and leaves a set nobody will expire.
	results := conn.client.DoMulti(ctx,
		add,
		conn.client.B().Pexpire().Key(key).Milliseconds(milliseconds(opts.TTL)).Build(),
	)

	added, err := results[0].AsInt64()
	if err != nil {
		op.RecordError("failed to add set members", err)
		return 0, err
	}

	if err := results[1].Error(); err != nil {
		op.RecordError("failed to set key expiration", err)
		return 0, err
	}

	return added, nil
}

// SRem removes members from a set.
//
// It automatically handles tracing and error recording.
func (conn *connection) SRem(ctx context.Context, key string, members []string) (int64, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "SRem", conn.keyAttrs(key)...)
	defer op.End(ctx)

	if len(members) == 0 {
		return 0, nil
	}

	removed, err := conn.client.Do(ctx, conn.client.B().Srem().Key(key).Member(members...).Build()).AsInt64()
	if err != nil {
		op.RecordError("failed to remove set members", err)
		return 0, err
	}

	return removed, nil
}

// SMembers returns the members of a set.
//
// It automatically handles tracing and error recording.
func (conn *connection) SMembers(ctx context.Context, key string) ([]string, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "SMembers", conn.keyAttrs(key)...)
	defer op.End(ctx)

	members, err := conn.client.Do(ctx, conn.client.B().Smembers().Key(key).Build()).AsStrSlice()
	if err != nil {
		op.RecordError("failed to read set members", err)
		return nil, err
	}

	if members == nil {
		members = []string{}
	}

	return members, nil
}

// SScan returns a page of the members of a set, and the cursor of the next page.
//
// It automatically handles tracing and error recording.
func (conn *connection) SScan(ctx context.Context, key string, cursor uint64, opts *OptionsSScan) ([]string, uint64, error) {
	attrs := conn.keyAttrs(key)
	count := int64(defaultSScanCount)
	var match string
	if opts != nil {
		if opts.Count > 0 {
			count = opts.Count
		}

		match = opts.Match
		if match != "" {
			attrs = append(attrs, conn.patternAttrs(match)...)
		}
	}

	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "SScan", attrs...)
	defer op.End(ctx)

	// The set is one key, so a cluster client sends the scan to the node holding
	// it, and the cursor is that node's.
	scan := conn.client.B().Sscan().Key(key).Cursor(cursor)
	var cmd valkey.Completed
	if match != "" {
		cmd = scan.Match(match).Count(count).Build()
	} else {
		cmd = scan.Count(count).Build()
	}

	entry, err := conn.client.Do(ctx, cmd).AsScanEntry()
	if err != nil {
		op.RecordError("failed to scan set members", err)
		return nil, 0, err
	}

	members := entry.Elements
	if members == nil {
		members = []string{}
	}

	return members, entry.Cursor, nil
}

// SCard returns how many members a set holds.
//
// It automatically handles tracing and error recording.
func (conn *connection) SCard(ctx context.Context, key string) (int64, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "SCard", conn.keyAttrs(key)...)
	defer op.End(ctx)

	count, err := conn.client.Do(ctx, conn.client.B().Scard().Key(key).Build()).AsInt64()
	if err != nil {
		op.RecordError("failed to count set members", err)
		return 0, err
	}

	return count, nil
}

// Publish publishes a message to a channel for live fan-out to current subscribers.
//
// It automatically handles tracing and error recording.
func (conn *connection) Publish(ctx context.Context, channel string, message []byte) error {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindProducer, "Publish")
	defer op.End(ctx)

	conn.recordChannel(op.Span, channel)

	// Copied for the reason buildSet copies a value.
	cmd := conn.client.B().Publish().Channel(channel).Message(string(message))
	err := conn.client.Do(ctx, cmd.Build()).Error()
	if err != nil {
		op.RecordError("failed to publish message", err)
	}

	return err
}

// Subscribe subscribes to channel and calls handler for every message until ctx is
// done, through the connection's pub/sub hub (pubsub).
//
// It automatically handles tracing and error recording.
func (conn *connection) Subscribe(ctx context.Context, channel string, handler func(ctx context.Context, msg PubSubMessage)) error {
	return conn.subscribe(ctx, channel, nil, handler)
}

// SubscribeConfirmed is Subscribe, calling confirmed once the server holds the
// subscription. See ConfirmedSubscriber.
func (conn *connection) SubscribeConfirmed(ctx context.Context, channel string, confirmed func(), handler func(ctx context.Context, msg PubSubMessage)) error {
	return conn.subscribe(ctx, channel, confirmed, handler)
}

// subscribe subscribes to channel and calls handler for every message until ctx
// is done, and calls confirmed, when it is not nil, once the server has confirmed
// the subscription and before handler is first called.
func (conn *connection) subscribe(ctx context.Context, channel string, confirmed func(), handler func(ctx context.Context, msg PubSubMessage)) error {
	setupCtx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "Subscribe")
	conn.recordChannel(op.Span, channel)

	// What is measured is the setup, from joining the channel to the server
	// confirming its subscription, which a Subscribe joining a channel already
	// subscribed finds done. The subscription itself lasts as long as ctx does,
	// and as one operation it would swamp every duration recorded next to it.
	var setup sync.Once
	endSetup := func(err error) {
		setup.Do(func() {
			op.RecordError("failed to subscribe to channel", err)
			op.End(setupCtx)
		})
	}

	cs, sub, err := conn.pubsub.join(ctx, conn, channel)
	if err != nil {

		// ctx ended while a previous subscription of the channel was being ended:
		// how a subscription is meant to end, so neither a failure nor a failed
		// setup.
		endSetup(nil)
		return nil
	}

	defer conn.pubsub.leave(cs, sub)

	select {
	case <-cs.confirmed:
		endSetup(nil)

		// The server holds the subscription and sub is one of its members: a
		// message published after any command the caller sends from here on is
		// queued for sub.
		if confirmed != nil {
			confirmed()
		}
	case <-sub.ended:
		endSetup(sub.err)
		return sub.err
	case <-ctx.Done():
		endSetup(nil)
		return nil
	}

	for {
		select {
		case msg := <-sub.queue:
			sub.pending.Add(-int64(len(msg.Message)))
			conn.receive(sub.ctx, msg, handler)

		case <-sub.ended:
			return sub.err

		case <-ctx.Done():
			return nil
		}
	}
}

// receive hands one message to handler, under a consumer span of its own.
//
// The span is a child of the ctx the subscription was started with, not of the
// subscription's setup: the payload carries no trace context to continue, and the
// setup ended long before most messages arrive.
func (conn *connection) receive(ctx context.Context, msg valkey.PubSubMessage, handler func(context.Context, PubSubMessage)) {
	ctx, span := conn.inst.Start(ctx, trace.SpanKindConsumer, "Receive")
	defer span.End()

	conn.recordChannel(span, msg.Channel)

	handler(ctx, PubSubMessage{
		Channel: msg.Channel,
		Payload: []byte(msg.Message),
	})
}

// XAdd appends an entry of field/value pairs to a stream and returns the
// server-assigned entry id.
//
// It automatically handles tracing and error recording.
func (conn *connection) XAdd(ctx context.Context, stream string, maxLen int64, fields map[string]string) (string, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "XAdd", conn.keyAttrs(stream)...)
	defer op.End(ctx)

	if len(fields) == 0 {
		err := errorstack.NewValidation(errorstack.Entry{
			Message: "Must not be empty",
			Path:    []any{"fields"},
		})

		op.RecordError("invalid stream entry", err)
		return "", err
	}

	// Map iteration order is random, and an entry's fields are stored in the order
	// they are sent.
	names := slices.Sorted(maps.Keys(fields))

	var built valkey.Completed
	if maxLen > 0 {
		entry := conn.client.B().Xadd().Key(stream).Maxlen().Almost().Threshold(strconv.FormatInt(maxLen, 10)).Id("*").FieldValue()
		for _, name := range names {
			entry = entry.FieldValue(name, fields[name])
		}

		built = entry.Build()
	} else {
		entry := conn.client.B().Xadd().Key(stream).Id("*").FieldValue()
		for _, name := range names {
			entry = entry.FieldValue(name, fields[name])
		}

		built = entry.Build()
	}

	id, err := conn.client.Do(ctx, built).ToString()
	if err != nil {
		op.RecordError("failed to append to stream", err)
		return "", err
	}

	return id, nil
}

// XRange returns stream entries within the inclusive id range [start, end].
//
// It automatically handles tracing and error recording.
func (conn *connection) XRange(ctx context.Context, stream, start, end string, opts *OptionsXRange) ([]StreamEntry, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "XRange", conn.keyAttrs(stream)...)
	defer op.End(ctx)

	cmd := conn.client.B().Xrange().Key(stream).Start(start).End(end)

	var built valkey.Completed
	if opts != nil && opts.Count > 0 {
		built = cmd.Count(opts.Count).Build()
	} else {
		built = cmd.Build()
	}

	entries, err := conn.client.Do(ctx, built).AsXRange()
	if err != nil {
		op.RecordError("failed to read stream range", err)
		return nil, err
	}

	result := make([]StreamEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, StreamEntry{
			ID:     entry.ID,
			Fields: entry.FieldValues,
		})
	}

	return result, nil
}

// B returns the driver's command builder.
func (conn *connection) B() valkey.Builder {
	return conn.client.B()
}

// Do sends a command built from B.
//
// It automatically handles tracing and error recording.
func (conn *connection) Do(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "Do")
	defer op.End(ctx)

	// Read before Do: the driver recycles the command once it is sent.
	if name := cmd.Commands(); len(name) > 0 {
		op.SetAttributes(attrKeyCommand.String(name[0]))
	}

	result := conn.client.Do(ctx, cmd)
	if err := result.Error(); err != nil && !valkey.IsValkeyNil(err) {
		op.RecordError("failed to run command", err)
	}

	return result
}

// keyAttrs returns the attributes recording key on an operation's span, under
// Config.RecordKeys: nothing when keys are not recorded. They are handed to
// StartOperation, so that a sampler deciding on attributes sees them.
func (conn *connection) keyAttrs(key string) []attribute.KeyValue {
	if !conn.recordKeys {
		return nil
	}

	return []attribute.KeyValue{attrKeyKey.String(key)}
}

// keyListAttrs returns the attributes recording keys on an operation's span, under
// Config.RecordKeys.
func (conn *connection) keyListAttrs(keys []string) []attribute.KeyValue {
	if !conn.recordKeys {
		return nil
	}

	return []attribute.KeyValue{attrKeyKeys.StringSlice(keys)}
}

// patternAttrs returns the attributes recording a scan pattern on an operation's
// span, under Config.RecordKeys: a pattern is as likely as a key to carry an
// identifier.
func (conn *connection) patternAttrs(pattern string) []attribute.KeyValue {
	if !conn.recordKeys {
		return nil
	}

	return []attribute.KeyValue{attrKeyPattern.String(pattern)}
}

// recordChannel records channel on span, under Config.RecordKeys: a channel name
// is as likely as a key to carry an identifier.
func (conn *connection) recordChannel(span *trace.Span, channel string) {
	if conn.recordKeys {
		span.SetAttributes(attrKeyChannel.String(channel))
	}
}
