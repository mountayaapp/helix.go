package valkey

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service"
	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/valkey-io/valkey-go"
)

/*
Pre-computed span names to avoid allocations on every call.
*/
const (
	spanExists    = humanized + ": Exists"
	spanGet       = humanized + ": Get"
	spanSet       = humanized + ": Set"
	spanIncrement = humanized + ": Increment"
	spanDecrement = humanized + ": Decrement"
	spanExpire    = humanized + ": Expire"
	spanScan      = humanized + ": Scan"
	spanMGet      = humanized + ": MGet"
	spanDelete    = humanized + ": Delete"
	spanPublish   = humanized + ": Publish"
	spanSubscribe = humanized + ": Subscribe"
	spanXAdd      = humanized + ": XAdd"
	spanXRange    = humanized + ": XRange"
	spanSetNX     = humanized + ": SetNX"
)

/*
Entry represents a key/value pair in Valkey.
*/
type Entry struct {
	Key   string `json:"key"`
	Value []byte `json:"value"`
}

/*
PubSubMessage is a single message received on a subscribed channel.
*/
type PubSubMessage struct {
	Channel string `json:"channel"`
	Payload []byte `json:"payload"`
}

/*
StreamEntry is a single entry read from a stream, identified by its server-assigned
id and carrying the field/value pairs of the entry.
*/
type StreamEntry struct {
	Id     string            `json:"id"`
	Fields map[string]string `json:"fields"`
}

/*
Valkey exposes an opinionated way to interact with Valkey, by bringing automatic
distributed tracing as well as error recording within traces.
*/
type Valkey interface {
	Exists(ctx context.Context, key string) (bool, error)
	Get(ctx context.Context, key string, opts *OptionsGet) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, opts *OptionsSet) error
	Increment(ctx context.Context, key string, increment int64) error
	Decrement(ctx context.Context, key string, decrement int64) error
	Expire(ctx context.Context, key string, ttl time.Duration) error
	Scan(ctx context.Context, pattern string) ([]string, error)
	MGet(ctx context.Context, keys []string) ([]Entry, error)
	Delete(ctx context.Context, keys []string) error
	SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
	Publish(ctx context.Context, channel string, message []byte) error
	Subscribe(ctx context.Context, channel string, handler func(PubSubMessage)) error
	XAdd(ctx context.Context, stream string, maxLen int64, fields map[string]string) (string, error)
	XRange(ctx context.Context, stream, start, end string) ([]StreamEntry, error)
}

/*
connection represents the valkey integration. It respects the integration.Dependency
and Valkey interfaces.
*/
type connection struct {

	// config holds the Config initially passed when creating a new Valkey client.
	config *Config

	// client is the connection made with the Valkey client.
	client valkey.Client
}

/*
Connect tries to create a Valkey client given the Config. Returns an error if
Config is not valid or if the initialization failed.
*/
func Connect(svc *service.Service, cfg Config) (Valkey, error) {

	// No need to continue if Config is not valid.
	err := cfg.sanitize()
	if err != nil {
		return nil, err
	}

	// Collect validation entries as we go, then build a single Validation error.
	var entries []errorstack.Entry
	conn := &connection{
		config: &cfg,
	}

	// Set the default Valkey config.
	var opts = valkey.ClientOption{
		InitAddress: []string{cfg.Address},
		Username:    cfg.User,
		Password:    cfg.Password,
	}

	// Set TLS options only if enabled in Config.
	if cfg.TLS.Enabled {
		var tlsEntries []errorstack.Entry
		opts.TLSConfig, tlsEntries = cfg.TLS.ToStandardTLS()
		entries = append(entries, tlsEntries...)
	}

	// Try to connect to the Valkey database.
	conn.client, err = valkey.NewClient(opts)
	if err != nil {
		entries = append(entries, errorstack.Entry{
			Message: integration.NormalizeErrorMessage(err),
			Path:    []any{"config"},
		})
	}

	// Stop here if validation entries were collected.
	if len(entries) > 0 {
		return nil, errorstack.NewValidation(entries...)
	}

	// Try to attach the integration to the service.
	if err := service.Attach(svc, conn); err != nil {
		return nil, err
	}

	return conn, nil
}

/*
Exists checks if a key exists in Valkey.

It automatically handles tracing and error recording.
*/
func (conn *connection) Exists(ctx context.Context, key string) (bool, error) {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanExists)
	defer span.End()

	cmd := conn.client.B().Exists().Key(key)
	count, err := conn.client.Do(ctx, cmd.Build()).AsInt64()
	if err != nil {
		span.RecordError("failed to check key existence", err)
	}

	setKeyAttributes(span, key)

	return count > 0, err
}

/*
Get reads the value at key and returns its byte representation.

It automatically handles tracing and error recording.
*/
func (conn *connection) Get(ctx context.Context, key string, opts *OptionsGet) ([]byte, error) {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanGet)
	defer span.End()

	cmd := conn.client.B().Get().Key(key)

	value, err := conn.client.Do(ctx, cmd.Build()).AsBytes()
	if err != nil {
		if opts != nil && opts.ErrorRecordOnNotFound {
			span.RecordError("failed to get key", err)
		}
	}

	setKeyAttributes(span, key)

	return value, err
}

/*
Set writes bytes representation of the value, with some optional options.

It automatically handles tracing and error recording.
*/
func (conn *connection) Set(ctx context.Context, key string, value []byte, opts *OptionsSet) error {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanSet)
	defer span.End()

	cmd := conn.client.B().Set().Key(key).Value(bytesToString(value))
	if opts != nil && opts.TTL > 0 {
		cmd.Ex(opts.TTL)
	}

	err := conn.client.Do(ctx, cmd.Build()).Error()
	if err != nil {
		span.RecordError("failed to set key", err)
	}

	setKeyAttributes(span, key)

	return err
}

/*
Increment increments the value of a key.

It automatically handles tracing and error recording.
*/
func (conn *connection) Increment(ctx context.Context, key string, increment int64) error {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanIncrement)
	defer span.End()

	cmd := conn.client.B().Incrby().Key(key).Increment(increment)
	err := conn.client.Do(ctx, cmd.Build()).Error()
	if err != nil {
		span.RecordError("failed to increment value", err)
	}

	setKeyAttributes(span, key)

	return err
}

/*
Decrement decrements the value of a key.

It automatically handles tracing and error recording.
*/
func (conn *connection) Decrement(ctx context.Context, key string, decrement int64) error {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanDecrement)
	defer span.End()

	cmd := conn.client.B().Decrby().Key(key).Decrement(decrement)
	err := conn.client.Do(ctx, cmd.Build()).Error()
	if err != nil {
		span.RecordError("failed to decrement value", err)
	}

	setKeyAttributes(span, key)

	return err
}

/*
Expire sets or refreshes the time-to-live of a key. Subsequent calls on the
same key reset the TTL relative to the new call time; this is the natural fit
for fixed-window rate limiters where the key is always re-created at the start
of a new window.

It automatically handles tracing and error recording.
*/
func (conn *connection) Expire(ctx context.Context, key string, ttl time.Duration) error {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanExpire)
	defer span.End()

	seconds := int64(ttl.Seconds())
	if seconds <= 0 {
		seconds = 1
	}

	cmd := conn.client.B().Expire().Key(key).Seconds(seconds)
	err := conn.client.Do(ctx, cmd.Build()).Error()
	if err != nil {
		span.RecordError("failed to set key expiration", err)
	}

	setKeyAttributes(span, key)

	return err
}

/*
Scan looks for and returns all keys matching a pattern.

It automatically handles tracing and error recording.
*/
func (conn *connection) Scan(ctx context.Context, pattern string) ([]string, error) {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanScan)
	defer span.End()

	var cursor uint64
	var keys []string
	var scanErr error
	for {
		batch := conn.client.Do(ctx, conn.client.B().Scan().Cursor(cursor).Match(pattern).Build())
		se, err := batch.AsScanEntry()
		if err != nil {
			span.RecordError("failed to scan keys", err)
			scanErr = err
			break
		}

		keys = append(keys, se.Elements...)

		cursor = se.Cursor
		if cursor == 0 {
			break
		}
	}

	return keys, scanErr
}

/*
MGet returns key/value pairs for all keys passed.

It automatically handles tracing and error recording.
*/
func (conn *connection) MGet(ctx context.Context, keys []string) ([]Entry, error) {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanMGet)
	defer span.End()

	if len(keys) == 0 {
		return []Entry{}, nil
	}

	values := conn.client.Do(ctx, conn.client.B().Mget().Key(keys...).Build())
	sse, err := values.AsStrSlice()
	if err != nil {
		span.RecordError("failed to get multiple keys", err)
		return nil, err
	}

	result := make([]Entry, 0, len(keys))
	for i, key := range keys {
		if i >= len(sse) {
			break
		}

		val := sse[i]
		if val == "" {
			continue
		}

		result = append(result, Entry{
			Key:   key,
			Value: []byte(val),
		})
	}

	return result, nil
}

/*
Delete deletes a set of keys.

It automatically handles tracing and error recording.
*/
func (conn *connection) Delete(ctx context.Context, keys []string) error {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanDelete)
	defer span.End()

	if len(keys) == 0 {
		return nil
	}

	cmd := conn.client.B().Del().Key(keys...)
	err := conn.client.Do(ctx, cmd.Build()).Error()
	if err != nil {
		span.RecordError("failed to delete keys", err)
	}

	return err
}

/*
SetNX sets key to value only if it does not already exist, with an optional TTL.
Returns true when the value was set (the caller won the race) and false when the
key already existed. A pre-existing key is not treated as an error.

It automatically handles tracing and error recording.
*/
func (conn *connection) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanSetNX)
	defer span.End()

	cmd := conn.client.B().Set().Key(key).Value(bytesToString(value)).Nx()
	if ttl > 0 {
		cmd.Ex(ttl)
	}

	setKeyAttributes(span, key)

	err := conn.client.Do(ctx, cmd.Build()).Error()
	if err != nil {
		if errors.Is(err, valkey.Nil) {
			return false, nil
		}

		span.RecordError("failed to set key if not exists", err)
		return false, err
	}

	return true, nil
}

/*
Publish publishes a message to a channel for live fan-out to current subscribers.

It automatically handles tracing and error recording.
*/
func (conn *connection) Publish(ctx context.Context, channel string, message []byte) error {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanPublish)
	defer span.End()

	cmd := conn.client.B().Publish().Channel(channel).Message(bytesToString(message))
	err := conn.client.Do(ctx, cmd.Build()).Error()
	if err != nil {
		span.RecordError("failed to publish message", err)
	}

	return err
}

/*
Subscribe subscribes to a channel and invokes handler for every message until ctx
is cancelled. It blocks for the lifetime of the subscription on a dedicated
connection, so callers run it in a goroutine and cancel ctx to tear it down. A
cancellation is normal teardown and is not recorded as an error.

It automatically handles tracing and error recording.
*/
func (conn *connection) Subscribe(ctx context.Context, channel string, handler func(PubSubMessage)) error {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanSubscribe)
	defer span.End()

	cmd := conn.client.B().Subscribe().Channel(channel).Build()
	err := conn.client.Receive(ctx, cmd, func(msg valkey.PubSubMessage) {
		handler(PubSubMessage{
			Channel: msg.Channel,
			Payload: []byte(msg.Message),
		})
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		span.RecordError("failed to subscribe to channel", err)
		return err
	}

	return nil
}

/*
XAdd appends an entry of field/value pairs to a stream and returns the
server-assigned entry id. When maxLen is greater than zero the stream is capped to
approximately that many entries (MAXLEN ~).

It automatically handles tracing and error recording.
*/
func (conn *connection) XAdd(ctx context.Context, stream string, maxLen int64, fields map[string]string) (string, error) {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanXAdd)
	defer span.End()

	setKeyAttributes(span, stream)

	var built valkey.Completed
	if maxLen > 0 {
		entry := conn.client.B().Xadd().Key(stream).Maxlen().Almost().Threshold(strconv.FormatInt(maxLen, 10)).Id("*").FieldValue()
		for field, value := range fields {
			entry = entry.FieldValue(field, value)
		}

		built = entry.Build()
	} else {
		entry := conn.client.B().Xadd().Key(stream).Id("*").FieldValue()
		for field, value := range fields {
			entry = entry.FieldValue(field, value)
		}

		built = entry.Build()
	}

	id, err := conn.client.Do(ctx, built).ToString()
	if err != nil {
		span.RecordError("failed to append to stream", err)
	}

	return id, err
}

/*
XRange returns stream entries within the inclusive id range [start, end]. Pass "-"
and "+" for the full range, or an exclusive "(id" start to resume after a known id.

It automatically handles tracing and error recording.
*/
func (conn *connection) XRange(ctx context.Context, stream, start, end string) ([]StreamEntry, error) {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanXRange)
	defer span.End()

	setKeyAttributes(span, stream)

	cmd := conn.client.B().Xrange().Key(stream).Start(start).End(end)
	entries, err := conn.client.Do(ctx, cmd.Build()).AsXRange()
	if err != nil {
		span.RecordError("failed to read stream range", err)
		return nil, err
	}

	result := make([]StreamEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, StreamEntry{
			Id:     entry.ID,
			Fields: entry.FieldValues,
		})
	}

	return result, nil
}
