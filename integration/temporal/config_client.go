package temporal

import (
	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	"go.temporal.io/sdk/converter"
)

/*
ConfigClient is used to configure a client-only Temporal connection registered
as a dependency.
*/
type ConfigClient struct {

	// Address is the Temporal server address to connect to.
	//
	// Default:
	//
	//   "127.0.0.1:7233"
	Address string `json:"address"`

	// Namespace sets the namespace to connect to.
	//
	// Default:
	//
	//   "default"
	Namespace string `json:"namespace"`

	// DataConverter customizes serialization/deserialization of arguments in
	// Temporal.
	DataConverter converter.DataConverter `json:"-"`

	// TLS configures TLS to communicate with the Temporal server.
	TLS integration.ConfigTLS `json:"tls"`
}

/*
sanitize sets default values - when applicable - and validates the configuration.
Returns an error if configuration is not valid.
*/
func (cfg *ConfigClient) sanitize() error {
	stack := errorstack.New("Failed to validate configuration", errorstack.WithIntegration(identifier))

	if cfg.Address == "" {
		cfg.Address = "127.0.0.1:7233"
	}

	if cfg.Namespace == "" {
		cfg.Namespace = "default"
	}

	stack.WithValidations(cfg.TLS.Sanitize()...)
	if stack.HasValidations() {
		return stack
	}

	return nil
}
