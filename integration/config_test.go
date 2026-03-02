package integration

import (
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
			name: "enabled TLS with both CertFile and KeyFile has no validations",
			cfg: ConfigTLS{
				Enabled:  true,
				CertFile: "cert.crt",
				KeyFile:  "cert.key",
			},
			validations: nil,
		},
		{
			name: "TLS with only CertFile returns error",
			cfg: ConfigTLS{
				Enabled:  true,
				CertFile: "cert.crt",
				KeyFile:  "",
			},
			validations: []errorstack.Validation{
				{
					Message: "CertFile and KeyFile must be set together or neither must be set",
					Path:    []string{"Config", "TLS"},
				},
			},
		},
		{
			name: "TLS with only KeyFile returns error",
			cfg: ConfigTLS{
				Enabled:  true,
				CertFile: "",
				KeyFile:  "cert.key",
			},
			validations: []errorstack.Validation{
				{
					Message: "CertFile and KeyFile must be set together or neither must be set",
					Path:    []string{"Config", "TLS"},
				},
			},
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

func TestConfigTLS_Sanitize_DisabledIgnoresInvalidFields(t *testing.T) {
	cfg := ConfigTLS{
		Enabled:  false,
		CertFile: "cert.crt",
		KeyFile:  "",
	}

	validations := cfg.Sanitize()

	assert.Empty(t, validations)
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

func TestConfigTLS_ToStandardTLS_InvalidCertFile(t *testing.T) {
	cfg := ConfigTLS{
		Enabled:  true,
		CertFile: "/nonexistent/cert.crt",
		KeyFile:  "/nonexistent/cert.key",
	}

	tlsConfig, validations := cfg.ToStandardTLS()

	assert.Nil(t, tlsConfig)
	assert.NotEmpty(t, validations)
}

func TestConfigTLS_ToStandardTLS_InvalidRootCAFile(t *testing.T) {
	cfg := ConfigTLS{
		Enabled:     true,
		RootCAFiles: []string{"/nonexistent/ca.crt"},
	}

	tlsConfig, validations := cfg.ToStandardTLS()

	assert.Nil(t, tlsConfig)
	assert.NotEmpty(t, validations)
}
