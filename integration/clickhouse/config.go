package clickhouse

import (
	"maps"
	"time"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// Config is used to configure the ClickHouse integration.
//
// Every pool and timeout field left at its zero value takes the driver's own
// default, which is what each field documents.
type Config struct {
	// Name identifies this connection in health output and error context. Two
	// pools of the same kind — a primary and a replica — are told apart by it.
	// Defaults to the integration's identifier when left empty.
	Name string `json:"name"`

	// Address is the ClickHouse address to connect to, over the native protocol.
	//
	// Default:
	//
	//   "127.0.0.1:9000"
	Address string `json:"address"`

	// Database is the database to connect to.
	//
	// Default:
	//
	//   "default"
	Database string `json:"-"`

	// User is the user to use to connect to the database.
	//
	// Default:
	//
	//   "default"
	User string `json:"-"`

	// Password is the user's password to connect to the database. It has no
	// default: an empty password is sent as such, which is what the stock
	// "default" user of a fresh server expects.
	Password string `json:"-"`

	// TLS configures TLS to communicate with the ClickHouse database. The
	// server's certificate is verified against the host of Address unless
	// TLS.ServerName says otherwise.
	TLS integration.ConfigTLS `json:"tls"`

	// DialTimeout bounds how long opening a connection, and waiting for one to
	// come free in the pool, may take. It is the only bound on a dial and its
	// handshake: the driver runs them whatever the context of the call says, so a
	// call whose context ends sooner still waits out the dial to a server that
	// does not answer. Set it to what a caller can afford to wait on a hung server.
	//
	// Default:
	//
	//   30 * time.Second
	DialTimeout time.Duration `json:"dial_timeout"`

	// ReadTimeout bounds how long a statement whose context has no deadline may
	// wait on the server's answer, from the first read to the last. A context
	// deadline on a call takes precedence.
	//
	// Default:
	//
	//   5 * time.Minute
	ReadTimeout time.Duration `json:"read_timeout"`

	// MaxOpenConns caps the connections the pool holds open at once, idle and
	// acquired together.
	//
	// Default:
	//
	//   MaxIdleConns + 5
	MaxOpenConns int `json:"max_open_conns"`

	// MaxIdleConns caps the connections the pool keeps open while unused.
	//
	// Default:
	//
	//   5
	MaxIdleConns int `json:"max_idle_conns"`

	// ConnMaxLifetime is how long a connection is reused before it is closed and
	// replaced.
	//
	// Default:
	//
	//   time.Hour
	ConnMaxLifetime time.Duration `json:"conn_max_lifetime"`

	// Compression is the method blocks are compressed with on the wire: "lz4",
	// "lz4hc", "zstd" or "none".
	//
	// Default:
	//
	//   "none"
	Compression string `json:"compression"`

	// Settings are ClickHouse settings applied to every statement this connection
	// runs, such as {"max_execution_time": 60}. A statement can still override
	// them through clickhouse.Context and clickhouse.WithSettings.
	Settings map[string]any `json:"settings"`

	// Optional marks the database as one the Service can run without, for a
	// while. It changes two things:
	//
	//   - Connect does not fail when the server cannot be reached. The connection
	//     is attached all the same, and the pool dials the server again on the
	//     next statement; until it answers, every statement fails as one to a
	//     server that is down does. A server that answers and refuses the login,
	//     or the database, still fails Connect: that is a configuration to fix,
	//     not an outage to wait out, and so does a TLS handshake that fails. A
	//     name that does not resolve is waited out: its record can come back.
	//   - The connection's health does not gate the Service's readiness: its
	//     health check answers at once, without a ping, so a server that is down
	//     or hangs leaves every replica ready. Whether it answers is reported on
	//     the clickhouse.reachable gauge instead, which a background ping keeps
	//     current.
	//
	// Set it for a service whose work does not all need the database, and that
	// handles a failed statement itself.
	//
	// Default:
	//
	//   false
	Optional bool `json:"optional"`
}

// compressionMethods maps the Compression values the native protocol supports to
// the driver's own. The driver accepts others — gzip, deflate, brotli — over HTTP
// only, and fails every dial with them over the native protocol.
var compressionMethods = map[string]clickhouse.CompressionMethod{
	"none":  clickhouse.CompressionNone,
	"lz4":   clickhouse.CompressionLZ4,
	"lz4hc": clickhouse.CompressionLZ4HC,
	"zstd":  clickhouse.CompressionZSTD,
}

// sanitize sets default values - when applicable - and validates the configuration.
// Returns an error if configuration is not valid.
func (cfg *Config) sanitize() error {
	var entries []errorstack.Entry

	if cfg.Address == "" {
		cfg.Address = "127.0.0.1:9000"
	}

	if cfg.Database == "" {
		cfg.Database = "default"
	}

	if cfg.User == "" {
		cfg.User = "default"
	}

	if cfg.Compression == "" {
		cfg.Compression = "none"
	}

	if _, ok := compressionMethods[cfg.Compression]; !ok {
		entries = append(entries, errorstack.Entry{
			Message: `Must be one of "none", "lz4", "lz4hc" or "zstd"`,
			Path:    []any{"config", "compression"},
		})
	}

	// A negative value means nothing to the driver, which would read a negative
	// timeout as one that has already expired.
	durations := []struct {
		name  string
		value time.Duration
	}{
		{"dial_timeout", cfg.DialTimeout},
		{"read_timeout", cfg.ReadTimeout},
		{"conn_max_lifetime", cfg.ConnMaxLifetime},
	}

	for _, d := range durations {
		if d.value < 0 {
			entries = append(entries, errorstack.Entry{
				Message: "Must not be negative",
				Path:    []any{"config", d.name},
			})
		}
	}

	if cfg.MaxOpenConns < 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must not be negative",
			Path:    []any{"config", "max_open_conns"},
		})
	}

	if cfg.MaxIdleConns < 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must not be negative",
			Path:    []any{"config", "max_idle_conns"},
		})
	}

	entries = append(entries, cfg.TLS.Sanitize()...)
	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	return nil
}

// options builds the driver's options from a sanitized Config. Returns an error if
// the TLS configuration is not valid; nothing has been dialed at that point.
//
// TLS.ServerName is left empty when the Config does not set it: the driver dials
// with crypto/tls, which then verifies the certificate against the host of the
// address it dials.
func (cfg *Config) options() (*clickhouse.Options, error) {
	opts := &clickhouse.Options{
		Addr: []string{cfg.Address},
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.User,
			Password: cfg.Password,
		},
		DialTimeout:     cfg.DialTimeout,
		ReadTimeout:     cfg.ReadTimeout,
		MaxOpenConns:    cfg.MaxOpenConns,
		MaxIdleConns:    cfg.MaxIdleConns,
		ConnMaxLifetime: cfg.ConnMaxLifetime,
		Compression: &clickhouse.Compression{
			Method: compressionMethods[cfg.Compression],
		},

		// Cloned, because the driver reads the map on every statement for the life
		// of the pool, and the caller still holds it.
		Settings: clickhouse.Settings(maps.Clone(cfg.Settings)),
	}

	if cfg.TLS.Enabled {
		var entries []errorstack.Entry
		opts.TLS, entries = cfg.TLS.ToStandardTLS()
		if len(entries) > 0 {
			return nil, errorstack.NewValidation(entries...)
		}
	}

	return opts, nil
}

// name returns the name the connection registers under, falling back to the
// integration's identifier when the Config leaves it empty.
func (cfg *Config) name() string {
	if cfg.Name != "" {
		return cfg.Name
	}

	return identifier
}
