package bucket

import (
	"fmt"
	"os"

	"github.com/mountayaapp/helix.go/errorstack"

	_ "gocloud.dev/blob/s3blob"
)

/*
DriverAWS allows to use AWS S3 as bucket driver. This driver relies on generic
environment variables required by the AWS SDK (v2):

  - AWS_ACCESS_KEY_ID
  - AWS_SECRET_ACCESS_KEY
  - AWS_REGION

Config example:

	bucket.Config{
	  Driver: bucket.DriverAWS,
	  Bucket: "my-bucket",
	}
*/
var DriverAWS Driver = &driverAWS{}

/*
driverAWS is the internal type to use AWS S3 as bucket driver.
*/
type driverAWS struct{}

/*
string returns the string representation of the AWS S3 bucket driver.
*/
func (d *driverAWS) string() string {
	return "aws"
}

/*
validate ensures Config and environment variables are valid for the AWS S3 bucket
driver.
*/
func (d *driverAWS) validate(cfg *Config) []errorstack.Entry {
	var entries []errorstack.Entry

	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"env", "aws_access_key_id"},
		})
	}

	if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"env", "aws_secret_access_key"},
		})
	}

	if os.Getenv("AWS_REGION") == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"env", "aws_region"},
		})
	}

	return entries
}

/*
url returns the Go Cloud bucket URL of the AWS S3 bucket driver.
*/
func (d *driverAWS) url(cfg *Config) string {
	path := fmt.Sprintf("s3://%s?region=%s&awssdk=v2", cfg.Bucket, os.Getenv("AWS_REGION"))

	return path
}
