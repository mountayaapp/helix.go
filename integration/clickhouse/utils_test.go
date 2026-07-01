package clickhouse

import (
	"reflect"
	"testing"

	"github.com/mountayaapp/helix.go/telemetry/trace"

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

func TestSetDefaultAttributes_WithConfig(t *testing.T) {
	span := trace.NewSpan(nil)
	cfg := &Config{
		Database: "analytics",
	}

	// Should not panic with nil span internals.
	setDefaultAttributes(span, cfg)
}

func TestSetDefaultAttributes_WithNilConfig(t *testing.T) {
	span := trace.NewSpan(nil)

	// Should not panic with nil config.
	setDefaultAttributes(span, nil)
}

func TestPreComputedAttributeKeys(t *testing.T) {
	assert.Equal(t, attribute.Key("clickhouse.database"), attrKeyDatabase)
}

func TestPreComputedSpanNames(t *testing.T) {
	assert.Equal(t, "ClickHouse: Batch / Begin", spanBatchBegin)
	assert.Equal(t, "ClickHouse: Batch / Send", spanBatchSend)
	assert.Equal(t, "ClickHouse: Async Insert", spanAsyncInsert)
	assert.Equal(t, "ClickHouse: Select", spanSelect)
}

func TestReflectStructInfo_ColumnsAndIndexes(t *testing.T) {
	info, err := reflectStructInfo(reflect.TypeOf(testRow{}))
	require.NoError(t, err)

	assert.Equal(t, []string{"date", "path", "count"}, info.columns)
	assert.Len(t, info.indexes, 3)
}

func TestReflectStructInfo_CachedAcrossCalls(t *testing.T) {
	first, err := reflectStructInfo(reflect.TypeOf(testRow{}))
	require.NoError(t, err)

	second, err := reflectStructInfo(reflect.TypeOf(testRow{}))
	require.NoError(t, err)

	assert.Same(t, first, second)
}

func TestReflectStructInfo_RejectsEmbeddedField(t *testing.T) {
	_, err := reflectStructInfo(reflect.TypeOf(testRowWithEmbedding{}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedded field")
}

func TestReflectStructInfo_RejectsUntaggedExportedField(t *testing.T) {
	_, err := reflectStructInfo(reflect.TypeOf(testRowUntaggedField{}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing a `ch` tag")
	assert.Contains(t, err.Error(), "Orphan")
}

func TestReflectStructInfo_AllFieldsSkipped(t *testing.T) {
	info, err := reflectStructInfo(reflect.TypeOf(testRowAllSkipped{}))
	require.NoError(t, err)
	assert.Empty(t, info.columns)
	assert.Empty(t, info.indexes)
}

func TestBuildInsertQuery(t *testing.T) {
	query := buildInsertQuery("events", []string{"date", "path", "count"})
	assert.Equal(t, "INSERT INTO events (`date`, `path`, `count`) VALUES (?, ?, ?)", query)
}

func TestBuildInsertQuery_SingleColumn(t *testing.T) {
	query := buildInsertQuery("foo", []string{"id"})
	assert.Equal(t, "INSERT INTO foo (`id`) VALUES (?)", query)
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
	assert.Equal(t, "INSERT INTO events (`date`, `path`, `count`) VALUES (?, ?, ?)", query)
	assert.Equal(t, []any{"2026-04-15", "/pricing", uint64(42)}, args)
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
	assert.Contains(t, err.Error(), "nil pointer")
}

func TestPrepareAsyncInsert_NotAStruct(t *testing.T) {
	_, _, err := prepareAsyncInsert("events", "not-a-struct")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected a struct")
}

func TestPrepareAsyncInsert_EmptyColumns(t *testing.T) {
	_, _, err := prepareAsyncInsert("events", testRowAllSkipped{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no exportable columns")
}

func TestPrepareAsyncInsert_OnlyUnexportedFieldsYieldEmptyColumns(t *testing.T) {
	_, _, err := prepareAsyncInsert("events", testRowOnlyUnexported{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no exportable columns")
}

func TestPrepareAsyncInsert_InvalidStructLayoutPropagates(t *testing.T) {
	_, _, err := prepareAsyncInsert("events", testRowUntaggedField{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing a `ch` tag")
}

func BenchmarkSetDefaultAttributes(b *testing.B) {
	span := trace.NewSpan(nil)
	cfg := &Config{
		Database: "analytics",
	}

	for b.Loop() {
		setDefaultAttributes(span, cfg)
	}
}
