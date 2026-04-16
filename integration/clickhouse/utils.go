package clickhouse

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/mountayaapp/helix.go/telemetry/trace"

	"go.opentelemetry.io/otel/attribute"
)

/*
Pre-computed span names to avoid allocations on every call.
*/
var (
	attrKeyDatabase = attribute.Key(identifier + ".database")
	attrKeyTable    = attribute.Key(identifier + ".table")
)

/*
setDefaultAttributes sets integration attributes to a trace span.
*/
func setDefaultAttributes(span *trace.Span, cfg *Config) {
	if cfg != nil {
		span.SetAttributes(attrKeyDatabase.String(cfg.Database))
	}
}

/*
structInfo caches the reflected column names and field indexes for a struct
type, so hot-path AsyncInsertStruct calls avoid repeated reflection.
*/
type structInfo struct {
	columns []string
	indexes [][]int
}

/*
structCache memoizes structInfo values by reflect.Type.
*/
var structCache sync.Map

/*
reflectStructInfo extracts column names from `ch` struct tags and field indexes
for a struct type. Results are cached per type.

Every exported field must carry a `ch` tag; a tag of "-" explicitly opts the
field out. Untagged exported fields would otherwise leak as literal column
names (e.g. "Untagged") and only fail at insert time with a cryptic server
error, so we reject them at registration time.

Embedded (anonymous) struct fields are also rejected: unlike the batch API's
AppendStruct, AsyncInsertStruct does not recurse into child fields. Returning
an error at registration time is safer than silently missing columns.
*/
func reflectStructInfo(t reflect.Type) (*structInfo, error) {
	if cached, ok := structCache.Load(t); ok {
		return cached.(*structInfo), nil
	}

	info := &structInfo{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			return nil, fmt.Errorf("AsyncInsertStruct: embedded field %q in %s is not supported", f.Name, t.String())
		}

		if len(f.PkgPath) != 0 {
			continue
		}

		tag := f.Tag.Get("ch")
		if len(tag) == 0 {
			return nil, fmt.Errorf("AsyncInsertStruct: exported field %q in %s is missing a `ch` tag (use `ch:\"-\"` to skip)", f.Name, t.String())
		}

		if tag == "-" {
			continue
		}

		info.columns = append(info.columns, tag)
		info.indexes = append(info.indexes, f.Index)
	}

	actual, _ := structCache.LoadOrStore(t, info)
	return actual.(*structInfo), nil
}

/*
buildInsertQuery returns an `INSERT INTO <table> (`col1`, `col2`) VALUES (?, ?)`
SQL string for the given table and columns. Column names are backtick-quoted so
that tag values containing reserved words or unusual characters still produce
valid SQL.
*/
func buildInsertQuery(table string, columns []string) string {
	var b strings.Builder
	b.Grow(len(table) + 32 + len(columns)*18)
	b.WriteString("INSERT INTO ")
	b.WriteString(table)
	b.WriteString(" (")

	for i, c := range columns {
		if i > 0 {
			b.WriteString(", ")
		}

		b.WriteByte('`')
		b.WriteString(c)
		b.WriteByte('`')
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

/*
prepareAsyncInsert validates the input value, reflects its columns and field
values, and builds the INSERT query. It contains all the logic of
AsyncInsertStruct that does not require a live ClickHouse connection, so it
can be unit-tested in isolation.
*/
func prepareAsyncInsert(table string, value any) (string, []any, error) {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "", nil, fmt.Errorf("AsyncInsertStruct: nil pointer passed as value")
		}

		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return "", nil, fmt.Errorf("AsyncInsertStruct: expected a struct, got %s", v.Kind())
	}

	info, err := reflectStructInfo(v.Type())
	if err != nil {
		return "", nil, err
	}

	if len(info.columns) == 0 {
		return "", nil, fmt.Errorf("AsyncInsertStruct: %s has no exportable columns", v.Type().String())
	}

	args := make([]any, len(info.indexes))
	for i, idx := range info.indexes {
		args[i] = v.FieldByIndex(idx).Interface()
	}

	return buildInsertQuery(table, info.columns), args, nil
}
