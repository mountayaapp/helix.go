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
	stack := errorstack.New("Failed to validate configuration", errorstack.WithIntegration(identifier))

	if cfg.Address == "" {
		cfg.Address = "127.0.0.1:5432"
	}

	if cfg.Database == "" {
		stack.WithValidations(errorstack.Validation{
			Message: "Database is required and must not be empty",
			Path:    []string{"Config", "Database"},
		})
	}

	if cfg.User == "" {
		stack.WithValidations(errorstack.Validation{
			Message: "User is required and must not be empty",
			Path:    []string{"Config", "User"},
		})
	}

	if cfg.Password == "" {
		stack.WithValidations(errorstack.Validation{
			Message: "Password is required and must not be empty",
			Path:    []string{"Config", "Password"},
		})
	}

	stack.WithValidations(cfg.TLS.Sanitize()...)
	if stack.HasValidations() {
		return stack
	}

	return nil
}
