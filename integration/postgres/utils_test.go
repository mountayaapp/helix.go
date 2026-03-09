package postgres

import (
	"testing"

	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestSetDefaultAttributes_WithConfig(t *testing.T) {
	span := trace.NewSpan(nil)
	cfg := &Config{
		Database: "mydb",
	}

	// Should not panic with nil span internals.
	setDefaultAttributes(span, cfg)
}

func TestSetDefaultAttributes_WithNilConfig(t *testing.T) {
	span := trace.NewSpan(nil)

	// Should not panic with nil config.
	setDefaultAttributes(span, nil)
}

func TestSetQueryAttributes(t *testing.T) {
	span := trace.NewSpan(nil)

	// Should not panic with nil span internals.
	setQueryAttributes(span, "SELECT 1")
}

func TestSetTransactionQueryAttributes(t *testing.T) {
	span := trace.NewSpan(nil)

	// Should not panic with nil span internals.
	setTransactionQueryAttributes(span, "INSERT INTO users VALUES ($1)")
}

func TestPreComputedAttributeKeys(t *testing.T) {
	assert.Equal(t, attribute.Key("postgres.database"), attrKeyDatabase)
	assert.Equal(t, attribute.Key("postgres.query"), attrKeyQuery)
	assert.Equal(t, attribute.Key("postgres.transaction.query"), attrKeyTxQuery)
}

func TestPreComputedSpanNames(t *testing.T) {
	assert.Equal(t, "PostgreSQL: Exec", spanExec)
	assert.Equal(t, "PostgreSQL: QueryRows", spanQuery)
	assert.Equal(t, "PostgreSQL: QueryRow", spanQueryRow)
	assert.Equal(t, "PostgreSQL: Transaction / Begin", spanTxBegin)
	assert.Equal(t, "PostgreSQL: Transaction / Commit", spanTxCommit)
	assert.Equal(t, "PostgreSQL: Transaction / Rollback", spanTxRollback)
	assert.Equal(t, "PostgreSQL: Transaction / Exec", spanTxExec)
	assert.Equal(t, "PostgreSQL: Transaction / Prepare", spanTxPrepare)
	assert.Equal(t, "PostgreSQL: Transaction / QueryRows", spanTxQuery)
	assert.Equal(t, "PostgreSQL: Transaction / QueryRow", spanTxQueryRow)
}

func BenchmarkSetDefaultAttributes(b *testing.B) {
	span := trace.NewSpan(nil)
	cfg := &Config{
		Database: "mydb",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		setDefaultAttributes(span, cfg)
	}
}

func BenchmarkSetQueryAttributes(b *testing.B) {
	span := trace.NewSpan(nil)
	query := "SELECT id, name, email FROM users WHERE id = $1 AND active = true"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		setQueryAttributes(span, query)
	}
}

func BenchmarkSetTransactionQueryAttributes(b *testing.B) {
	span := trace.NewSpan(nil)
	query := "INSERT INTO users (name, email) VALUES ($1, $2)"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		setTransactionQueryAttributes(span, query)
	}
}
