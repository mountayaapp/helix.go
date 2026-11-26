package temporal

import (
	"testing"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"

	"github.com/stretchr/testify/assert"
	"go.temporal.io/sdk/converter"
)

func TestConfigClient_Sanitize(t *testing.T) {
	testcases := []struct {
		name   string
		before ConfigClient
		after  ConfigClient
		err    error
	}{
		{
			name:   "empty config applies default address and namespace",
			before: ConfigClient{},
			after: ConfigClient{
				Address:   "127.0.0.1:7233",
				Namespace: "default",
			},
			err: nil,
		},
		{
			name: "custom namespace preserves namespace and applies default address",
			before: ConfigClient{
				Namespace: "fake",
			},
			after: ConfigClient{
				Address:   "127.0.0.1:7233",
				Namespace: "fake",
			},
			err: nil,
		},
		{
			name: "custom address and namespace are preserved",
			before: ConfigClient{
				Address:   "temporal.example.com:7233",
				Namespace: "production",
			},
			after: ConfigClient{
				Address:   "temporal.example.com:7233",
				Namespace: "production",
			},
			err: nil,
		},
		{
			name: "a gRPC target is a valid address",
			before: ConfigClient{
				Address: "dns:///temporal.example.com:7233",
			},
			after: ConfigClient{
				Address:   "dns:///temporal.example.com:7233",
				Namespace: "default",
			},
			err: nil,
		},
		{
			name: "an address without a port returns error",
			before: ConfigClient{
				Address: "temporal.example.com",
			},
			after: ConfigClient{
				Address:   "temporal.example.com",
				Namespace: "default",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{
					Message: "Must be a host:port address or a gRPC target",
					Path:    []any{"config", "address"},
				},
			),
		},
		{
			name: "whitespace in the address and namespace returns errors",
			before: ConfigClient{
				Address:   "temporal.example.com:7233\n",
				Namespace: "prod uction",
			},
			after: ConfigClient{
				Address:   "temporal.example.com:7233\n",
				Namespace: "prod uction",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{
					Message: "Must be a host:port address or a gRPC target",
					Path:    []any{"config", "address"},
				},
				errorstack.Entry{
					Message: "Must not contain whitespace",
					Path:    []any{"config", "namespace"},
				},
			),
		},
		{
			name: "TLS with only CertPEM returns error",
			before: ConfigClient{
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			after: ConfigClient{
				Address:   "127.0.0.1:7233",
				Namespace: "default",
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			err: errorstack.NewValidation(
				errorstack.Entry{
					Message: "Must be set together; cert_pem and key_pem are required as a pair",
					Path:    []any{"config", "tls"},
				},
			),
		},
		{
			name: "TLS with both CertPEM and KeyPEM is valid",
			before: ConfigClient{
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
				},
			},
			after: ConfigClient{
				Address:   "127.0.0.1:7233",
				Namespace: "default",
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
				},
			},
			err: nil,
		},
		{
			name: "disabled TLS ignores invalid certs",
			before: ConfigClient{
				TLS: integration.ConfigTLS{
					Enabled: false,
					CertPEM: []byte("cert"),
				},
			},
			after: ConfigClient{
				Address:   "127.0.0.1:7233",
				Namespace: "default",
				TLS: integration.ConfigTLS{
					Enabled: false,
					CertPEM: []byte("cert"),
				},
			},
			err: nil,
		},
		{
			name: "API key and full Event propagation are preserved",
			before: ConfigClient{
				APIKey:             "key",
				PropagateFullEvent: true,
			},
			after: ConfigClient{
				Address:            "127.0.0.1:7233",
				Namespace:          "default",
				APIKey:             "key",
				PropagateFullEvent: true,
			},
			err: nil,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.before.sanitize()

			assert.Equal(t, tc.after, tc.before)
			assert.Equal(t, tc.err, err)
		})
	}
}

func TestConfigClient_Name(t *testing.T) {
	assert.Equal(t, "temporal", (&ConfigClient{}).name())
	assert.Equal(t, "orders", (&ConfigClient{Name: "orders"}).name())
}

func TestConfigClient_DataConverter(t *testing.T) {
	assert.Same(t, converter.GetDefaultDataConverter(), (&ConfigClient{}).dataConverter())

	custom := converter.NewCompositeDataConverter(converter.NewJSONPayloadConverter())
	assert.Same(t, custom, (&ConfigClient{DataConverter: custom}).dataConverter())
}
