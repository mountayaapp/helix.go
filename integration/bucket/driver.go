package bucket

import (
	"github.com/mountayaapp/helix.go/errorstack"
)

/*
Driver allows to set the driver to use for the bucket integration.
*/
type Driver interface {

	// string returns the string representation of the driver.
	string() string

	// validate ensures Config and environment variables are valid for the driver.
	validate(cfg *Config) []errorstack.Entry

	// url returns the Go Cloud bucket URL of the bucket driver.
	url(cfg *Config) string
}
