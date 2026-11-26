package bucket

import (
	"strings"

	"github.com/mountaya/helix.go/errorstack"
)

// Config is used to configure the Bucket integration.
type Config struct {

	// Name identifies this connection in health output, error context and
	// telemetry, where every span carries it as "bucket.name". Two buckets
	// attached to the same Service — uploads and exports, say — are told apart by
	// it, so they must not share it. Defaults to the integration's identifier,
	// "bucket", when left empty.
	Name string `json:"name"`

	// Driver is the storage provider, with everything needed to reach it: one of
	// DriverS3, DriverAzure, DriverGoogleCloud or DriverLocal, each holding the
	// fields of its own provider and no other.
	//
	// It is set in code, along with the credentials it holds: a "driver" in JSON
	// fails to decode. Encoded, it carries its own fields, never a credential.
	//
	// Required.
	//
	// Example:
	//
	//   bucket.DriverLocal{}
	Driver Driver `json:"driver"`

	// Bucket is the name of the bucket: the container for DriverAzure, and the
	// directory for DriverLocal. It is checked against the provider's naming
	// rules.
	//
	// Required.
	//
	// Example:
	//
	//   "my-bucket"
	Bucket string `json:"bucket"`

	// Subfolder sets an optional folder inside the bucket where all keys are
	// stored. Operations on "<key>" are translated to "<subfolder><key>".
	//
	// It is normalized to the prefix it stands for: a leading "/" is dropped and a
	// single trailing "/" is added, so "my/subfolder", "/my/subfolder" and
	// "my/subfolder/" are the same folder. Empty, or "/", is the bucket root.
	//
	// Default:
	//
	//   ""
	//
	// Example:
	//
	//   "my/subfolder/"
	Subfolder string `json:"subfolder,omitempty"`
}

// sanitize sets default values - when applicable - and validates the configuration.
// Returns an error if configuration is not valid.
func (cfg *Config) sanitize() error {
	var entries []errorstack.Entry

	noDriver := isNil(cfg.Driver)
	if noDriver {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "driver"},
		})
	}

	if cfg.Bucket == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "bucket"},
		})
	}

	if !noDriver {
		entries = append(entries, cfg.Driver.validate(cfg.Bucket)...)
	}

	cfg.Subfolder = normalizeSubfolder(cfg.Subfolder)

	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	return nil
}

// normalizeSubfolder returns subfolder as the key prefix it stands for. A
// leading "/" would become part of every key — "/key" rather than "key" — and a
// missing trailing one would glue the folder to the key, so both are fixed here
// rather than refused. A subfolder of nothing but slashes is the bucket root,
// which is no prefix at all.
func normalizeSubfolder(subfolder string) string {
	subfolder = strings.Trim(subfolder, "/")
	if subfolder == "" {
		return ""
	}

	return subfolder + "/"
}

// name returns the name the connection registers under, falling back to the
// integration's identifier when the Config leaves it empty.
func (cfg *Config) name() string {
	if cfg.Name != "" {
		return cfg.Name
	}

	return identifier
}
