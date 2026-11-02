package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Tx is a database transaction, returned by PostgreSQL.BeginTx. It holds one
// connection from the pool until Commit or Rollback, and every statement it runs
// runs on that connection.
//
// A Tx is not safe for concurrent use, and neither are the Rows and
// BatchResults it returns: the next statement can only be sent once they are
// closed. Like PostgreSQL, its errors are the driver's own, unwrapped.
type Tx interface {

	// Begin starts a pseudo nested transaction, implemented with a savepoint.
	// Committing it releases the savepoint; rolling it back rolls back to it,
	// leaving the outer transaction usable.
	Begin(ctx context.Context) (Tx, error)

	// Commit commits the transaction, or releases the savepoint of a nested one.
	// A transaction the server has already aborted — after a failed statement —
	// is rolled back instead, and Commit returns pgx.ErrTxCommitRollback.
	Commit(ctx context.Context) error

	// Rollback rolls the transaction back, or rolls back to the savepoint of a
	// nested one. It returns pgx.ErrTxClosed if the Tx is already closed but is
	// otherwise safe to call more than once, so a deferred Rollback is safe even
	// when Commit is called first.
	Rollback(ctx context.Context) error

	// Exec executes a statement that returns no rows, and returns its command
	// tag. See PostgreSQL.Exec.
	Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error)

	// Prepare creates a prepared statement named id from query on the
	// transaction's connection. id can then be passed in place of SQL to Exec,
	// Query and QueryRow. Preparing the same id and query again is a no-op.
	Prepare(ctx context.Context, id string, query string) (*pgconn.StatementDescription, error)

	// Query sends a query and returns the Rows to read its result from. The Rows
	// must be closed before the transaction runs anything else. See
	// PostgreSQL.Query.
	Query(ctx context.Context, query string, args ...any) (pgx.Rows, error)

	// QueryRow is a convenience wrapper over Query for a query that returns at
	// most one row. See PostgreSQL.QueryRow.
	QueryRow(ctx context.Context, query string, args ...any) pgx.Row

	// SendBatch sends every statement queued in batch in one round trip, inside
	// the transaction. The returned BatchResults must be closed before the
	// transaction runs anything else. See PostgreSQL.SendBatch.
	SendBatch(ctx context.Context, batch *pgx.Batch) pgx.BatchResults

	// CopyFrom bulk-loads rows with the COPY protocol, inside the transaction.
	// See PostgreSQL.CopyFrom.
	CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error)
}

// transaction implements the Tx interface, labelling each call with the name the
// driver's tracer gives its span.
//
// It holds no Config and opens no span of its own: pgx implements Begin, Commit,
// Rollback and savepoints by running SQL, so every method below reaches the
// tracer through the same hooks an ordinary statement does.
type transaction struct {
	client pgx.Tx
}

// Begin starts a nested transaction. See Tx.
func (tx *transaction) Begin(ctx context.Context) (Tx, error) {
	subtx, err := tx.client.Begin(withOperation(ctx, "Transaction / Begin"))
	if err != nil {
		return nil, err
	}

	return &transaction{client: subtx}, nil
}

// Commit commits the transaction. See Tx.
func (tx *transaction) Commit(ctx context.Context) error {
	return tx.client.Commit(withOperation(ctx, "Transaction / Commit"))
}

// Rollback rolls the transaction back. See Tx.
func (tx *transaction) Rollback(ctx context.Context) error {
	return tx.client.Rollback(withOperation(ctx, "Transaction / Rollback"))
}

// Exec executes a statement. See Tx.
func (tx *transaction) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return tx.client.Exec(withOperation(ctx, "Transaction / Exec"), query, args...)
}

// Prepare creates a prepared statement. See Tx.
func (tx *transaction) Prepare(ctx context.Context, id string, query string) (*pgconn.StatementDescription, error) {
	return tx.client.Prepare(withPrepare(ctx, "Transaction / Prepare"), id, query)
}

// Query sends a query and returns its Rows. See Tx.
func (tx *transaction) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	return tx.client.Query(withOperation(ctx, "Transaction / Query"), query, args...)
}

// QueryRow sends a query and returns its one Row. See Tx.
func (tx *transaction) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return tx.client.QueryRow(withOperation(ctx, "Transaction / QueryRow"), query, args...)
}

// SendBatch sends a batch of statements. See Tx.
func (tx *transaction) SendBatch(ctx context.Context, batch *pgx.Batch) pgx.BatchResults {
	return tx.client.SendBatch(withOperation(ctx, "Transaction / Batch"), batch)
}

// CopyFrom bulk-loads rows with the COPY protocol. See Tx.
func (tx *transaction) CopyFrom(
	ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource,
) (int64, error) {
	return tx.client.CopyFrom(withOperation(ctx, "Transaction / Copy From"), tableName, columnNames, rowSrc)
}
