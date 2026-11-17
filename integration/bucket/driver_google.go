package bucket

import (
	"context"
	"net/url"
	"regexp"

	"github.com/mountayaapp/helix.go/errorstack"

	"gocloud.dev/blob"
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
// None of Config.Region, Config.Endpoint, Config.AccessKeyID or
// Config.SecretAccessKey applies: a bucket name is global, the Cloud Storage
// client reaches an emulator through STORAGE_EMULATOR_HOST, and the credentials
// are the ones above.
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
	entries = append(entries, unsupported(DriverGoogleCloud, "access_key_id", cfg.AccessKeyID)...)
	entries = append(entries, unsupported(DriverGoogleCloud, "secret_access_key", cfg.SecretAccessKey)...)

	return entries
}

// open opens the bucket from its URL.
func (d driverGoogleCloud) open(ctx context.Context, cfg *Config) (*blob.Bucket, error) {
	return openURL(ctx, d, cfg)
}

// url returns the gcsblob URL of the bucket.
func (driverGoogleCloud) url(cfg *Config) (*url.URL, error) {
	return &url.URL{
		Scheme: "gs",
		Host:   cfg.Bucket,
	}, nil
}
