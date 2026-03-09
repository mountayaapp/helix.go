package bucket

import (
	"testing"

	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestSetAttributes_WithConfig(t *testing.T) {
	span := trace.NewSpan(nil)
	cfg := &Config{
		Driver: DriverLocal,
		Bucket: "my-bucket",
	}

	// Should not panic with nil span internals.
	setAttributes(span, cfg, "some/key")
}

func TestSetAttributes_WithConfigAndSubfolder(t *testing.T) {
	span := trace.NewSpan(nil)
	cfg := &Config{
		Driver:    DriverLocal,
		Bucket:    "my-bucket",
		Subfolder: "prefix/",
	}

	// Should not panic with nil span internals.
	setAttributes(span, cfg, "some/key")
}

func TestSetAttributes_WithNilConfig(t *testing.T) {
	span := trace.NewSpan(nil)

	// Should not panic with nil config.
	setAttributes(span, nil, "some/key")
}

func TestPreComputedAttributeKeys(t *testing.T) {
	assert.Equal(t, attribute.Key("bucket.driver"), attrKeyDriver)
	assert.Equal(t, attribute.Key("bucket.bucket"), attrKeyBucket)
	assert.Equal(t, attribute.Key("bucket.subfolder"), attrKeySubfolder)
	assert.Equal(t, attribute.Key("bucket.key"), attrKeyKey)
}

func TestPreComputedSpanNames(t *testing.T) {
	assert.Equal(t, "Bucket: Exists", spanBucketExists)
	assert.Equal(t, "Bucket: Read", spanBucketRead)
	assert.Equal(t, "Bucket: Write", spanBucketWrite)
	assert.Equal(t, "Bucket: Delete", spanBucketDelete)
}

func BenchmarkSetAttributes_WithConfig(b *testing.B) {
	span := trace.NewSpan(nil)
	cfg := &Config{
		Driver: DriverLocal,
		Bucket: "my-bucket",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		setAttributes(span, cfg, "some/key")
	}
}

func BenchmarkSetAttributes_WithSubfolder(b *testing.B) {
	span := trace.NewSpan(nil)
	cfg := &Config{
		Driver:    DriverLocal,
		Bucket:    "my-bucket",
		Subfolder: "prefix/",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		setAttributes(span, cfg, "some/key")
	}
}
