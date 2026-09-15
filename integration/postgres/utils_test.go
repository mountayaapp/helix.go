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

func TestBuildAddress(t *testing.T) {
	testcases := []struct {
		name     string
		config   Config
		expected string
	}{
		{
			name: "plain credentials",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "app",
				User:     "helix",
				Password: "s3cret",
			},
			expected: "postgres://helix:s3cret@127.0.0.1:5432/app",
		},
		{
			name: "at sign in password is escaped",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "app",
				User:     "helix",
				Password: "p@ssword",
			},
			expected: "postgres://helix:p%40ssword@127.0.0.1:5432/app",
		},
		{
			name: "space in password is escaped",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "app",
				User:     "helix",
				Password: "pa ss",
			},
			expected: "postgres://helix:pa%20ss@127.0.0.1:5432/app",
		},
		{
			name: "number sign in password is escaped",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "app",
				User:     "helix",
				Password: "pa#ss",
			},
			expected: "postgres://helix:pa%23ss@127.0.0.1:5432/app",
		},
		{
			name: "slash in password is escaped",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "app",
				User:     "helix",
				Password: "pa/ss",
			},
			expected: "postgres://helix:pa%2Fss@127.0.0.1:5432/app",
		},
		{
			name: "percent sign in password is escaped",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "app",
				User:     "helix",
				Password: "pa%ss",
			},
			expected: "postgres://helix:pa%25ss@127.0.0.1:5432/app",
		},
		{
			name: "at sign in user is escaped",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "app",
				User:     "he@lix",
				Password: "s3cret",
			},
			expected: "postgres://he%40lix:s3cret@127.0.0.1:5432/app",
		},
		{
			name: "space in database is escaped",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "my app",
				User:     "helix",
				Password: "s3cret",
			},
			expected: "postgres://helix:s3cret@127.0.0.1:5432/my%20app",
		},
		{
			name: "bracketed IPv6 address is preserved",
			config: Config{
				Address:  "[::1]:5432",
				Database: "app",
				User:     "helix",
				Password: "s3cret",
			},
			expected: "postgres://helix:s3cret@[::1]:5432/app",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, buildAddress(&tc.config))
		})
	}
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
