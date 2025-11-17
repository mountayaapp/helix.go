package valkey

import (
	"context"
	"fmt"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/service"
	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/valkey-io/valkey-go"
)

/*
Entry represents a key/value pair in Valkey.
*/
type Entry struct {
	Key   string `json:"key"`
	Value []byte `json:"value"`
}

/*
Valkey exposes an opinionated way to interact with Valkey, by bringing automatic
distributed tracing as well as error recording within traces.
*/
type Valkey interface {
	Get(ctx context.Context, key string, opts *OptionsGet) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, opts *OptionsSet) error
	Increment(ctx context.Context, key string, increment int64) error
	Decrement(ctx context.Context, key string, decrement int64) error
	Scan(ctx context.Context, pattern string) []string
	MGet(ctx context.Context, keys []string) []Entry
	Delete(ctx context.Context, keys []string) error
}

/*
connection represents the valkey integration. It respects the integration.Integration
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
func Connect(cfg Config) (Valkey, error) {

	// No need to continue if Config is not valid.
	err := cfg.sanitize()
	if err != nil {
		return nil, err
	}

	// Start to build an error stack, so we can add validations as we go.
	stack := errorstack.New("Failed to initialize integration", errorstack.WithIntegration(identifier))
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
		var validations []errorstack.Validation

		opts.TLSConfig, validations = cfg.TLS.ToStandardTLS()
		if len(validations) > 0 {
			stack.WithValidations(validations...)
		}
	}

	// Try to connect to the Valkey database.
	conn.client, err = valkey.NewClient(opts)
	if err != nil {
		stack.WithValidations(errorstack.Validation{
			Message: err.Error(),
		})
	}

	// Stop here if error validations were encountered.
	if stack.HasValidations() {
		return nil, stack
	}

	// Try to attach the integration to the service.
	if err := service.Attach(conn); err != nil {
		return nil, err
	}

	return conn, nil
}

/*
Get reads the value at key and returns its byte representation.

It automatically handles tracing and error recording.
*/
func (conn *connection) Get(ctx context.Context, key string, opts *OptionsGet) ([]byte, error) {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, fmt.Sprintf("%s: Get", humanized))
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
	ctx, span := trace.Start(ctx, trace.SpanKindClient, fmt.Sprintf("%s: Set", humanized))
	defer span.End()

	cmd := conn.client.B().Set().Key(key).Value(string(value))
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
	ctx, span := trace.Start(ctx, trace.SpanKindClient, fmt.Sprintf("%s: Increment", humanized))
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
	ctx, span := trace.Start(ctx, trace.SpanKindClient, fmt.Sprintf("%s: Decrement", humanized))
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
Scan look for and return all keys given a pattern.

It automatically handles tracing.
*/
func (conn *connection) Scan(ctx context.Context, pattern string) []string {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, fmt.Sprintf("%s: Scan", humanized))
	defer span.End()

	var cursor uint64
	var keys []string
	for {
		batch := conn.client.Do(ctx, conn.client.B().Scan().Cursor(cursor).Match(pattern).Build())
		se, _ := batch.AsScanEntry()

		keys = append(keys, se.Elements...)

		cursor = se.Cursor
		if cursor == 0 {
			break
		}
	}

	return keys
}

/*
MGet returns key/value pairs for all keys passed.

It automatically handles tracing.
*/
func (conn *connection) MGet(ctx context.Context, keys []string) []Entry {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, fmt.Sprintf("%s: MGet", humanized))
	defer span.End()

	if len(keys) == 0 {
		return []Entry{}
	}

	values := conn.client.Do(ctx, conn.client.B().Mget().Key(keys...).Build())
	sse, _ := values.AsStrSlice()

	result := make([]Entry, 0, len(keys))
	for i, key := range keys {
		if i >= len(sse) {
			break
		}

		val := sse[i]
		if val == "" {
			continue
		}

		record := Entry{
			Key:   key,
			Value: []byte(val),
		}

		result = append(result, record)
	}

	return result
}

/*
Delete deletes a set of keys.

It automatically handles tracing and error recording.
*/
func (conn *connection) Delete(ctx context.Context, keys []string) error {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, fmt.Sprintf("%s: Delete", humanized))
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
