package bucket

import (
	"context"
	"io"
	"net/url"
	"reflect"
	"regexp"

	"github.com/mountaya/helix.go/errorstack"

	"go.opentelemetry.io/otel/attribute"
	"gocloud.dev/blob"
)

// Driver is the storage provider a Bucket is backed by, together with everything
// needed to reach it: one of DriverS3, DriverAzure, DriverGoogleCloud or
// DriverLocal. Each holds the fields of its own provider and no other, so a Config
// can neither name two providers nor configure one with another's fields.
//
// Its methods are unexported: a type outside this package implements it only by
// embedding one of the four. A pointer to one of them implements it as well.
type Driver interface {

	// name returns the driver's name, which every span carries as
	// "bucket.driver": "s3", "azure", "google" or "local".
	name() string

	// validate returns an entry for every field the provider cannot accept, and
	// for a bucket name that breaks the provider's naming rules. It runs after the
	// fields every driver shares have been checked, so it has nothing to say about
	// an empty bucket. An entry never quotes a value, a credential being one.
	validate(bucket string) []errorstack.Entry

	// attributes returns the attributes the driver adds to every span. A
	// credential is never one of them.
	attributes() []attribute.KeyValue

	// open opens bucket, outside any Subfolder, through a driver whose validate
	// returned no entry. Next to the bucket, it returns what the connection must
	// close once the bucket is closed, or nil when there is nothing.
	//
	// ctx bounds the opening only: nothing open builds may keep it.
	open(ctx context.Context, bucket string) (*blob.Bucket, io.Closer, error)
}

// isNil reports whether d holds no driver: nil itself, or a nil pointer to one,
// whose first method call would panic.
func isNil(d Driver) bool {
	if d == nil {
		return true
	}

	v := reflect.ValueOf(d)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

// driverPath returns the path of a driver field in a validation entry.
func driverPath(field string) []any {
	return []any{"config", "driver", field}
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

// required returns an entry for a driver field the provider cannot work without,
// when it is empty. The entry never quotes a value, a credential being one.
func required(field, value string) []errorstack.Entry {
	if value != "" {
		return nil
	}

	return []errorstack.Entry{{
		Message: "Must be set",
		Path:    driverPath(field),
	}}
}

// parseEndpoint returns endpoint as a URL when it is an absolute http or https
// URL, and false otherwise. Credentials, a query and a fragment are refused rather
// than passed on: none of them means anything in the base URL of a storage
// service, and a query would ride along on every request sent to it.
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

// validateEndpoint returns an entry when endpoint is set and is not a URL a
// request can be sent to: an absolute http or https URL, with no credentials,
// query or fragment. A path is allowed.
func validateEndpoint(endpoint string) []errorstack.Entry {
	if endpoint == "" {
		return nil
	}

	if _, ok := parseEndpoint(endpoint); ok {
		return nil
	}

	return []errorstack.Entry{{
		Message: "Must be an http or https URL without credentials, query or fragment",
		Path:    driverPath("endpoint"),
	}}
}

// validateServiceDomain returns an entry when endpoint is set and is not the
// scheme and domain of a service alone: an absolute http or https URL, with no
// path, credentials, query or fragment.
func validateServiceDomain(endpoint string) []errorstack.Entry {
	if endpoint == "" {
		return nil
	}

	if u, ok := parseEndpoint(endpoint); ok && (u.Path == "" || u.Path == "/") {
		return nil
	}

	return []errorstack.Entry{{
		Message: "Must be an http or https URL of the service domain, without path, credentials, query or fragment",
		Path:    driverPath("endpoint"),
	}}
}
