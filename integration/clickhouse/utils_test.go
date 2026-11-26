package clickhouse

import (
	"reflect"
	"testing"

	"github.com/mountaya/helix.go/errorstack"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

type testRow struct {
	Date    string `ch:"date"`
	Path    string `ch:"path"`
	Ignored string `ch:"-"`
	Count   uint64 `ch:"count"`
}

type testRowWithEmbedding struct {
	testRow
	Extra string `ch:"extra"`
}

type testRowAllSkipped struct {
	Skipped string `ch:"-"`
}

type testRowUntaggedField struct {
	Tagged string `ch:"tagged"`
	Orphan int
}

type testRowOnlyUnexported struct {
	hidden int //nolint:unused
}

// testUnexportedEmbed promotes nothing: its only field is unexported.
type testUnexportedEmbed struct { //nolint:unused
	hidden int //nolint:unused
}

type testRowWithUnexportedEmbed struct {
	testUnexportedEmbed        //nolint:unused
	ID                  uint32 `ch:"id"`
}

type testRowUnusualColumns struct {
	Nested   string `ch:"n.a"`
	Backtick string "ch:\"a`b\""
	Escape   string `ch:"a\\"`
}

type testRowControlColumn struct {
	Bad string `ch:"a\nb"`
}

func TestPreComputedAttributeKeys(t *testing.T) {
	assert.Equal(t, attribute.Key("clickhouse.query"), attrKeyQuery)
	assert.Equal(t, attribute.Key("clickhouse.table"), attrKeyTable)
	assert.Equal(t, attribute.Key("clickhouse.pool.state"), attrKeyPoolState)
}

func TestQuoteTable(t *testing.T) {
	testcases := []struct {
		table string
		want  string
	}{
		{table: "events", want: "`events`"},
		{table: "_events_2026", want: "`_events_2026`"},
		{table: "analytics.events", want: "`analytics`.`events`"},
	}

	for _, tc := range testcases {
		t.Run(tc.table, func(t *testing.T) {
			got, err := quoteTable(tc.table)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestQuoteTable_RejectsInjection pins that a table name is spliced into an
// INSERT only when it can be nothing but a name. Each of these used to reach the
// server as SQL.
func TestQuoteTable_RejectsInjection(t *testing.T) {
	testcases := []string{
		"",
		"events; DROP TABLE users",
		"events (id) SELECT * FROM secrets --",
		"events`; DROP TABLE users; --",
		"`events`",
		"a.b.c",
		".events",
		"events.",
		"1events",
		"ev ents",
		"events\n",
		"évènements",
	}

	for _, table := range testcases {
		t.Run(table, func(t *testing.T) {
			quoted, err := quoteTable(table)
			require.Error(t, err)
			assert.Empty(t, quoted)

			var stack *errorstack.Error
			require.ErrorAs(t, err, &stack)
			assert.Equal(t, errorstack.CodeValidationFailed, stack.Entries[0].Extensions["code"])
			assert.Equal(t, []any{"table"}, stack.Entries[0].Path)
		})
	}
}

func TestQuoteColumn(t *testing.T) {
	testcases := []struct {
		column string
		want   string
	}{
		{column: "id", want: "`id`"},
		{column: "n.a", want: "`n.a`"},
		{column: "a`b", want: "`a``b`"},
		{column: "a`) VALUES (1); DROP TABLE users; --", want: "`a``) VALUES (1); DROP TABLE users; --`"},
		{column: `a\`, want: "`a\\\\`"},
		{column: "a\\`", want: "`a\\\\```"},
	}

	for _, tc := range testcases {
		t.Run(tc.column, func(t *testing.T) {
			assert.Equal(t, tc.want, quoteColumn(tc.column))
		})
	}
}

func TestReflectStructInfo_ColumnsAndIndexes(t *testing.T) {
	info, err := reflectStructInfo(reflect.TypeFor[testRow]())
	require.NoError(t, err)

	assert.Equal(t, []string{"date", "path", "count"}, info.columns)
	assert.Len(t, info.indexes, 3)
}

func TestReflectStructInfo_CachedAcrossCalls(t *testing.T) {
	first, err := reflectStructInfo(reflect.TypeFor[testRow]())
	require.NoError(t, err)

	second, err := reflectStructInfo(reflect.TypeFor[testRow]())
	require.NoError(t, err)

	assert.Same(t, first, second)
}

func TestReflectStructInfo_RejectsEmbeddedField(t *testing.T) {
	_, err := reflectStructInfo(reflect.TypeFor[testRowWithEmbedding]())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Must not be an embedded struct")
}

func TestReflectStructInfo_SkipsUnexportedEmbedPromotingNothing(t *testing.T) {
	info, err := reflectStructInfo(reflect.TypeFor[testRowWithUnexportedEmbed]())
	require.NoError(t, err)
	assert.Equal(t, []string{"id"}, info.columns)
}

func TestReflectStructInfo_QuotesUnusualColumns(t *testing.T) {
	info, err := reflectStructInfo(reflect.TypeFor[testRowUnusualColumns]())
	require.NoError(t, err)
	assert.Equal(t, " (`n.a`, `a``b`, `a\\\\`) VALUES (?, ?, ?)", info.suffix)
}

func TestReflectStructInfo_RejectsControlCharacterInColumn(t *testing.T) {
	_, err := reflectStructInfo(reflect.TypeFor[testRowControlColumn]())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "naming a column")
}

func TestReflectStructInfo_RejectsUntaggedExportedField(t *testing.T) {
	_, err := reflectStructInfo(reflect.TypeFor[testRowUntaggedField]())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Must carry a \"ch\" struct tag")
	assert.Contains(t, err.Error(), "Orphan")
}

func TestReflectStructInfo_AllFieldsSkipped(t *testing.T) {
	info, err := reflectStructInfo(reflect.TypeFor[testRowAllSkipped]())
	require.NoError(t, err)
	assert.Empty(t, info.columns)
	assert.Empty(t, info.indexes)
}

func TestBuildInsertSuffix(t *testing.T) {
	suffix := buildInsertSuffix([]string{"date", "path", "count"})
	assert.Equal(t, " (`date`, `path`, `count`) VALUES (?, ?, ?)", suffix)
}

func TestBuildInsertSuffix_SingleColumn(t *testing.T) {
	suffix := buildInsertSuffix([]string{"id"})
	assert.Equal(t, " (`id`) VALUES (?)", suffix)
}

func TestPrepareAsyncInsert_Struct(t *testing.T) {
	row := testRow{
		Date:    "2026-04-15",
		Path:    "/pricing",
		Ignored: "skip-me",
		Count:   42,
	}

	query, args, err := prepareAsyncInsert("events", row)
	require.NoError(t, err)
	assert.Equal(t, "INSERT INTO `events` (`date`, `path`, `count`) VALUES (?, ?, ?)", query)
	assert.Equal(t, []any{"2026-04-15", "/pricing", uint64(42)}, args)
}

func TestPrepareAsyncInsert_QualifiedTable(t *testing.T) {
	query, _, err := prepareAsyncInsert("analytics.events", testRow{})
	require.NoError(t, err)
	assert.Equal(t, "INSERT INTO `analytics`.`events` (`date`, `path`, `count`) VALUES (?, ?, ?)", query)
}

func TestPrepareAsyncInsert_RejectsInjectedTable(t *testing.T) {
	query, args, err := prepareAsyncInsert("events (date) SELECT secret FROM vault --", testRow{})
	require.Error(t, err)
	assert.Empty(t, query)
	assert.Nil(t, args)
	assert.Contains(t, err.Error(), "Must be a table name")
}

func TestPrepareAsyncInsert_Pointer(t *testing.T) {
	row := &testRow{Date: "2026-04-15", Path: "/pricing", Count: 1}

	_, args, err := prepareAsyncInsert("events", row)
	require.NoError(t, err)
	assert.Equal(t, []any{"2026-04-15", "/pricing", uint64(1)}, args)
}

func TestPrepareAsyncInsert_NilPointer(t *testing.T) {
	var row *testRow

	_, _, err := prepareAsyncInsert("events", row)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Must not be a nil pointer")
}

func TestPrepareAsyncInsert_NotAStruct(t *testing.T) {
	_, _, err := prepareAsyncInsert("events", "not-a-struct")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Must be a struct")
}

func TestPrepareAsyncInsert_EmptyColumns(t *testing.T) {
	_, _, err := prepareAsyncInsert("events", testRowAllSkipped{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Must declare at least one column")
}

func TestPrepareAsyncInsert_OnlyUnexportedFieldsYieldEmptyColumns(t *testing.T) {
	_, _, err := prepareAsyncInsert("events", testRowOnlyUnexported{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Must declare at least one column")
}

func TestPrepareAsyncInsert_InvalidStructLayoutPropagates(t *testing.T) {
	_, _, err := prepareAsyncInsert("events", testRowUntaggedField{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Must carry a \"ch\" struct tag")
}
