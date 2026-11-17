package bucket

import (
	"context"
	"regexp"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/ratelimit"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"gocloud.dev/blob"
	"gocloud.dev/blob/s3blob"
)

// DriverAWS uses Amazon S3, or any S3-compatible service reached through
// Config.Endpoint: MinIO, Cloudflare R2, LocalStack, Ceph.
//
// The client is built from the Config alone: the static key pair in
// Config.AccessKeyID and Config.SecretAccessKey, the region in Config.Region,
// and the service in Config.Endpoint when it is set. Nothing is read from the
// environment, the shared configuration and credentials files, web identity
// federation or an instance role: an AWS_* variable set beside the service
// changes neither the credentials, the region nor the endpoint the requests go
// to. The key pair and the region are required.
//
// Config example:
//
//	bucket.Config{
//	  Driver:          bucket.DriverAWS,
//	  Bucket:          "my-bucket",
//	  Region:          "eu-west-1",
//	  AccessKeyID:     os.Getenv("MY_ACCESS_KEY_ID"),
//	  SecretAccessKey: os.Getenv("MY_SECRET_ACCESS_KEY"),
//	}
const DriverAWS Driver = "aws"

// awsBucketName is the S3 naming rule, widened to the legacy one that buckets
// created before March 2018 may still follow — uppercase letters, underscores
// and up to 255 characters — so that an old bucket is not refused here while S3
// still serves it.
var awsBucketName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{1,253}[a-zA-Z0-9]$`)

// driverAWS is the behaviour behind DriverAWS.
type driverAWS struct{}

// validate ensures the bucket name is one S3 accepts, that the region and the
// key pair are set, and that Endpoint, when set, is a URL the SDK can send
// requests to.
func (driverAWS) validate(cfg *Config) []errorstack.Entry {
	entries := validateName(cfg.Bucket, awsBucketName,
		"Must be 3 to 255 letters, digits, '.', '-' or '_', starting and ending with a letter or digit")

	entries = append(entries, required("region", cfg.Region)...)

	if cfg.Endpoint != "" {
		if _, ok := parseEndpoint(cfg.Endpoint); !ok {
			entries = append(entries, errorstack.Entry{
				Message: "Must be an http or https URL without credentials, query or fragment",
				Path:    []any{"config", "endpoint"},
			})
		}
	}

	entries = append(entries, required("access_key_id", cfg.AccessKeyID)...)
	entries = append(entries, required("secret_access_key", cfg.SecretAccessKey)...)

	return entries
}

// open opens the bucket through an S3 client built from cfg alone (newS3Client).
func (driverAWS) open(ctx context.Context, cfg *Config) (*blob.Bucket, error) {
	return s3blob.OpenBucket(ctx, newS3Client(cfg), cfg.Bucket, nil)
}

// newS3Client returns the S3 client cfg describes, built from cfg and from
// nothing else. The aws.Config is assembled here rather than loaded, so that it
// carries no configuration source: the SDK resolves an endpoint, a region or a
// credential from those sources alone, and with none it finds nothing in the
// environment or the shared files to resolve them from.
//
// A custom endpoint is addressed path-style — the bucket in the URL path rather
// than in the host name — because that is the one form every S3-compatible
// service accepts, and the only one MinIO accepts without extra configuration.
//
// Retries are the SDK's standard ones without its client-side retry quota, as
// Go Cloud configures the clients it loads itself: a burst of failed requests
// would otherwise exhaust the quota and fail every request after it without
// retrying, long after the service has recovered.
func newS3Client(cfg *Config) *s3.Client {
	awsCfg := aws.Config{
		Region:      cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Retryer: func() aws.Retryer {
			return retry.NewStandard(func(o *retry.StandardOptions) {
				o.RateLimiter = ratelimit.None
			})
		},
	}

	if cfg.Endpoint != "" {
		awsCfg.BaseEndpoint = aws.String(cfg.Endpoint)
	}

	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.Endpoint != ""
	})
}
