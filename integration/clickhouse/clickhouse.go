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
	spanSelect      = humanized + ": Select"
)

/*
ClickHouse exposes an opinionated way to interact with ClickHouse, by bringing
automatic distributed tracing as well as error recording within traces.
*/
type ClickHouse interface {
	NewBatchInsert(ctx context.Context, table string) (Batch, error)
	AsyncInsertStruct(ctx context.Context, table string, value any) error
	Select(ctx context.Context, dest any, query string, args ...any) error
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

	// Collect validation entries as we go, then build a single Validation error.
	var entries []errorstack.Entry
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
		var tlsEntries []errorstack.Entry
		opts.TLS, tlsEntries = cfg.TLS.ToStandardTLS()
		entries = append(entries, tlsEntries...)
	}

	// Try to connect to the ClickHouse database.
	conn.client, err = clickhouse.Open(opts)
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

Intended for high-frequency, low-latency per-request writes such as request
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

/*
Select runs a read query against ClickHouse and scans the result set into dest,
which must be a pointer to a slice of structs whose exported fields carry `ch`
struct tags matching the selected columns. Pass query arguments as variadic args
so the driver binds them as parameters rather than interpolating into the SQL.

Intended for analytical reads such as aggregations over large datasets. For
writes, use NewBatchInsert or AsyncInsertStruct.

It automatically handles tracing and error recording.
*/
func (conn *connection) Select(ctx context.Context, dest any, query string, args ...any) error {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanSelect)
	defer span.End()

	setDefaultAttributes(span, conn.config)

	if err := conn.client.Select(ctx, dest, query, args...); err != nil {
		span.RecordError("failed to select", err)
		return err
	}

	return nil
}
