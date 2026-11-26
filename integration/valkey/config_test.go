package valkey

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
			name:   "empty config applies default address",
			before: Config{},
			after: Config{
				Address:                "127.0.0.1:6379",
				SubscriberBacklog:      defaultSubscriberBacklog,
				SubscriberBacklogBytes: defaultSubscriberBacklogBytes,
			},
			err: nil,
		},
		{
			name: "custom address and credentials are preserved",
			before: Config{
				Address:  "valkey.example.com:6379",
				User:     "admin",
				Password: "secret",
			},
			after: Config{
				Address:                "valkey.example.com:6379",
				SubscriberBacklog:      defaultSubscriberBacklog,
				SubscriberBacklogBytes: defaultSubscriberBacklogBytes,
				User:                   "admin",
				Password:               "secret",
			},
			err: nil,
		},
		{
			name: "custom address preserves address",
			before: Config{
				Address: "valkey.custom.com:6380",
			},
			after: Config{
				Address:                "valkey.custom.com:6380",
				SubscriberBacklog:      defaultSubscriberBacklog,
				SubscriberBacklogBytes: defaultSubscriberBacklogBytes,
			},
			err: nil,
		},
		{
			name: "custom user and password without address applies default address",
			before: Config{
				User:     "myuser",
				Password: "mypassword",
			},
			after: Config{
				Address:                "127.0.0.1:6379",
				SubscriberBacklog:      defaultSubscriberBacklog,
				SubscriberBacklogBytes: defaultSubscriberBacklogBytes,
				User:                   "myuser",
				Password:               "mypassword",
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
				Address:                "127.0.0.1:6379",
				SubscriberBacklog:      defaultSubscriberBacklog,
				SubscriberBacklogBytes: defaultSubscriberBacklogBytes,
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
				Address:                "127.0.0.1:6379",
				SubscriberBacklog:      defaultSubscriberBacklog,
				SubscriberBacklogBytes: defaultSubscriberBacklogBytes,
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
				Address:                "127.0.0.1:6379",
				SubscriberBacklog:      defaultSubscriberBacklog,
				SubscriberBacklogBytes: defaultSubscriberBacklogBytes,
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
				Address:                "127.0.0.1:6379",
				SubscriberBacklog:      defaultSubscriberBacklog,
				SubscriberBacklogBytes: defaultSubscriberBacklogBytes,
				TLS: integration.ConfigTLS{
					Enabled: false,
					CertPEM: []byte("cert"),
				},
			},
			err: nil,
		},
		{
			name: "database, client name and timeouts are preserved",
			before: Config{
				Database:     3,
				ClientName:   "worker",
				DialTimeout:  time.Second,
				WriteTimeout: 2 * time.Second,
				RecordKeys:   true,
			},
			after: Config{
				Address:                "127.0.0.1:6379",
				SubscriberBacklog:      defaultSubscriberBacklog,
				SubscriberBacklogBytes: defaultSubscriberBacklogBytes,
				Database:               3,
				ClientName:             "worker",
				DialTimeout:            time.Second,
				WriteTimeout:           2 * time.Second,
				RecordKeys:             true,
			},
			err: nil,
		},
		{
			name: "a subscriber backlog is preserved",
			before: Config{
				SubscriberBacklog:      64,
				SubscriberBacklogBytes: 1 << 20,
			},
			after: Config{
				Address:                "127.0.0.1:6379",
				SubscriberBacklog:      64,
				SubscriberBacklogBytes: 1 << 20,
			},
			err: nil,
		},
		{
			name: "a negative subscriber backlog returns errors",
			before: Config{
				SubscriberBacklog:      -1,
				SubscriberBacklogBytes: -1,
			},
			after: Config{
				Address:                "127.0.0.1:6379",
				SubscriberBacklog:      -1,
				SubscriberBacklogBytes: -1,
			},
			err: errorstack.NewValidation(
				errorstack.Entry{
					Message: "Must not be negative",
					Path:    []any{"config", "subscriber_backlog"},
				},
				errorstack.Entry{
					Message: "Must not be negative",
					Path:    []any{"config", "subscriber_backlog_bytes"},
				},
			),
		},
		{
			name: "negative database and timeouts return errors alongside TLS ones",
			before: Config{
				Database:     -1,
				DialTimeout:  -time.Second,
				WriteTimeout: -time.Second,
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			after: Config{
				Address:                "127.0.0.1:6379",
				SubscriberBacklog:      defaultSubscriberBacklog,
				SubscriberBacklogBytes: defaultSubscriberBacklogBytes,
				Database:               -1,
				DialTimeout:            -time.Second,
				WriteTimeout:           -time.Second,
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			err: errorstack.NewValidation(
				errorstack.Entry{
					Message: "Must not be negative",
					Path:    []any{"config", "database"},
				},
				errorstack.Entry{
					Message: "Must be a positive duration",
					Path:    []any{"config", "dial_timeout"},
				},
				errorstack.Entry{
					Message: "Must be a positive duration",
					Path:    []any{"config", "write_timeout"},
				},
				errorstack.Entry{
					Message: "Must be set together; cert_pem and key_pem are required as a pair",
					Path:    []any{"config", "tls"},
				},
			),
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
				Address:                "127.0.0.1:6379",
				SubscriberBacklog:      defaultSubscriberBacklog,
				SubscriberBacklogBytes: defaultSubscriberBacklogBytes,
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
