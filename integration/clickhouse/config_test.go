package clickhouse

import (
	"testing"
	"time"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_Sanitize(t *testing.T) {
	testcases := []struct {
		name   string
		before Config
		after  Config
		err    error
	}{
		{
			name:   "empty config applies all defaults but no password",
			before: Config{},
			after: Config{
				Address:     "127.0.0.1:9000",
				Database:    "default",
				User:        "default",
				Compression: "none",
			},
			err: nil,
		},
		{
			name: "custom address and credentials are preserved",
			before: Config{
				Address:  "clickhouse.example.com:9000",
				Database: "analytics",
				User:     "admin",
				Password: "secret",
			},
			after: Config{
				Address:     "clickhouse.example.com:9000",
				Database:    "analytics",
				User:        "admin",
				Password:    "secret",
				Compression: "none",
			},
			err: nil,
		},
		{
			name: "custom database preserves database and applies other defaults",
			before: Config{
				Database: "mydb",
			},
			after: Config{
				Address:     "127.0.0.1:9000",
				Database:    "mydb",
				User:        "default",
				Compression: "none",
			},
			err: nil,
		},
		{
			name: "custom user preserves user and applies other defaults",
			before: Config{
				User: "custom_user",
			},
			after: Config{
				Address:     "127.0.0.1:9000",
				Database:    "default",
				User:        "custom_user",
				Compression: "none",
			},
			err: nil,
		},
		{
			name: "custom password preserves password and applies other defaults",
			before: Config{
				Password: "my_secret",
			},
			after: Config{
				Address:     "127.0.0.1:9000",
				Database:    "default",
				User:        "default",
				Password:    "my_secret",
				Compression: "none",
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
				Address:     "127.0.0.1:9000",
				Database:    "default",
				User:        "default",
				Compression: "none",
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
				Address:     "127.0.0.1:9000",
				Database:    "default",
				User:        "default",
				Compression: "none",
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
			name: "TLS with both CertPEM and KeyPEM is valid",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
				},
			},
			after: Config{
				Address:     "127.0.0.1:9000",
				Database:    "default",
				User:        "default",
				Compression: "none",
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
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled: false,
					CertPEM: []byte("cert"),
				},
			},
			after: Config{
				Address:     "127.0.0.1:9000",
				Database:    "default",
				User:        "default",
				Compression: "none",
				TLS: integration.ConfigTLS{
					Enabled: false,
					CertPEM: []byte("cert"),
				},
			},
			err: nil,
		},
		{
			name: "TLS with InsecureSkipVerify is valid",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled:            true,
					InsecureSkipVerify: true,
				},
			},
			after: Config{
				Address:     "127.0.0.1:9000",
				Database:    "default",
				User:        "default",
				Compression: "none",
				TLS: integration.ConfigTLS{
					Enabled:            true,
					InsecureSkipVerify: true,
				},
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

// TestConfig_Sanitize_NoDefaultPassword pins that no credential is invented: a
// password left empty is sent empty, which is what the stock "default" user of a
// fresh server expects, rather than the literal "default" it used to become.
func TestConfig_Sanitize_NoDefaultPassword(t *testing.T) {
	cfg := Config{}
	require.NoError(t, cfg.sanitize())
	assert.Empty(t, cfg.Password)

	opts, err := cfg.options()
	require.NoError(t, err)
	assert.Empty(t, opts.Auth.Password)
}

func TestConfig_Sanitize_RejectsInvalidPoolAndTimeouts(t *testing.T) {
	cfg := Config{
		DialTimeout:     -time.Second,
		ReadTimeout:     -time.Second,
		ConnMaxLifetime: -time.Second,
		MaxOpenConns:    -1,
		MaxIdleConns:    -1,
		Compression:     "gzip",
	}

	err := cfg.sanitize()
	require.Error(t, err)

	var paths [][]any
	for _, entry := range errorstack.EntriesOf(err) {
		assert.Equal(t, errorstack.CodeValidationFailed, entry.Extensions["code"])
		paths = append(paths, entry.Path)
	}

	assert.ElementsMatch(t, [][]any{
		{"config", "compression"},
		{"config", "dial_timeout"},
		{"config", "read_timeout"},
		{"config", "conn_max_lifetime"},
		{"config", "max_open_conns"},
		{"config", "max_idle_conns"},
	}, paths)
}

func TestConfig_Options(t *testing.T) {
	settings := map[string]any{"max_execution_time": 60}
	cfg := Config{
		Address:         "clickhouse.example.com:9440",
		Database:        "analytics",
		User:            "reader",
		Password:        "secret",
		DialTimeout:     5 * time.Second,
		ReadTimeout:     time.Minute,
		MaxOpenConns:    20,
		MaxIdleConns:    10,
		ConnMaxLifetime: 30 * time.Minute,
		Compression:     "zstd",
		Settings:        settings,
	}
	require.NoError(t, cfg.sanitize())

	opts, err := cfg.options()
	require.NoError(t, err)

	assert.Equal(t, []string{"clickhouse.example.com:9440"}, opts.Addr)
	assert.Equal(t, clickhouse.Auth{Database: "analytics", Username: "reader", Password: "secret"}, opts.Auth)
	assert.Equal(t, 5*time.Second, opts.DialTimeout)
	assert.Equal(t, time.Minute, opts.ReadTimeout)
	assert.Equal(t, 20, opts.MaxOpenConns)
	assert.Equal(t, 10, opts.MaxIdleConns)
	assert.Equal(t, 30*time.Minute, opts.ConnMaxLifetime)
	assert.Equal(t, clickhouse.CompressionZSTD, opts.Compression.Method)
	assert.Equal(t, clickhouse.Settings{"max_execution_time": 60}, opts.Settings)
	assert.Nil(t, opts.TLS)

	// The driver reads the settings for the life of the pool; the caller's map
	// changing afterwards must not reach it.
	settings["max_execution_time"] = 1
	assert.Equal(t, 60, opts.Settings["max_execution_time"])
}

func TestConfig_Options_ZeroValuesLeaveDriverDefaults(t *testing.T) {
	cfg := Config{}
	require.NoError(t, cfg.sanitize())

	opts, err := cfg.options()
	require.NoError(t, err)

	assert.Zero(t, opts.DialTimeout)
	assert.Zero(t, opts.ReadTimeout)
	assert.Zero(t, opts.MaxOpenConns)
	assert.Zero(t, opts.MaxIdleConns)
	assert.Zero(t, opts.ConnMaxLifetime)
	assert.Equal(t, clickhouse.CompressionNone, opts.Compression.Method)
	assert.Nil(t, opts.Settings)
}

func TestConfig_Options_TLS(t *testing.T) {
	cfg := Config{
		Address: "clickhouse.example.com:9440",
		TLS:     integration.ConfigTLS{Enabled: true},
	}
	require.NoError(t, cfg.sanitize())

	opts, err := cfg.options()
	require.NoError(t, err)
	require.NotNil(t, opts.TLS)

	// Left empty on purpose: the driver dials with crypto/tls, which verifies the
	// certificate against the host it dials when ServerName is empty.
	assert.Empty(t, opts.TLS.ServerName)
	assert.False(t, opts.TLS.InsecureSkipVerify)
}

func TestConfig_Options_InvalidTLS(t *testing.T) {
	cfg := Config{
		TLS: integration.ConfigTLS{
			Enabled: true,
			CertPEM: []byte("cert"),
			KeyPEM:  []byte("key"),
		},
	}
	require.NoError(t, cfg.sanitize())

	opts, err := cfg.options()
	assert.Nil(t, opts)
	assert.Equal(t, errorstack.NewValidation(errorstack.Entry{
		Message: "Must be a valid X509 key pair",
		Path:    []any{"config", "tls"},
	}), err)
}
