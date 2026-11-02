package bucket

import (
	"net/url"
	"regexp"

	"github.com/mountayaapp/helix.go/errorstack"

	_ "gocloud.dev/blob/gcsblob"
)

// DriverGoogleCloud uses Google Cloud Storage.
//
// Credentials come from Application Default Credentials: the file named by
// GOOGLE_APPLICATION_CREDENTIALS, the gcloud CLI's user credentials, or the
// metadata server — which is how Workload Identity on GKE and the attached
// service account on Compute Engine and Cloud Run are picked up. Nothing is
// required in the environment beyond what that chain needs.
//
// Neither Config.Region nor Config.Endpoint applies: a bucket name is global,
// and the Cloud Storage client reaches an emulator through STORAGE_EMULATOR_HOST.
//
// Config example:
//
//	bucket.Config{
//	  Driver: bucket.DriverGoogleCloud,
//	  Bucket: "my-bucket",
//	}
const DriverGoogleCloud Driver = "google"

// googleBucketName is the Cloud Storage naming rule. The rules it leaves to the
// service — no "goog" prefix, 63 characters per dot-separated component — cannot
// be used to reshape the URL, so a name breaking them is refused at Connect.
var googleBucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,220}[a-z0-9]$`)

// driverGoogleCloud is the behaviour behind DriverGoogleCloud.
type driverGoogleCloud struct{}

// validate ensures the bucket name is one Cloud Storage accepts, and that no
// field the driver has no use for is set.
func (driverGoogleCloud) validate(cfg *Config) []errorstack.Entry {
	entries := validateName(cfg.Bucket, googleBucketName,
		"Must be 3 to 222 lowercase letters, digits, '.', '-' or '_', starting and ending with a letter or digit")

	entries = append(entries, unsupported(DriverGoogleCloud, "region", cfg.Region)...)
	entries = append(entries, unsupported(DriverGoogleCloud, "endpoint", cfg.Endpoint)...)

	return entries
}

// url returns the gcsblob URL of the bucket.
func (driverGoogleCloud) url(cfg *Config) (*url.URL, error) {
	return &url.URL{
		Scheme: "gs",
		Host:   cfg.Bucket,
	}, nil
}
