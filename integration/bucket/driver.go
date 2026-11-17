package bucket

import (
	"context"
	"net/url"
	"regexp"

	"github.com/mountayaapp/helix.go/errorstack"

	"gocloud.dev/blob"
)

// Driver names the storage provider a Bucket is backed by: one of DriverAWS,
// DriverAzure, DriverGoogleCloud or DriverLocal.
//
// It is a string so that the drivers are constants nothing can reassign, and so
// that a Config decodes from JSON, where a driver is written by its name: "aws",
// "azure", "google" or "local".
type Driver string

// driver is the behaviour behind a Driver.
type driver interface {

	// validate returns an entry for every Config field the driver cannot accept.
	// It runs after the fields every driver shares have been checked, so it has
	// nothing to say about an empty Bucket.
	validate(cfg *Config) []errorstack.Entry

	// open opens the bucket a valid cfg names, outside any Subfolder.
	open(ctx context.Context, cfg *Config) (*blob.Bucket, error)
}

// urlDriver is a driver whose bucket is opened from a Go Cloud URL, through the
// opener its Go Cloud package registers (openURL).
type urlDriver interface {

	// url returns the Go Cloud URL the bucket is opened from. It is a url.URL
	// built from Config fields rather than a formatted string, so that no field
	// can carry a query parameter of its own into it: a Bucket of
	// "x?endpoint=https://elsewhere" names a bucket, it does not redirect one.
	url(cfg *Config) (*url.URL, error)
}

// openURL opens the bucket d's URL for cfg names, through the drivers
// registered with Go Cloud.
func openURL(ctx context.Context, d urlDriver, cfg *Config) (*blob.Bucket, error) {
	u, err := d.url(cfg)
	if err != nil {
		return nil, err
	}

	return blob.DefaultURLMux().OpenBucketURL(ctx, u)
}

// impl returns the behaviour behind d, or nil when d is not a supported Driver.
func (d Driver) impl() driver {
	switch d {
	case DriverAWS:
		return driverAWS{}
	case DriverAzure:
		return driverAzure{}
	case DriverGoogleCloud:
		return driverGoogleCloud{}
	case DriverLocal:
		return driverLocal{}
	default:
		return nil
	}
}

// validateName returns an entry when name is set and does not match the
// provider's naming rule. An empty name is left to the check every driver shares.
func validateName(name string, rule *regexp.Regexp, message string) []errorstack.Entry {
	if name == "" || rule.MatchString(name) {
		return nil
	}

	return []errorstack.Entry{{
		Message: message,
		Path:    []any{"config", "bucket"},
	}}
}

// required returns an entry for a Config field the driver cannot work without,
// when it is empty. The entry never quotes a value, a credential being one.
func required(field, value string) []errorstack.Entry {
	if value != "" {
		return nil
	}

	return []errorstack.Entry{{
		Message: "Must be set",
		Path:    []any{"config", field},
	}}
}

// parseEndpoint returns endpoint as a URL when it is an absolute http or https
// URL, and false otherwise. Credentials, a query and a fragment are refused
// rather than passed on: none of them means anything to a driver, and a query is
// exactly the shape that smuggles parameters into the driver's URL.
func parseEndpoint(endpoint string) (*url.URL, bool) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, false
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, false
	}

	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, false
	}

	return u, true
}

// unsupported returns an entry for a Config field that is set although the
// driver has no use for it. It is refused rather than ignored: an Endpoint
// silently dropped is a service talking to production while its author believes
// it talks to an emulator.
func unsupported(d Driver, field, value string) []errorstack.Entry {
	if value == "" {
		return nil
	}

	return []errorstack.Entry{{
		Message: "Must be empty for the " + string(d) + " driver",
		Path:    []any{"config", field},
	}}
}
