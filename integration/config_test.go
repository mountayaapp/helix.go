package integration

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigTLS_Sanitize(t *testing.T) {
	testcases := []struct {
		name        string
		cfg         ConfigTLS
		validations []errorstack.Validation
	}{
		{
			name: "disabled TLS has no validations",
			cfg: ConfigTLS{
				Enabled: false,
			},
			validations: nil,
		},
		{
			name: "enabled TLS without certs has no validations",
			cfg: ConfigTLS{
				Enabled: true,
			},
			validations: nil,
		},
		{
			name: "enabled TLS with both CertPEM and KeyPEM has no validations",
			cfg: ConfigTLS{
				Enabled: true,
				CertPEM: []byte("cert"),
				KeyPEM:  []byte("key"),
			},
			validations: nil,
		},
		{
			name: "TLS with only CertPEM returns error",
			cfg: ConfigTLS{
				Enabled: true,
				CertPEM: []byte("cert"),
			},
			validations: []errorstack.Validation{
				{
					Message: "CertPEM and KeyPEM must be set together or neither must be set",
					Path:    []string{"Config", "TLS"},
				},
			},
		},
		{
			name: "TLS with only KeyPEM returns error",
			cfg: ConfigTLS{
				Enabled: true,
				KeyPEM:  []byte("key"),
			},
			validations: []errorstack.Validation{
				{
					Message: "CertPEM and KeyPEM must be set together or neither must be set",
					Path:    []string{"Config", "TLS"},
				},
			},
		},
		{
			name: "disabled TLS ignores invalid CertPEM",
			cfg: ConfigTLS{
				Enabled: false,
				CertPEM: []byte("cert"),
			},
			validations: nil,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			validations := tc.cfg.Sanitize()
			assert.Equal(t, tc.validations, validations)
		})
	}
}

func TestConfigTLS_ToStandardTLS(t *testing.T) {
	testcases := []struct {
		name                   string
		cfg                    ConfigTLS
		expectedSkipVerifyFlag bool
		expectNilConfig        bool
	}{
		{
			name: "disabled TLS returns nil config",
			cfg: ConfigTLS{
				Enabled: false,
			},
			expectNilConfig: true,
		},
		{
			name: "enabled TLS without InsecureSkipVerify",
			cfg: ConfigTLS{
				Enabled:            true,
				InsecureSkipVerify: false,
			},
			expectedSkipVerifyFlag: false,
			expectNilConfig:        false,
		},
		{
			name: "enabled TLS with InsecureSkipVerify",
			cfg: ConfigTLS{
				Enabled:            true,
				InsecureSkipVerify: true,
			},
			expectedSkipVerifyFlag: true,
			expectNilConfig:        false,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			tlsConfig, validations := tc.cfg.ToStandardTLS()

			require.Empty(t, validations)

			if tc.expectNilConfig {
				assert.Nil(t, tlsConfig)
			} else {
				require.NotNil(t, tlsConfig)
				assert.Equal(t, tc.expectedSkipVerifyFlag, tlsConfig.InsecureSkipVerify)
			}
		})
	}
}

func TestConfigTLS_ToStandardTLS_FullInsecureSkipVerify(t *testing.T) {
	cfg := ConfigTLS{
		Enabled:            true,
		InsecureSkipVerify: true,
	}

	tlsConfig, validations := cfg.ToStandardTLS()

	require.Empty(t, validations)
	require.NotNil(t, tlsConfig)

	assert.True(t, tlsConfig.InsecureSkipVerify)

	assert.Empty(t, tlsConfig.ServerName)
	assert.Nil(t, tlsConfig.Certificates)
	assert.Nil(t, tlsConfig.RootCAs)
}

func TestConfigTLS_ToStandardTLS_WithServerName(t *testing.T) {
	cfg := ConfigTLS{
		Enabled:    true,
		ServerName: "api.example.com",
	}

	tlsConfig, validations := cfg.ToStandardTLS()

	require.Empty(t, validations)
	require.NotNil(t, tlsConfig)
	assert.Equal(t, "api.example.com", tlsConfig.ServerName)
}

func TestConfigTLS_ToStandardTLS_InvalidCertPEM(t *testing.T) {
	cfg := ConfigTLS{
		Enabled: true,
		CertPEM: []byte("invalid"),
		KeyPEM:  []byte("invalid"),
	}

	tlsConfig, validations := cfg.ToStandardTLS()

	assert.Nil(t, tlsConfig)
	assert.NotEmpty(t, validations)
}

func TestConfigTLS_ToStandardTLS_InvalidRootCAPEM(t *testing.T) {
	cfg := ConfigTLS{
		Enabled:    true,
		RootCAPEMs: [][]byte{[]byte("invalid")},
	}

	tlsConfig, validations := cfg.ToStandardTLS()

	assert.Nil(t, tlsConfig)
	assert.NotEmpty(t, validations)
}

func TestConfigTLS_ToStandardTLS_MultipleInvalidRootCAPEMs(t *testing.T) {
	cfg := ConfigTLS{
		Enabled: true,
		RootCAPEMs: [][]byte{
			[]byte("invalid"),
			[]byte("invalid"),
			[]byte("invalid"),
		},
	}

	tlsConfig, validations := cfg.ToStandardTLS()

	assert.Nil(t, tlsConfig)
	require.Len(t, validations, 3)
	for _, v := range validations {
		assert.NotEmpty(t, v.Message)
	}
}

func TestConfigTLS_ToStandardTLS_NoCertsNoCAs(t *testing.T) {
	cfg := ConfigTLS{
		Enabled: true,
	}

	tlsConfig, validations := cfg.ToStandardTLS()

	require.Empty(t, validations)
	require.NotNil(t, tlsConfig)
	assert.Nil(t, tlsConfig.Certificates)
	assert.Nil(t, tlsConfig.RootCAs)
	assert.False(t, tlsConfig.InsecureSkipVerify)
	assert.Empty(t, tlsConfig.ServerName)
}

func TestConfigTLS_Sanitize_NormalizesPEM(t *testing.T) {
	original := "-----BEGIN CERTIFICATE-----\nMIIB\ntest\n-----END CERTIFICATE-----\n"

	testcases := []struct {
		name  string
		input string
	}{
		{
			name:  "literal backslash-n",
			input: `-----BEGIN CERTIFICATE-----\nMIIB\ntest\n-----END CERTIFICATE-----\n`,
		},
		{
			name:  "literal backslash-backslash-n",
			input: `-----BEGIN CERTIFICATE-----\\nMIIB\\ntest\\n-----END CERTIFICATE-----\\n`,
		},
		{
			name:  "literal backslash-r-backslash-n",
			input: `-----BEGIN CERTIFICATE-----\r\nMIIB\r\ntest\r\n-----END CERTIFICATE-----\r\n`,
		},
		{
			name:  "real newlines unchanged",
			input: original,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ConfigTLS{
				Enabled: true,
				CertPEM: []byte(tc.input),
				KeyPEM:  []byte(tc.input),
				RootCAPEMs: [][]byte{
					[]byte(tc.input),
					[]byte(tc.input),
				},
			}

			cfg.Sanitize()

			assert.Equal(t, original, string(cfg.CertPEM))
			assert.Equal(t, original, string(cfg.KeyPEM))
			require.Len(t, cfg.RootCAPEMs, 2)
			assert.Equal(t, original, string(cfg.RootCAPEMs[0]))
			assert.Equal(t, original, string(cfg.RootCAPEMs[1]))
		})
	}
}

func TestConfigTLS_Sanitize_DisabledSkipsNormalization(t *testing.T) {
	input := []byte(`-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n`)
	original := make([]byte, len(input))
	copy(original, input)

	cfg := ConfigTLS{
		Enabled: false,
		CertPEM: input,
	}

	cfg.Sanitize()

	assert.Equal(t, original, cfg.CertPEM)
}

// Test PEM data generated with:
//
//	openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
//	  -keyout /dev/stdout -out /dev/stdout -days 3650 -nodes -subj '/CN=test'
var (
	testCertPEM = []byte(`-----BEGIN CERTIFICATE-----
MIIBczCCARmgAwIBAgIUc6WZjHQcu5PCpiBootjEPjLTycIwCgYIKoZIzj0EAwIw
DzENMAsGA1UEAwwEdGVzdDAeFw0yNjAzMzAxOTAyNTFaFw0zNjAzMjcxOTAyNTFa
MA8xDTALBgNVBAMMBHRlc3QwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAAQoV73S
jZCY6O5+Pxnxnd8foaZ4lfqMQK4J8j3XctBiMYr5tSAqq64o6o0/JXxAQXUMb4ry
ZetsKFjKLMlgU+beo1MwUTAdBgNVHQ4EFgQUd7s+al96Z8e53qOIoowTt8x8vQ0w
HwYDVR0jBBgwFoAUd7s+al96Z8e53qOIoowTt8x8vQ0wDwYDVR0TAQH/BAUwAwEB
/zAKBggqhkjOPQQDAgNIADBFAiEAu8B2o9frMKJqCs86gs0atPcebUni2Y60co1a
11W+6V0CIEsbsMEPztX8/i4TYVDbPXRkwGPIWuXaWq05+4VOEY/5
-----END CERTIFICATE-----`)

	testKeyPEM = []byte(`-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgomDqFjAnZoyQLc4w
lAmiUCfU+kk6UlGTTuffpISAA1uhRANCAAQoV73SjZCY6O5+Pxnxnd8foaZ4lfqM
QK4J8j3XctBiMYr5tSAqq64o6o0/JXxAQXUMb4ryZetsKFjKLMlgU+be
-----END PRIVATE KEY-----`)
)

func TestConfigTLS_ToStandardTLS_WithLiteralNewlines(t *testing.T) {
	// Simulate environment variable behavior: replace real newlines with literal \n.
	mangledCert := bytes.ReplaceAll(testCertPEM, []byte("\n"), []byte(`\n`))
	mangledKey := bytes.ReplaceAll(testKeyPEM, []byte("\n"), []byte(`\n`))

	// Ensure we actually mangled the data (no real newlines remain).
	require.False(t, strings.Contains(string(mangledCert), "\n"))
	require.False(t, strings.Contains(string(mangledKey), "\n"))

	cfg := ConfigTLS{
		Enabled:    true,
		CertPEM:    mangledCert,
		KeyPEM:     mangledKey,
		RootCAPEMs: [][]byte{mangledCert},
	}

	validations := cfg.Sanitize()
	require.Empty(t, validations)

	tlsConfig, validations := cfg.ToStandardTLS()
	require.Empty(t, validations)
	require.NotNil(t, tlsConfig)
	assert.Len(t, tlsConfig.Certificates, 1)
	assert.NotNil(t, tlsConfig.RootCAs)
}
