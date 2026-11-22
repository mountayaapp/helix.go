package bucket

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestPreComputedAttributeKeys(t *testing.T) {
	assert.Equal(t, attribute.Key("bucket.key"), attrKeyKey)
}

func TestInstrumentAttributes(t *testing.T) {
	testcases := []struct {
		name  string
		cfg   Config
		attrs []attribute.KeyValue
	}{
		{
			name: "driver and bucket are always stamped",
			cfg: Config{
				Driver: DriverLocal{},
				Bucket: "my-bucket",
			},
			attrs: []attribute.KeyValue{
				attribute.String("bucket.driver", "local"),
				attribute.String("bucket.bucket", "my-bucket"),
			},
		},
		{
			name: "a subfolder adds one attribute",
			cfg: Config{
				Driver:    DriverLocal{},
				Bucket:    "my-bucket",
				Subfolder: "prefix/",
			},
			attrs: []attribute.KeyValue{
				attribute.String("bucket.driver", "local"),
				attribute.String("bucket.bucket", "my-bucket"),
				attribute.String("bucket.subfolder", "prefix/"),
			},
		},
		{
			name: "s3 adds its region and endpoint, never its key pair",
			cfg: Config{
				Driver: s3Driver("http://localhost:9000"),
				Bucket: "my-bucket",
			},
			attrs: []attribute.KeyValue{
				attribute.String("bucket.driver", "s3"),
				attribute.String("bucket.bucket", "my-bucket"),
				attribute.String("bucket.region", "eu-west-3"),
				attribute.String("bucket.endpoint", "http://localhost:9000"),
			},
		},
		{
			name: "azure adds its endpoint, never its account or key",
			cfg: Config{
				Driver: azureDriver("http://127.0.0.1:10000/devstoreaccount1"),
				Bucket: "my-container",
			},
			attrs: []attribute.KeyValue{
				attribute.String("bucket.driver", "azure"),
				attribute.String("bucket.bucket", "my-container"),
				attribute.String("bucket.endpoint", "http://127.0.0.1:10000/devstoreaccount1"),
			},
		},
		{
			name: "google adds its endpoint, never its key",
			cfg: Config{
				Driver: googleDriver(t, "http://localhost:4443"),
				Bucket: "my-bucket",
			},
			attrs: []attribute.KeyValue{
				attribute.String("bucket.driver", "google"),
				attribute.String("bucket.bucket", "my-bucket"),
				attribute.String("bucket.endpoint", "http://localhost:4443"),
			},
		},
		{
			name: "google without endpoint adds nothing",
			cfg: Config{
				Driver: googleDriver(t, ""),
				Bucket: "my-bucket",
			},
			attrs: []attribute.KeyValue{
				attribute.String("bucket.driver", "google"),
				attribute.String("bucket.bucket", "my-bucket"),
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.attrs, instrumentAttributes(&tc.cfg))
		})
	}
}
