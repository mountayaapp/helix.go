package integration

import (
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigTLS_Sanitize(t *testing.T) {
	testcases := []struct {
		cfg         ConfigTLS
		validations []errorstack.Validation
	}{
		{
			cfg: ConfigTLS{
				Enabled: false,
			},
			validations: nil,
		},
		{
			cfg: ConfigTLS{
				Enabled: true,
			},
			validations: nil,
		},
		{
			cfg: ConfigTLS{
				Enabled:  true,
				CertFile: "cert.crt",
				KeyFile:  "cert.key",
			},
			validations: nil,
		},
		{
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
		validations := tc.cfg.Sanitize()
		assert.Equal(t, tc.validations, validations)
	}
}

func TestConfigTLS_ToStandardTLS(t *testing.T) {
	testcases := []struct {
		cfg                    ConfigTLS
		expectedSkipVerifyFlag bool
		expectNilConfig        bool
	}{
		{
			cfg: ConfigTLS{
				Enabled: false,
			},
			expectNilConfig: true,
		},
		{
			cfg: ConfigTLS{
				Enabled:            true,
				InsecureSkipVerify: false,
			},
			expectedSkipVerifyFlag: false,
			expectNilConfig:        false,
		},
		{
			cfg: ConfigTLS{
				Enabled:            true,
				InsecureSkipVerify: true,
			},
			expectedSkipVerifyFlag: true,
			expectNilConfig:        false,
		},
	}

	for _, tc := range testcases {
		tlsConfig, validations := tc.cfg.ToStandardTLS()

		require.Empty(t, validations, "ToStandardTLS should not return validations in this test setup.")

		if tc.expectNilConfig {
			assert.Nil(t, tlsConfig, "Config should be nil when disabled.")
		} else {
			require.NotNil(t, tlsConfig, "Config should not be nil when enabled.")
			assert.Equal(t, tc.expectedSkipVerifyFlag, tlsConfig.InsecureSkipVerify, "InsecureSkipVerify flag mismatch.")
		}
	}
}

func TestConfigTLS_ToStandardTLS_FullInsecureSkipVerify(t *testing.T) {
	cfg := ConfigTLS{
		Enabled:            true,
		InsecureSkipVerify: true,
	}

	tlsConfig, validations := cfg.ToStandardTLS()

	require.Empty(t, validations, "Expected no validation errors.")
	require.NotNil(t, tlsConfig, "Expected a non-nil *tls.Config.")

	assert.True(t, tlsConfig.InsecureSkipVerify, "InsecureSkipVerify must be true.")

	assert.Empty(t, tlsConfig.ServerName, "ServerName must be empty (zero value).")
	assert.Nil(t, tlsConfig.Certificates, "Certificates slice must be nil (no client cert provided).")
	assert.Nil(t, tlsConfig.RootCAs, "RootCAs pool must be nil (no RootCAFiles provided).")
}
