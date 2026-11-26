package clickhouse

import (
	"reflect"
	"strings"

	"github.com/mountaya/helix.go/errorstack"
)

// BatchOption configures a batch insert begun by NewBatchInsert.
type BatchOption func(*batchOptions)

// batchOptions is what the BatchOptions given to NewBatchInsert set.
type batchOptions struct {

	// columns are the columns the INSERT names, none naming every column of the
	// table in its order.
	columns []string

	// err is why an option could not be applied, reported by NewBatchInsert
	// before anything is sent.
	err error
}

// WithColumns names the columns the batch inserts, so the statement is
// INSERT INTO table (column, …). AppendStruct then fills those columns from the
// fields whose `ch` tags name them, a field naming another column is ignored,
// and every column of the table left unnamed takes its DEFAULT.
//
// Naming the columns keeps a batch valid while the table changes under a
// running release: a column added with a DEFAULT is filled by the server rather
// than missing from every row the release appends. A column named twice, empty,
// not valid UTF-8 or holding a control character is a validation error.
func WithColumns(columns ...string) BatchOption {
	return func(opts *batchOptions) {
		seen := make(map[string]bool, len(columns))

		for _, column := range columns {
			if !validColumn(column) || seen[column] {
				opts.err = errorstack.NewValidation(errorstack.Entry{
					Message: "Must name distinct columns, each valid UTF-8 without control characters",
					Path:    []any{"columns"},
				})

				return
			}

			seen[column] = true
		}

		opts.columns = columns
	}
}

// WithColumnsOf names the columns the batch inserts as the `ch` tags of value's
// fields name them, in their order, as WithColumns does. value is a struct, or a
// pointer to one, held to the same rules as a row given to AsyncInsertStruct:
// every exported field tagged, `ch:"-"` to skip one, no embedded struct.
func WithColumnsOf(value any) BatchOption {
	return func(opts *batchOptions) {
		t := reflect.TypeOf(value)
		if t != nil && t.Kind() == reflect.Pointer {
			t = t.Elem()
		}

		if t == nil || t.Kind() != reflect.Struct {
			opts.err = errorstack.NewValidation(errorstack.Entry{
				Message: "Must be a struct, or a pointer to one",
				Path:    []any{"value"},
			})

			return
		}

		info, err := reflectStructInfo(t)
		if err != nil {
			opts.err = err
			return
		}

		opts.columns = info.columns
	}
}

// BatchColumns returns the columns opts name, as NewBatchInsert reads them, and
// none when they name none, which inserts every column of the table. It is for a
// test double of ClickHouse, which can then begin the same INSERT, or record what
// a caller asked for.
func BatchColumns(opts ...BatchOption) ([]string, error) {
	options := &batchOptions{}
	for _, opt := range opts {
		opt(options)
	}

	return options.columns, options.err
}

// insertStatement returns the INSERT a batch into quoted, a quoted table name,
// begins with: naming the columns opts names, every column of the table when it
// names none.
func (opts *batchOptions) insertStatement(quoted string) string {
	if len(opts.columns) == 0 {
		return "INSERT INTO " + quoted
	}

	columns := make([]string, len(opts.columns))
	for i, column := range opts.columns {
		columns[i] = quoteColumn(column)
	}

	return "INSERT INTO " + quoted + " (" + strings.Join(columns, ", ") + ")"
}
