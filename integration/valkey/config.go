package valkey

import (
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
)

// Config is used to configure the Valkey integration.
type Config struct {

	// Name identifies this connection in health output and error context. Two
	// pools of the same kind — a primary and a replica — are told apart by it.
	// Defaults to the integration's identifier when left empty.
	Name string `json:"name"`

	// Address is the Valkey address to connect to. When the server runs in
	// cluster mode, the client discovers the other nodes from it.
	//
	// Default:
	//
	//   "127.0.0.1:6379"
	Address string `json:"address"`

	// User is the user to use to connect to the database.
	User string `json:"-"`

	// Password is the user's password to connect to the database.
	Password string `json:"-"`

	// Database is the numbered database every connection selects. A server in
	// cluster mode only accepts 0 before Valkey 9.
	//
	// Default:
	//
	//   0
	Database int `json:"database"`

	// ClientName is the name every connection registers with the server, as
	// reported by CLIENT LIST. Left empty, connections are not named.
	ClientName string `json:"client_name"`

	// DialTimeout bounds opening a connection: dialing it, and then the handshake
	// on it, are each given this long. Zero leaves the driver's default, 5
	// seconds.
	DialTimeout time.Duration `json:"dial_timeout"`

	// WriteTimeout bounds how long a connection waits for the server to answer the
	// commands written to it, beyond which the connection is considered dead and
	// closed. It does not apply to blocking commands. Zero leaves the driver's
	// default, 10 seconds.
	WriteTimeout time.Duration `json:"write_timeout"`

	// RecordKeys records the names a command is given — keys, key patterns,
	// stream and channel names — on its span, under valkey.key, valkey.keys,
	// valkey.pattern and valkey.channel.
	//
	// It is off by default because those names routinely embed session tokens,
	// user identifiers and email addresses, and a span exported to a tracing
	// backend is not where they belong. Turn it on where the key space carries
	// nothing sensitive.
	RecordKeys bool `json:"record_keys"`

	// TLS configures TLS to communicate with the Valkey server. The server name
	// verified is ServerName when set, and the host of the address dialed
	// otherwise.
	TLS integration.ConfigTLS `json:"tls"`

	// SubscriberBacklog bounds how far one Subscribe's handler may fall behind its
	// channel, in messages received and not yet handled. Past it the subscription
	// is ended with ErrSubscriberTooSlow: the messages of every channel arrive on
	// the connection the subscriptions share, so a subscriber that cannot keep up
	// is let go rather than waited for or buffered without end.
	//
	// Default:
	//
	//   1024
	SubscriberBacklog int `json:"subscriber_backlog"`

	// SubscriberBacklogBytes bounds the same backlog in payload bytes, for a
	// channel whose messages are large. A message larger than the bound is still
	// delivered to a subscriber with nothing waiting.
	//
	// Default:
	//
	//   33554432 (32 MiB)
	SubscriberBacklogBytes int `json:"subscriber_backlog_bytes"`
}

// sanitize sets default values - when applicable - and validates the configuration.
// Returns an error if configuration is not valid.
func (cfg *Config) sanitize() error {
	var entries []errorstack.Entry
	if cfg.Address == "" {
		cfg.Address = "127.0.0.1:6379"
	}

	if cfg.Database < 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must not be negative",
			Path:    []any{"config", "database"},
		})
	}

	if cfg.DialTimeout < 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must be a positive duration",
			Path:    []any{"config", "dial_timeout"},
		})
	}

	if cfg.WriteTimeout < 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must be a positive duration",
			Path:    []any{"config", "write_timeout"},
		})
	}

	switch {
	case cfg.SubscriberBacklog < 0:
		entries = append(entries, errorstack.Entry{
			Message: "Must not be negative",
			Path:    []any{"config", "subscriber_backlog"},
		})
	case cfg.SubscriberBacklog == 0:
		cfg.SubscriberBacklog = defaultSubscriberBacklog
	}

	switch {
	case cfg.SubscriberBacklogBytes < 0:
		entries = append(entries, errorstack.Entry{
			Message: "Must not be negative",
			Path:    []any{"config", "subscriber_backlog_bytes"},
		})
	case cfg.SubscriberBacklogBytes == 0:
		cfg.SubscriberBacklogBytes = defaultSubscriberBacklogBytes
	}

	entries = append(entries, cfg.TLS.Sanitize()...)
	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	return nil
}

// name returns the name the connection registers under, falling back to the
// integration's identifier when the Config leaves it empty.
func (cfg *Config) name() string {
	if cfg.Name != "" {
		return cfg.Name
	}

	return identifier
}
