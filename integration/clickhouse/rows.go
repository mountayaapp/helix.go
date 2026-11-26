package clickhouse

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mountaya/helix.go/integration"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// rows wraps the driver's rows so that the Query span ends once the result has
// been read, rather than when its first block arrived: the driver streams the
// rest while the caller iterates, and the server's counters for the query arrive
// with it.
//
// It embeds the driver's rows, so the methods it does not override — and any the
// driver adds — are the driver's own.
type rows struct {
	driver.Rows

	// ctx is the context the span lives in, carried to End so that the
	// measurement's exemplar points back at the span. It is a context held in a
	// struct, which is ordinarily wrong; rows are a scope that outlives the call
	// that opened them, which is the exception.
	ctx     context.Context
	op      *integration.Operation
	profile *profile
	ended   bool
}

// Next advances to the next row. When it reports false the driver has already
// closed the rows, so the span ends here, with whatever error ended the
// iteration.
func (r *rows) Next() bool {
	if r.Rows.Next() {
		return true
	}

	r.end(r.Err())
	return false
}

// Close closes the rows and ends the span, unless Next already has.
func (r *rows) Close() error {
	err := r.Rows.Close()
	r.end(err)

	return err
}

// end records the result's outcome and counters, and ends the span. Only the
// first call does anything, so that Close after an exhausted Next is free.
func (r *rows) end(err error) {
	if r.ended {
		return
	}

	r.ended = true
	r.profile.stamp(r.op.Span)
	r.op.RecordError("failed to read rows", err)
	r.op.End(r.ctx)
}

// row wraps the driver's row so that the QueryRow span ends once the row has
// been scanned, which is when the driver reads it and releases the connection.
type row struct {
	driver.Row

	// ctx is held for the same reason, and with the same caveat, as on rows.
	ctx     context.Context
	op      *integration.Operation
	profile *profile
	ended   bool
}

// Scan copies the row's columns into dest and ends the span.
func (r *row) Scan(dest ...any) error {
	err := r.Row.Scan(dest...)
	r.end(err)

	return err
}

// ScanStruct copies the row's columns into the `ch`-tagged fields of dest and
// ends the span.
func (r *row) ScanStruct(dest any) error {
	err := r.Row.ScanStruct(dest)
	r.end(err)

	return err
}

// end records the read's outcome and counters, and ends the span. Only the first
// call does anything. sql.ErrNoRows is not recorded as a failure: the read
// succeeded, and its answer is that there is no such row.
func (r *row) end(err error) {
	if r.ended {
		return
	}

	r.ended = true
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}

	r.profile.stamp(r.op.Span)
	r.op.RecordError("failed to read row", err)
	r.op.End(r.ctx)
}
