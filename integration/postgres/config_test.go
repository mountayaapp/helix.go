package postgres

import (
	"testing"
	"time"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"

	"github.com/stretchr/testify/assert"
)

func TestConfig_Sanitize(t *testing.T) {
	testcases := []struct {
		name   string
		before Config
		after  Config
		err    error
	}{
		{
			name:   "empty config returns required field errors",
			before: Config{},
			after: Config{
				Address: "127.0.0.1:5432",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "database"}},
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "user"}},
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "password"}},
			),
		},
		{
			name: "valid config with all required fields",
			before: Config{
				Database: "mydb",
				User:     "admin",
				Password: "secret",
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
			},
			err: nil,
		},
		{
			name: "custom address and credentials are preserved",
			before: Config{
				Address:  "postgres.example.com:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
			},
			after: Config{
				Address:  "postgres.example.com:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
			},
			err: nil,
		},
		{
			name: "missing only database returns database error",
			before: Config{
				User:     "admin",
				Password: "secret",
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				User:     "admin",
				Password: "secret",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "database"}},
			),
		},
		{
			name: "missing only user returns user error",
			before: Config{
				Database: "mydb",
				Password: "secret",
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				Password: "secret",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "user"}},
			),
		},
		{
			name: "missing only password returns password error",
			before: Config{
				Database: "mydb",
				User:     "admin",
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "password"}},
			),
		},
		{
			name: "a time zone the time zone database knows is preserved",
			before: Config{
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TimeZone: "Europe/Paris",
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TimeZone: "Europe/Paris",
			},
			err: nil,
		},
		{
			name: "an unknown time zone returns error",
			before: Config{
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TimeZone: "Mars/Olympus_Mons",
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TimeZone: "Mars/Olympus_Mons",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: `Must be an IANA time zone name, such as "UTC"`, Path: []any{"config", "time_zone"}},
			),
		},
		{
			name: "the process's Local zone is not a time zone the server knows",
			before: Config{
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TimeZone: "Local",
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TimeZone: "Local",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: `Must be an IANA time zone name, such as "UTC"`, Path: []any{"config", "time_zone"}},
			),
		},
		{
			name: "TLS with only CertPEM returns error",
			before: Config{
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
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
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TLS: integration.ConfigTLS{
					Enabled: true,
					KeyPEM:  []byte("key"),
				},
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
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
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
				},
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
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
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TLS: integration.ConfigTLS{
					Enabled: false,
					CertPEM: []byte("cert"),
				},
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
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
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TLS: integration.ConfigTLS{
					Enabled:            true,
					InsecureSkipVerify: true,
				},
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TLS: integration.ConfigTLS{
					Enabled:            true,
					InsecureSkipVerify: true,
				},
			},
			err: nil,
		},
		{
			name: "pool settings are preserved",
			before: Config{
				Database:          "mydb",
				User:              "admin",
				Password:          "secret",
				MaxConns:          10,
				MinConns:          10,
				MaxConnLifetime:   time.Hour,
				MaxConnIdleTime:   time.Minute,
				HealthCheckPeriod: time.Minute,
				ConnectTimeout:    time.Second,
			},
			after: Config{
				Address:           "127.0.0.1:5432",
				Database:          "mydb",
				User:              "admin",
				Password:          "secret",
				MaxConns:          10,
				MinConns:          10,
				MaxConnLifetime:   time.Hour,
				MaxConnIdleTime:   time.Minute,
				HealthCheckPeriod: time.Minute,
				ConnectTimeout:    time.Second,
			},
			err: nil,
		},
		{
			name: "negative pool settings return errors",
			before: Config{
				Database:          "mydb",
				User:              "admin",
				Password:          "secret",
				MaxConns:          -1,
				MinConns:          -1,
				MaxConnLifetime:   -time.Second,
				MaxConnIdleTime:   -time.Second,
				HealthCheckPeriod: -time.Second,
				ConnectTimeout:    -time.Second,
			},
			after: Config{
				Address:           "127.0.0.1:5432",
				Database:          "mydb",
				User:              "admin",
				Password:          "secret",
				MaxConns:          -1,
				MinConns:          -1,
				MaxConnLifetime:   -time.Second,
				MaxConnIdleTime:   -time.Second,
				HealthCheckPeriod: -time.Second,
				ConnectTimeout:    -time.Second,
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must not be negative", Path: []any{"config", "max_conns"}},
				errorstack.Entry{Message: "Must not be negative", Path: []any{"config", "min_conns"}},
				errorstack.Entry{Message: "Must not be negative", Path: []any{"config", "max_conn_lifetime"}},
				errorstack.Entry{Message: "Must not be negative", Path: []any{"config", "max_conn_idle_time"}},
				errorstack.Entry{Message: "Must not be negative", Path: []any{"config", "health_check_period"}},
				errorstack.Entry{Message: "Must not be negative", Path: []any{"config", "connect_timeout"}},
			),
		},
		{
			name: "min conns above max conns returns error",
			before: Config{
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				MaxConns: 2,
				MinConns: 3,
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				MaxConns: 2,
				MinConns: 3,
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must not be greater than max_conns", Path: []any{"config", "min_conns"}},
			),
		},
		{
			name: "min conns with the default max conns is valid",
			before: Config{
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				MinConns: 3,
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				MinConns: 3,
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
