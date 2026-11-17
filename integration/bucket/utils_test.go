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
				Driver: DriverLocal,
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
				Driver:    DriverLocal,
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
			name: "a region and an endpoint add one attribute each, the key pair none",
			cfg: Config{
				Driver:          DriverAWS,
				Bucket:          "my-bucket",
				Region:          "us-east-1",
				Endpoint:        "http://localhost:9000",
				AccessKeyID:     "access-key",
				SecretAccessKey: "secret-key",
			},
			attrs: []attribute.KeyValue{
				attribute.String("bucket.driver", "aws"),
				attribute.String("bucket.bucket", "my-bucket"),
				attribute.String("bucket.region", "us-east-1"),
				attribute.String("bucket.endpoint", "http://localhost:9000"),
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.attrs, instrumentAttributes(&tc.cfg))
		})
	}
}
