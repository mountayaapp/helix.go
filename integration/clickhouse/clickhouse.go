package clickhouse

import (
	"context"
	"fmt"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service"
	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/ClickHouse/clickhouse-go/v2"
)

/*
Pre-computed span names to avoid allocations on every call.
*/
const (
	spanBatchBegin  = humanized + ": Batch / Begin"
	spanAsyncInsert = humanized + ": Async Insert"
)

/*
ClickHouse exposes an opinionated way to interact with ClickHouse, by bringing
automatic distributed tracing as well as error recording within traces.
*/
type ClickHouse interface {
	NewBatchInsert(ctx context.Context, table string) (Batch, error)
	AsyncInsertStruct(ctx context.Context, table string, value any) error
}

/*
connection represents the clickhouse integration. It respects the
integration.Dependency and ClickHouse interfaces.
*/
type connection struct {

	// config holds the Config initially passed when creating a new ClickHouse client.
	config *Config

	// client is the connection made with the ClickHouse database.
	client clickhouse.Conn
}

/*
Connect tries to connect to the ClickHouse database given the Config. Returns an
error if Config is not valid or if the connection failed.
*/
func Connect(svc *service.Service, cfg Config) (ClickHouse, error) {

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

	// Set the default ClickHouse options.
	opts := &clickhouse.Options{
		Addr: []string{cfg.Address},
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.User,
			Password: cfg.Password,
		},
	}

	// Set TLS options only if enabled in Config.
	if cfg.TLS.Enabled {
		var validations []errorstack.Validation

		opts.TLS, validations = cfg.TLS.ToStandardTLS()
		if len(validations) > 0 {
			stack.WithValidations(validations...)
		}
	}

	// Try to connect to the ClickHouse database.
	conn.client, err = clickhouse.Open(opts)
	if err != nil {
		stack.WithValidations(errorstack.Validation{
			Message: integration.NormalizeErrorMessage(err),
		})
	}

	// Stop here if error validations were encountered.
	if stack.HasValidations() {
		return nil, stack
	}

	// Try to attach the integration to the service.
	if err := service.Attach(svc, conn); err != nil {
		return nil, err
	}

	return conn, nil
}

/*
NewBatchInsert starts a new transaction for inserting batch data into a table.

It automatically handles tracing and error recording.
*/
func (conn *connection) NewBatchInsert(ctx context.Context, table string) (Batch, error) {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanBatchBegin)

	client, err := conn.client.PrepareBatch(ctx, fmt.Sprintf("INSERT INTO %s", table))
	if err != nil {
		span.RecordError("failed to begin batch", err)
	}

	setDefaultAttributes(span, conn.config)

	b := &batch{
		config:     conn.config,
		client:     client,
		parentSpan: span,
	}

	return b, err
}

/*
AsyncInsertStruct inserts a single struct row into the given table using
ClickHouse's native async_insert mechanism. The server buffers incoming rows
and flushes them in the background, so this call does not pay the
prepare/append/send roundtrip cost of a batch insert. The call returns as soon
as the server acknowledges receipt; it does not wait for the flush.

Intended for high-frequency, low-latency per-request writes such as usage
counters. For bulk loads, prefer NewBatchInsert. Server-side buffering is
governed by `async_insert_max_data_size`, `async_insert_busy_timeout_ms`, and
`async_insert_max_query_number`.

It automatically handles tracing and error recording.
*/
func (conn *connection) AsyncInsertStruct(ctx context.Context, table string, value any) error {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanAsyncInsert)
	defer span.End()

	setDefaultAttributes(span, conn.config)
	span.SetAttributes(attrKeyTable.String(table))

	query, args, err := prepareAsyncInsert(table, value)
	if err != nil {
		span.RecordError("failed to prepare async insert", err)
		return err
	}

	// WithAsync(false): the server buffers the row and we do not block waiting
	// for the flush. This is the recommended async insert API as of
	// clickhouse-go v2.
	asyncCtx := clickhouse.Context(ctx, clickhouse.WithAsync(false))
	if err := conn.client.Exec(asyncCtx, query, args...); err != nil {
		span.RecordError("failed to async insert struct", err)
		return err
	}

	return nil
}
