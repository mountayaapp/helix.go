package rest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// selfSignedCertificate returns a PEM-encoded certificate for localhost and its
// private key, good for an hour.
func selfSignedCertificate(t *testing.T) ([]byte, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM
}

func TestConfig_Sanitize(t *testing.T) {
	certPEM, keyPEM := selfSignedCertificate(t)

	testcases := []struct {
		name   string
		before Config
		after  Config
		err    error
	}{
		{
			name:   "empty config applies default address",
			before: Config{},
			after: Config{
				Address:             ":8080",
				IdleTimeout:         120 * time.Second,
				ReadHeaderTimeout:   10 * time.Second,
				MaxRequestBodyBytes: DefaultMaxRequestBodyBytes,
			},
			err: nil,
		},
		{
			name: "custom address is preserved",
			before: Config{
				Address: ":9090",
			},
			after: Config{
				Address:             ":9090",
				IdleTimeout:         120 * time.Second,
				ReadHeaderTimeout:   10 * time.Second,
				MaxRequestBodyBytes: DefaultMaxRequestBodyBytes,
			},
			err: nil,
		},
		{
			name: "OpenAPI enabled without description returns error",
			before: Config{
				OpenAPI: ConfigOpenAPI{
					Enabled: true,
				},
			},
			after: Config{
				Address:             ":8080",
				IdleTimeout:         120 * time.Second,
				ReadHeaderTimeout:   10 * time.Second,
				MaxRequestBodyBytes: DefaultMaxRequestBodyBytes,
				OpenAPI: ConfigOpenAPI{
					Enabled: true,
				},
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "openapi", "descriptions"}},
			),
		},
		{
			name: "OpenAPI enabled with one description is valid",
			before: Config{
				OpenAPI: ConfigOpenAPI{
					Enabled:      true,
					Descriptions: []string{"./openapi.yaml"},
				},
			},
			after: Config{
				Address:             ":8080",
				IdleTimeout:         120 * time.Second,
				ReadHeaderTimeout:   10 * time.Second,
				MaxRequestBodyBytes: DefaultMaxRequestBodyBytes,
				OpenAPI: ConfigOpenAPI{
					Enabled:      true,
					Descriptions: []string{"./openapi.yaml"},
				},
			},
			err: nil,
		},
		{
			name: "OpenAPI enabled with several descriptions is valid",
			before: Config{
				OpenAPI: ConfigOpenAPI{
					Enabled:      true,
					Descriptions: []string{"./openapi.yaml", "./openapi.internal.yaml"},
				},
			},
			after: Config{
				Address:             ":8080",
				IdleTimeout:         120 * time.Second,
				ReadHeaderTimeout:   10 * time.Second,
				MaxRequestBodyBytes: DefaultMaxRequestBodyBytes,
				OpenAPI: ConfigOpenAPI{
					Enabled:      true,
					Descriptions: []string{"./openapi.yaml", "./openapi.internal.yaml"},
				},
			},
			err: nil,
		},
		{
			name: "OpenAPI enabled with an empty description returns error",
			before: Config{
				OpenAPI: ConfigOpenAPI{
					Enabled:      true,
					Descriptions: []string{"./openapi.yaml", ""},
				},
			},
			after: Config{
				Address:             ":8080",
				IdleTimeout:         120 * time.Second,
				ReadHeaderTimeout:   10 * time.Second,
				MaxRequestBodyBytes: DefaultMaxRequestBodyBytes,
				OpenAPI: ConfigOpenAPI{
					Enabled:      true,
					Descriptions: []string{"./openapi.yaml", ""},
				},
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must not be empty", Path: []any{"config", "openapi", "descriptions", 1}},
			),
		},
		{
			name: "OpenAPI disabled is valid",
			before: Config{
				OpenAPI: ConfigOpenAPI{
					Enabled: false,
				},
			},
			after: Config{
				Address:             ":8080",
				IdleTimeout:         120 * time.Second,
				ReadHeaderTimeout:   10 * time.Second,
				MaxRequestBodyBytes: DefaultMaxRequestBodyBytes,
				OpenAPI: ConfigOpenAPI{
					Enabled: false,
				},
			},
			err: nil,
		},
		{
			name: "OpenAPI disabled ignores an absent description",
			before: Config{
				OpenAPI: ConfigOpenAPI{
					Enabled:      false,
					Descriptions: nil,
				},
			},
			after: Config{
				Address:             ":8080",
				IdleTimeout:         120 * time.Second,
				ReadHeaderTimeout:   10 * time.Second,
				MaxRequestBodyBytes: DefaultMaxRequestBodyBytes,
				OpenAPI: ConfigOpenAPI{
					Enabled:      false,
					Descriptions: nil,
				},
			},
			err: nil,
		},
		{
			name: "TLS with only CertPEM returns error",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			after: Config{
				Address:             ":8080",
				IdleTimeout:         120 * time.Second,
				ReadHeaderTimeout:   10 * time.Second,
				MaxRequestBodyBytes: DefaultMaxRequestBodyBytes,
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
			name: "TLS with only KeyPEM returns error",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled: true,
					KeyPEM:  []byte("key"),
				},
			},
			after: Config{
				Address:             ":8080",
				IdleTimeout:         120 * time.Second,
				ReadHeaderTimeout:   10 * time.Second,
				MaxRequestBodyBytes: DefaultMaxRequestBodyBytes,
				TLS: integration.ConfigTLS{
					Enabled: true,
					KeyPEM:  []byte("key"),
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
			name: "TLS with a valid key pair is valid",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: certPEM,
					KeyPEM:  keyPEM,
				},
			},
			after: Config{
				Address:             ":8080",
				IdleTimeout:         120 * time.Second,
				ReadHeaderTimeout:   10 * time.Second,
				MaxRequestBodyBytes: DefaultMaxRequestBodyBytes,
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: certPEM,
					KeyPEM:  keyPEM,
				},
			},
			err: nil,
		},
		{
			name: "TLS with a malformed key pair returns error",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
				},
			},
			after: Config{
				Address:             ":8080",
				IdleTimeout:         120 * time.Second,
				ReadHeaderTimeout:   10 * time.Second,
				MaxRequestBodyBytes: DefaultMaxRequestBodyBytes,
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
				},
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be a valid X509 key pair", Path: []any{"config", "tls"}},
			),
		},
		{
			name: "disabled TLS ignores invalid certs",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled: false,
					CertPEM: []byte("cert"),
				},
			},
			after: Config{
				Address:             ":8080",
				IdleTimeout:         120 * time.Second,
				ReadHeaderTimeout:   10 * time.Second,
				MaxRequestBodyBytes: DefaultMaxRequestBodyBytes,
				TLS: integration.ConfigTLS{
					Enabled: false,
					CertPEM: []byte("cert"),
				},
			},
			err: nil,
		},
		{
			// A server has no use for the client-only fields, and without a key pair
			// it has nothing to listen with: enabled TLS is refused rather than
			// served as plaintext.
			name: "TLS enabled without a key pair returns error",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled:            true,
					InsecureSkipVerify: true,
				},
			},
			after: Config{
				Address:             ":8080",
				IdleTimeout:         120 * time.Second,
				ReadHeaderTimeout:   10 * time.Second,
				MaxRequestBodyBytes: DefaultMaxRequestBodyBytes,
				TLS: integration.ConfigTLS{
					Enabled:            true,
					InsecureSkipVerify: true,
				},
			},
			err: errorstack.NewValidation(
				errorstack.Entry{
					Message: "Must be set; a server requires cert_pem and key_pem",
					Path:    []any{"config", "tls"},
				},
			),
		},
		{
			name: "OpenAPI and TLS both invalid returns combined errors",
			before: Config{
				OpenAPI: ConfigOpenAPI{
					Enabled: true,
				},
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			after: Config{
				Address:             ":8080",
				IdleTimeout:         120 * time.Second,
				ReadHeaderTimeout:   10 * time.Second,
				MaxRequestBodyBytes: DefaultMaxRequestBodyBytes,
				OpenAPI: ConfigOpenAPI{
					Enabled: true,
				},
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "openapi", "descriptions"}},
				errorstack.Entry{
					Message: "Must be set together; cert_pem and key_pem are required as a pair",
					Path:    []any{"config", "tls"},
				},
			),
		},
		{
			name: "custom limits and timeouts are preserved",
			before: Config{
				IdleTimeout:         30 * time.Second,
				ReadHeaderTimeout:   5 * time.Second,
				ReadTimeout:         20 * time.Second,
				WriteTimeout:        25 * time.Second,
				RequestTimeout:      45 * time.Second,
				MaxRequestBodyBytes: 1024,
				MaxHeaderBytes:      4096,
			},
			after: Config{
				Address:             ":8080",
				IdleTimeout:         30 * time.Second,
				ReadHeaderTimeout:   5 * time.Second,
				ReadTimeout:         20 * time.Second,
				WriteTimeout:        25 * time.Second,
				RequestTimeout:      45 * time.Second,
				MaxRequestBodyBytes: 1024,
				MaxHeaderBytes:      4096,
			},
			err: nil,
		},
		{
			name: "zero RequestTimeout, ReadTimeout and WriteTimeout are left alone rather than defaulted",
			before: Config{
				RequestTimeout: 0,
				ReadTimeout:    0,
				WriteTimeout:   0,
			},
			after: Config{
				Address:             ":8080",
				IdleTimeout:         120 * time.Second,
				ReadHeaderTimeout:   10 * time.Second,
				MaxRequestBodyBytes: DefaultMaxRequestBodyBytes,
			},
			err: nil,
		},
		{
			name: "negative timeouts and limits return errors",
			before: Config{
				IdleTimeout:         -1 * time.Second,
				ReadHeaderTimeout:   -1 * time.Second,
				ReadTimeout:         -1 * time.Second,
				WriteTimeout:        -1 * time.Second,
				RequestTimeout:      -1 * time.Second,
				MaxRequestBodyBytes: -1,
				MaxHeaderBytes:      -1,
			},
			after: Config{
				Address:             ":8080",
				IdleTimeout:         -1 * time.Second,
				ReadHeaderTimeout:   -1 * time.Second,
				ReadTimeout:         -1 * time.Second,
				WriteTimeout:        -1 * time.Second,
				RequestTimeout:      -1 * time.Second,
				MaxRequestBodyBytes: -1,
				MaxHeaderBytes:      -1,
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be a positive duration", Path: []any{"config", "request_timeout"}},
				errorstack.Entry{Message: "Must be a positive duration", Path: []any{"config", "read_header_timeout"}},
				errorstack.Entry{Message: "Must be a positive duration", Path: []any{"config", "read_timeout"}},
				errorstack.Entry{Message: "Must be a positive duration", Path: []any{"config", "write_timeout"}},
				errorstack.Entry{Message: "Must be a positive duration", Path: []any{"config", "idle_timeout"}},
				errorstack.Entry{Message: "Must be a positive number of bytes", Path: []any{"config", "max_request_body_bytes"}},
				errorstack.Entry{Message: "Must be a positive number of bytes", Path: []any{"config", "max_header_bytes"}},
			),
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
