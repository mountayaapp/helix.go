package postgres

import (
	"context"

	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

/*
Pre-computed span names to avoid allocations on every call.
*/
const (
	spanTxBegin    = humanized + ": Transaction / Begin"
	spanTxCommit   = humanized + ": Transaction / Commit"
	spanTxRollback = humanized + ": Transaction / Rollback"
	spanTxExec     = humanized + ": Transaction / Exec"
	spanTxPrepare  = humanized + ": Transaction / Prepare"
	spanTxQuery    = humanized + ": Transaction / QueryRows"
	spanTxQueryRow = humanized + ": Transaction / QueryRow"
)

/*
transaction implements the Tx interface and allows to wrap the PostgreSQL transaction
functions for automatic tracing and error recording.
*/
type transaction struct {
	config *Config
	client pgx.Tx
}

/*
Tx represents a database transaction.
*/
type Tx interface {
	Begin(ctx context.Context) (Tx, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
	Exec(ctx context.Context, query string, args ...any) (commandTag pgconn.CommandTag, err error)
	Prepare(ctx context.Context, id string, query string) (*pgconn.StatementDescription, error)
	Query(ctx context.Context, query string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) pgx.Row
}

/*
Begin starts a pseudo nested transaction implemented with a savepoint.

It automatically handles tracing and error recording.
*/
func (tx *transaction) Begin(ctx context.Context) (Tx, error) {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanTxBegin)
	defer span.End()

	subtx, err := tx.client.Begin(ctx)
	if err != nil {
		span.RecordError("failed to begin transaction", err)
	}

	setDefaultAttributes(span, tx.config)

	sub := &transaction{
		config: tx.config,
		client: subtx,
	}

	return sub, err
}

/*
Commit commits the transaction.

It automatically handles tracing and error recording.
*/
func (tx *transaction) Commit(ctx context.Context) error {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanTxCommit)
	defer span.End()

	err := tx.client.Commit(ctx)
	if err != nil {
		span.RecordError("failed to commit transaction", err)
	}

	setDefaultAttributes(span, tx.config)

	return err
}

/*
Rollback rolls back the transaction. Rollback will return pgx.ErrTxClosed if the
Tx is already closed, but is otherwise safe to call multiple times. Hence, a
defer tx.Rollback() is safe even if tx.Commit() will be called first in a non-error
condition.

It automatically handles tracing and error recording.
*/
func (tx *transaction) Rollback(ctx context.Context) error {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanTxRollback)
	defer span.End()

	err := tx.client.Rollback(ctx)
	if err != nil {
		span.RecordError("failed to rollback transaction", err)
	}

	setDefaultAttributes(span, tx.config)

	return err
}

/*
Exec delegates to the underlying connection.

It automatically handles tracing and error recording.
*/
func (tx *transaction) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanTxExec)
	defer span.End()

	stmt, err := tx.client.Exec(ctx, query, args...)
	if err != nil {
		span.RecordError("failed to execute query", err)
	}

	setDefaultAttributes(span, tx.config)
	setTransactionQueryAttributes(span, query)

	return stmt, err
}

/*
Prepare delegates to the underlying connection.

It automatically handles tracing and error recording.
*/
func (tx *transaction) Prepare(ctx context.Context, id string, query string) (*pgconn.StatementDescription, error) {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanTxPrepare)
	defer span.End()

	stmt, err := tx.client.Prepare(ctx, id, query)
	if err != nil {
		span.RecordError("failed to prepare statement", err)
	}

	setDefaultAttributes(span, tx.config)
	setTransactionQueryAttributes(span, query)

	return stmt, err
}

/*
Query delegates to the underlying connection.

It automatically handles tracing and error recording.
*/
func (tx *transaction) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanTxQuery)
	defer span.End()

	rows, err := tx.client.Query(ctx, query, args...)
	if err != nil {
		span.RecordError("failed to query rows", err)
	}

	setDefaultAttributes(span, tx.config)
	setTransactionQueryAttributes(span, query)

	return rows, err
}

/*
QueryRow delegates to the underlying connection.

It automatically handles tracing.
*/
func (tx *transaction) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	ctx, span := trace.Start(ctx, trace.SpanKindClient, spanTxQueryRow)
	defer span.End()

	row := tx.client.QueryRow(ctx, query, args...)

	setDefaultAttributes(span, tx.config)
	setTransactionQueryAttributes(span, query)

	return row
}
