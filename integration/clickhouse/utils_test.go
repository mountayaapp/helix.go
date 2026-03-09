package clickhouse

import (
	"testing"

	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

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
}

func BenchmarkSetDefaultAttributes(b *testing.B) {
	span := trace.NewSpan(nil)
	cfg := &Config{
		Database: "analytics",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		setDefaultAttributes(span, cfg)
	}
}
