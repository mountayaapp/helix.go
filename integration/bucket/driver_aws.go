package bucket

import (
	"net/url"
	"regexp"

	"github.com/mountayaapp/helix.go/errorstack"

	_ "gocloud.dev/blob/s3blob"
)

// DriverAWS uses Amazon S3, or any S3-compatible service reached through
// Config.Endpoint: MinIO, Cloudflare R2, LocalStack, Ceph.
//
// Credentials come from the AWS SDK's default chain, in its order: the
// AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY environment variables, the shared
// configuration and credentials files (AWS_PROFILE), web identity federation
// (IAM Roles for Service Accounts on EKS), and the ECS or EC2 instance role.
// Nothing is required in the environment beyond what that chain needs.
//
// The region is Config.Region, and when it is empty, the SDK's own resolution:
// AWS_REGION, then AWS_DEFAULT_REGION, then the shared configuration profile.
//
// Config example:
//
//	bucket.Config{
//	  Driver: bucket.DriverAWS,
//	  Bucket: "my-bucket",
//	  Region: "eu-west-1",
//	}
const DriverAWS Driver = "aws"

// awsBucketName is the S3 naming rule, widened to the legacy one that buckets
// created before March 2018 may still follow — uppercase letters, underscores
// and up to 255 characters — so that an old bucket is not refused here while S3
// still serves it.
var awsBucketName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{1,253}[a-zA-Z0-9]$`)

// driverAWS is the behaviour behind DriverAWS.
type driverAWS struct{}

// validate ensures the bucket name is one S3 accepts and that Endpoint, when
// set, is a URL the SDK can send requests to.
func (driverAWS) validate(cfg *Config) []errorstack.Entry {
	entries := validateName(cfg.Bucket, awsBucketName,
		"Must be 3 to 255 letters, digits, '.', '-' or '_', starting and ending with a letter or digit")

	if cfg.Endpoint != "" {
		if _, ok := parseEndpoint(cfg.Endpoint); !ok {
			entries = append(entries, errorstack.Entry{
				Message: "Must be an http or https URL without credentials, query or fragment",
				Path:    []any{"config", "endpoint"},
			})
		}
	}

	return entries
}

// url returns the s3blob URL of the bucket.
//
// A custom endpoint is addressed path-style — the bucket in the URL path rather
// than in the host name — because that is the one form every S3-compatible
// service accepts, and the only one MinIO accepts without extra configuration.
func (driverAWS) url(cfg *Config) (*url.URL, error) {
	query := url.Values{}
	if cfg.Region != "" {
		query.Set("region", cfg.Region)
	}

	if cfg.Endpoint != "" {
		query.Set("endpoint", cfg.Endpoint)
		query.Set("use_path_style", "true")
	}

	return &url.URL{
		Scheme:   "s3",
		Host:     cfg.Bucket,
		RawQuery: query.Encode(),
	}, nil
}
