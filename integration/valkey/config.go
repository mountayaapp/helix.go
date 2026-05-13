package valkey

import (
	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
)

/*
Config is used to configure the Valkey integration.
*/
type Config struct {

	// Address is the Valkey address to connect to.
	//
	// Default:
	//
	//   "127.0.0.1:6379"
	Address string `json:"address"`

	// User is the user to use to connect to the database.
	User string `json:"-"`

	// Password is the user's password to connect to the database.
	Password string `json:"-"`

	// TLSConfig configures TLS to communicate with the Valkey server.
	TLS integration.ConfigTLS `json:"tls"`
}

/*
sanitize sets default values - when applicable - and validates the configuration.
Returns an error if configuration is not valid.
*/
func (cfg *Config) sanitize() error {
	if cfg.Address == "" {
		cfg.Address = "127.0.0.1:6379"
	}

	entries := cfg.TLS.Sanitize()
	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	return nil
}
