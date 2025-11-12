package clickhouse

import (
	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
)

/*
Config is used to configure the ClickHouse integration.
*/
type Config struct {

	// Address is the ClickHouse address to connect to.
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

	// Password is the user's password to connect to the database.
	//
	// Default:
	//
	//   "default"
	Password string `json:"-"`

	// TLSConfig configures TLS to communicate with the ClickHouse database.
	TLS integration.ConfigTLS `json:"tls"`
}

/*
sanitize sets default values - when applicable - and validates the configuration.
Returns an error if configuration is not valid.
*/
func (cfg *Config) sanitize() error {
	stack := errorstack.New("Failed to validate configuration", errorstack.WithIntegration(identifier))

	if cfg.Address == "" {
		cfg.Address = "127.0.0.1:9000"
	}

	if cfg.Database == "" {
		cfg.Database = "default"
	}

	if cfg.User == "" {
		cfg.User = "default"
	}

	if cfg.Password == "" {
		cfg.Password = "default"
	}

	stack.WithValidations(cfg.TLS.Sanitize()...)
	if stack.HasValidations() {
		return stack
	}

	return nil
}
