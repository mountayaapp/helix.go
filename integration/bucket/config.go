package bucket

import (
	"strings"

	"github.com/mountayaapp/helix.go/errorstack"
)

/*
Config is used to configure the Bucket integration.
*/
type Config struct {

	// Driver sets the driver to use.
	//
	// Required.
	//
	// Example:
	//
	//   bucket.DriverAWS
	Driver Driver `json:"driver"`

	// Bucket is the name of the bucket.
	//
	// Required.
	//
	// Example:
	//
	//   "my-bucket"
	Bucket string `json:"bucket"`

	// Subfolder sets an optional subfolder where all keys are stored in the bucket.
	//
	// Default:
	//
	//   "/"
	//
	// Example:
	//
	//   "my/subfolder/"
	//
	// Operations on "<key>" will be translated to "my/subfolder/<key>".
	Subfolder string `json:"subfolder,omitempty"`
}

/*
sanitize sets default values - when applicable - and validates the configuration.
Returns an error if configuration is not valid.
*/
func (cfg *Config) sanitize() error {
	var entries []errorstack.Entry

	if cfg.Driver == nil {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "driver"},
		})
	} else {
		entries = append(entries, cfg.Driver.validate(cfg)...)
	}

	if cfg.Bucket == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "bucket"},
		})
	}

	if cfg.Subfolder != "" && !strings.HasSuffix(cfg.Subfolder, "/") {
		entries = append(entries, errorstack.Entry{
			Message: "Must end with a trailing slash",
			Path:    []any{"config", "subfolder"},
		})
	}

	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	return nil
}
