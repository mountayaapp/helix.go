package clickhouse

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
)

// Pre-computed attribute keys to avoid allocations on every call.
//
// The database is not among them: it is fixed for the life of the connection, so
// it is handed to the Instrument once and stamped by it rather than re-set on
// every span.
var (
	attrKeyQuery     = integration.Key(identifier, "query")
	attrKeyTable     = integration.Key(identifier, "table")
	attrKeyPoolState = integration.Key(identifier, "pool.state")
)

// tableIdentifier is what a table name passed to NewBatchInsert or
// AsyncInsertStruct must look like: a plain identifier, optionally qualified by
// its database.
//
// It is deliberately narrower than what ClickHouse accepts between backticks. The
// table is the one part of an INSERT this package builds from a runtime string,
// and that string is routinely derived from input — a tenant, a period — so it is
// held to a shape that cannot carry anything but a name.
var tableIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)

// quoteTable validates table and returns it backtick-quoted, each part on its own
// so that "db.events" names the table events in the database db rather than a
// table called "db.events".
func quoteTable(table string) (string, error) {
	if !tableIdentifier.MatchString(table) {
		return "", errorstack.NewValidation(errorstack.Entry{
			Message: "Must be a table name, optionally qualified by its database, made of letters, digits and underscores",
			Path:    []any{"table"},
		})
	}

	database, name, qualified := strings.Cut(table, ".")
	if !qualified {
		return "`" + table + "`", nil
	}

	return "`" + database + "`.`" + name + "`", nil
}

// quoteColumn returns column as a backtick-quoted identifier.
//
// ClickHouse reads a backslash inside a quoted identifier as an escape, and a
// doubled backtick as a literal one, so both are escaped: quoting alone would let
// a name ending in a backslash, or holding a backtick, close the identifier early
// and carry the rest of the name into the statement as SQL.
func quoteColumn(column string) string {
	column = strings.ReplaceAll(column, `\`, `\\`)
	column = strings.ReplaceAll(column, "`", "``")

	return "`" + column + "`"
}

// validColumn reports whether column can name a column: it is not empty, is valid
// UTF-8 and holds no control character. Anything else ClickHouse accepts between
// backticks is accepted here too — including the dot of a Nested sub-column — so
// that a row type valid for a batch insert, which takes its columns from the
// server, is valid for an async insert as well.
func validColumn(column string) bool {
	if column == "" || !utf8.ValidString(column) {
		return false
	}

	return strings.IndexFunc(column, unicode.IsControl) < 0
}

// structInfo caches what an async insert of a struct type needs, so hot-path
// AsyncInsertStruct calls pay for reflection and query building once per type.
type structInfo struct {
	columns []string
	indexes [][]int

	// suffix is everything of the INSERT statement after the table:
	// " (`a`, `b`) VALUES (?, ?)". It is cached here rather than the whole
	// statement per type and table, because the table is a runtime string and a
	// cache keyed on it grows with every distinct table a caller ever names.
	suffix string
}

// structCache memoizes structInfo values by reflect.Type.
var structCache sync.Map

// reflectStructInfo extracts column names from `ch` struct tags and field indexes
// for a struct type. Results are cached per type.
//
// Every exported field must carry a `ch` tag; a tag of "-" explicitly opts the
// field out. Untagged exported fields would otherwise leak as literal column
// names (e.g. "Untagged") and only fail at insert time with a cryptic server
// error, so we reject them at registration time.
//
// Embedded (anonymous) fields are also rejected: unlike the batch API's
// AppendStruct, AsyncInsertStruct does not recurse into child fields. Returning
// an error at registration time is safer than silently missing columns. That
// includes an unexported embedded struct, whose exported fields are promoted and
// which the batch API does recurse into; an unexported embedded field that
// promotes nothing is skipped like any other unexported field.
func reflectStructInfo(t reflect.Type) (*structInfo, error) {
	if cached, ok := structCache.Load(t); ok {
		return cached.(*structInfo), nil
	}

	info := &structInfo{}
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous && (f.IsExported() || promotesExported(f.Type)) {
			return nil, errorstack.NewValidation(errorstack.Entry{
				Message: fmt.Sprintf("Must not be an embedded struct; %s does not recurse into %q", identifier, f.Name),
				Path:    []any{"value", t.String(), f.Name},
			})
		}

		if !f.IsExported() {
			continue
		}

		tag := f.Tag.Get("ch")
		if len(tag) == 0 {
			return nil, errorstack.NewValidation(errorstack.Entry{
				Message: `Must carry a "ch" struct tag; use ` + "`ch:\"-\"`" + ` to skip the field`,
				Path:    []any{"value", t.String(), f.Name},
			})
		}

		if tag == "-" {
			continue
		}

		if !validColumn(tag) {
			return nil, errorstack.NewValidation(errorstack.Entry{
				Message: `Must carry a "ch" struct tag naming a column; it must be valid UTF-8 without control characters`,
				Path:    []any{"value", t.String(), f.Name},
			})
		}

		info.columns = append(info.columns, tag)
		info.indexes = append(info.indexes, f.Index)
	}

	info.suffix = buildInsertSuffix(info.columns)

	actual, _ := structCache.LoadOrStore(t, info)
	return actual.(*structInfo), nil
}

// promotesExported reports whether embedding a field of type t promotes an
// exported field into the struct that embeds it.
func promotesExported(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return false
	}

	for _, f := range reflect.VisibleFields(t) {
		if f.IsExported() {
			return true
		}
	}

	return false
}

// buildInsertSuffix returns the part of an INSERT statement that follows the
// table: " (`col1`, `col2`) VALUES (?, ?)". Column names are quoted with
// quoteColumn, so tag values holding reserved words or unusual characters still
// produce valid SQL, and no tag value can produce anything but a column name.
func buildInsertSuffix(columns []string) string {
	var b strings.Builder
	b.Grow(16 + len(columns)*8)
	b.WriteString(" (")

	for i, c := range columns {
		if i > 0 {
			b.WriteString(", ")
		}

		b.WriteString(quoteColumn(c))
	}

	b.WriteString(") VALUES (")

	for i := range columns {
		if i > 0 {
			b.WriteString(", ")
		}

		b.WriteString("?")
	}

	b.WriteString(")")

	return b.String()
}

// prepareAsyncInsert validates the table and the input value, reflects its
// columns and field values, and builds the INSERT query. It contains all the logic
// of AsyncInsertStruct that does not require a live ClickHouse connection, so it
// can be unit-tested in isolation.
func prepareAsyncInsert(table string, value any) (string, []any, error) {
	quoted, err := quoteTable(table)
	if err != nil {
		return "", nil, err
	}

	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "", nil, errorstack.NewValidation(errorstack.Entry{
				Message: "Must not be a nil pointer",
				Path:    []any{"value"},
			})
		}

		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return "", nil, errorstack.NewValidation(errorstack.Entry{
			Message: fmt.Sprintf("Must be a struct, got %s", v.Kind()),
			Path:    []any{"value"},
		})
	}

	info, err := reflectStructInfo(v.Type())
	if err != nil {
		return "", nil, err
	}

	if len(info.columns) == 0 {
		return "", nil, errorstack.NewValidation(errorstack.Entry{
			Message: fmt.Sprintf("Must declare at least one column; %s has none", v.Type().String()),
			Path:    []any{"value"},
		})
	}

	args := make([]any, len(info.indexes))
	for i, idx := range info.indexes {
		args[i] = v.FieldByIndex(idx).Interface()
	}

	return "INSERT INTO " + quoted + info.suffix, args, nil
}
