package postgres

import (
	"context"
	"time"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"

	"github.com/jackc/pgx/v5/pgconn"
)

// Config is used to configure the PostgreSQL integration.
//
// Every pool and timeout field left at its zero value keeps the driver's default,
// which is documented on the field.
type Config struct {

	// Name identifies this connection in health output and error context. Two
	// pools of the same kind — a primary and a replica — are told apart by it.
	// Defaults to the integration's identifier when left empty.
	Name string `json:"name"`

	// Address is the PostgreSQL address to connect to, as "host:port".
	//
	// Default:
	//
	//   "127.0.0.1:5432"
	Address string `json:"address"`

	// Database is the database to connect to.
	//
	// Required.
	Database string `json:"-"`

	// User is the user to use to connect to the database.
	//
	// Required.
	User string `json:"-"`

	// Password is the user's password to connect to the database.
	//
	// Required.
	Password string `json:"-"`

	// ApplicationName is reported to the server as the connection's
	// application_name, which is what pg_stat_activity and the server's logs show
	// for it. Left empty, the server's default applies.
	ApplicationName string `json:"application_name"`

	// TimeZone is the time zone of every connection, as an IANA name such as "UTC"
	// or "Europe/Paris".
	//
	// The server takes it as the session's TimeZone, which decides the date or the
	// timestamp a timestamptz is cast to, and the instant a timestamp without time
	// zone is taken to mean beside a timestamptz. The driver scans every
	// timestamptz, alone or in an array, a range or a multirange, into a time.Time
	// in that location, so a value read back carries the zone the connection was
	// set to rather than the process's local one. The instant never changes, only
	// the location it is expressed in.
	//
	// A name other than "UTC" needs the time zone database on the host, or the
	// program importing time/tzdata.
	//
	// Default: the server's TimeZone, and timestamptz values scanned in the
	// process's local time zone.
	TimeZone string `json:"time_zone"`

	// MaxConns is the maximum number of connections the pool holds.
	//
	// Default:
	//
	//   max(4, runtime.NumCPU())
	MaxConns int32 `json:"max_conns"`

	// MinConns is the number of connections the pool keeps open even when idle.
	// It must not be greater than MaxConns when MaxConns is set.
	//
	// Default:
	//
	//   0
	MinConns int32 `json:"min_conns"`

	// MaxConnLifetime is how long a connection may live before the pool closes
	// it and opens a new one.
	//
	// Default:
	//
	//   time.Hour
	MaxConnLifetime time.Duration `json:"max_conn_lifetime"`

	// MaxConnIdleTime is how long a connection may sit idle before the pool's
	// health check closes it.
	//
	// Default:
	//
	//   30 * time.Minute
	MaxConnIdleTime time.Duration `json:"max_conn_idle_time"`

	// HealthCheckPeriod is how often the pool checks its idle connections for
	// MaxConnLifetime, MaxConnIdleTime and MinConns.
	//
	// Default:
	//
	//   time.Minute
	HealthCheckPeriod time.Duration `json:"health_check_period"`

	// ConnectTimeout bounds each attempt to open a connection, TLS handshake and
	// authentication included, in addition to whatever deadline the caller's
	// context carries.
	//
	// Default:
	//
	//   2 * time.Minute for a pooled connection; the caller's context alone for
	//   the dedicated connection of a Listener
	ConnectTimeout time.Duration `json:"connect_timeout"`

	// TLS configures TLS to communicate with the PostgreSQL database.
	//
	// When enabled, every connection uses TLS or fails: there is no fallback to
	// plaintext. ServerName defaults to the host of Address, so the server's
	// certificate is verified against the name that was dialed.
	//
	// When disabled, connections are plaintext. The driver's own default —
	// attempt TLS without verifying the certificate, then fall back to plaintext —
	// is not used: a connection is either verified TLS or knowingly plaintext.
	TLS integration.ConfigTLS `json:"tls"`

	// OnNotification is called when a pooled connection reads a notification from
	// the LISTEN/NOTIFY system.
	//
	// A pooled connection only reads from the server while it runs a statement,
	// so a notification for a LISTEN issued through Exec is delivered late, on
	// whichever request next uses that connection, or never.
	//
	// Deprecated: Use PostgreSQL.Listen, which holds a dedicated connection and
	// delivers every notification as it arrives. OnNotification is never called
	// for a Listener's notifications.
	OnNotification func(notif *pgconn.Notification) `json:"-"`

	// OnWrite, when set, is called each time the database has durably kept
	// something written through the pool, with the context of the statement that
	// got it kept. It is how a caller knows that a request has durably written
	// something: a value the request put in its context is visible to it.
	//
	// It is called once for every transaction that commits, a read-only one
	// included, and once for a statement, a batch or a copy run outside of a
	// transaction that inserted, updated, deleted, merged or copied at least one
	// row. It is never called for a statement that failed, a rollback, a read, a
	// write inside a transaction that has not committed yet, or the savepoint a
	// nested transaction's Commit releases.
	//
	// Outside of a transaction, the statement's command tag is what tells a write
	// from a read, so a write the tag does not report is not seen: one a function
	// called from a SELECT makes, one under a data-modifying WITH whose top-level
	// statement is a SELECT, or one made by a statement other than the last of a
	// multi-statement string. Such writes are seen when run in a transaction, since
	// its COMMIT is. A COMMIT whose answer never arrived is not reported either,
	// although the server may have applied it.
	//
	// It runs synchronously, on the goroutine that ran the statement, before the
	// call that finished it returns: Exec, Commit, the Close of the Rows of a Query
	// or of the BatchResults of a SendBatch, the Scan of the Row of a QueryRow. It
	// must therefore be quick, and must not panic.
	OnWrite func(ctx context.Context) `json:"-"`
}

// sanitize sets default values - when applicable - and validates the configuration.
// Returns an error if configuration is not valid.
func (cfg *Config) sanitize() error {
	var entries []errorstack.Entry

	if cfg.Address == "" {
		cfg.Address = "127.0.0.1:5432"
	}

	if cfg.Database == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "database"},
		})
	}

	if cfg.User == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "user"},
		})
	}

	if cfg.Password == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "password"},
		})
	}

	// "Local" is the process's own zone to Go and no zone at all to the server.
	if cfg.TimeZone != "" {
		if _, err := time.LoadLocation(cfg.TimeZone); err != nil || cfg.TimeZone == "Local" {
			entries = append(entries, errorstack.Entry{
				Message: `Must be an IANA time zone name, such as "UTC"`,
				Path:    []any{"config", "time_zone"},
			})
		}
	}

	entries = append(entries, cfg.sanitizePool()...)
	entries = append(entries, cfg.TLS.Sanitize()...)
	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	return nil
}

// sanitizePool validates the pool and timeout fields. Zero is never an error: it
// is how a field asks for the driver's default.
func (cfg *Config) sanitizePool() []errorstack.Entry {
	var entries []errorstack.Entry

	negative := func(field string) {
		entries = append(entries, errorstack.Entry{
			Message: "Must not be negative",
			Path:    []any{"config", field},
		})
	}

	if cfg.MaxConns < 0 {
		negative("max_conns")
	}

	if cfg.MinConns < 0 {
		negative("min_conns")
	}

	if cfg.MaxConns > 0 && cfg.MinConns > cfg.MaxConns {
		entries = append(entries, errorstack.Entry{
			Message: "Must not be greater than max_conns",
			Path:    []any{"config", "min_conns"},
		})
	}

	durations := []struct {
		field string
		value time.Duration
	}{
		{"max_conn_lifetime", cfg.MaxConnLifetime},
		{"max_conn_idle_time", cfg.MaxConnIdleTime},
		{"health_check_period", cfg.HealthCheckPeriod},
		{"connect_timeout", cfg.ConnectTimeout},
	}

	for _, d := range durations {
		if d.value < 0 {
			negative(d.field)
		}
	}

	return entries
}

// name returns the name the connection registers under, falling back to the
// integration's identifier when the Config leaves it empty.
func (cfg *Config) name() string {
	if cfg.Name != "" {
		return cfg.Name
	}

	return identifier
}
