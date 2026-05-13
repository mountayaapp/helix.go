package bucket

import (
	"fmt"
	"os"

	"github.com/mountayaapp/helix.go/errorstack"

	_ "gocloud.dev/blob/gcsblob"
)

/*
DriverGoogleCloud allows to use Google Cloud Storage as bucket driver. This
driver relies on generic environment variables required by the Google Cloud SDK:

  - GOOGLE_APPLICATION_CREDENTIALS

Config example:

	bucket.Config{
	  Driver: bucket.DriverGoogleCloud,
	  Bucket: "my-bucket",
	}
*/
var DriverGoogleCloud Driver = &driverGoogleCloud{}

/*
driverGoogleCloud is the internal type to use Google Cloud as bucket driver.
*/
type driverGoogleCloud struct{}

/*
string returns the string representation of the Google Cloud bucket driver.
*/
func (d *driverGoogleCloud) string() string {
	return "google"
}

/*
validate ensures Config and environment variables are valid for the Google Cloud
bucket driver.
*/
func (d *driverGoogleCloud) validate(cfg *Config) []errorstack.Entry {
	var entries []errorstack.Entry

	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"env", "google_application_credentials"},
		})
	}

	return entries
}

/*
url returns the Go Cloud bucket URL of the Google Cloud bucket driver.
*/
func (d *driverGoogleCloud) url(cfg *Config) string {
	path := fmt.Sprintf("gs://%s", cfg.Bucket)

	return path
}
