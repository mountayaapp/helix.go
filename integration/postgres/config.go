package postgres

import (
	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	"github.com/jackc/pgx/v5/pgconn"
)

/*
Config is used to configure the PostgreSQL integration.
*/
type Config struct {

	// Address is the PostgreSQL address to connect to.
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

	// TLSConfig configures TLS to communicate with the PostgreSQL database.
	TLS integration.ConfigTLS `json:"tls"`

	// OnNotification is a callback function called when a notification from the
	// LISTEN/NOTIFY system is received.
	OnNotification func(notif *pgconn.Notification) `json:"-"`
}

/*
sanitize sets default values - when applicable - and validates the configuration.
Returns an error if configuration is not valid.
*/
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

	entries = append(entries, cfg.TLS.Sanitize()...)
	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	return nil
}
