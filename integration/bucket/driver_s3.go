package bucket

import (
	"context"
	"io"
	"regexp"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/ratelimit"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.opentelemetry.io/otel/attribute"
	"gocloud.dev/blob"
	"gocloud.dev/blob/s3blob"
)

// DriverS3 uses Amazon S3, or any S3-compatible service reached through
// Endpoint: MinIO, Cloudflare R2, LocalStack, Ceph.
//
// The requests are signed with the static key pair in AccessKeyID and
// SecretAccessKey, for Region, and go to Endpoint when it is set. The client is
// built from these fields alone. Nothing is read from the environment, the shared
// configuration and credentials files, web identity federation or an instance
// role: an AWS_* variable set beside the service changes neither the credentials,
// the region nor the endpoint the requests go to.
//
// Config example:
//
//	bucket.Config{
//	  Bucket: "my-bucket",
//	  Driver: bucket.DriverS3{
//	    Region:          "eu-west-1",
//	    AccessKeyID:     os.Getenv("MY_ACCESS_KEY_ID"),
//	    SecretAccessKey: os.Getenv("MY_SECRET_ACCESS_KEY"),
//	  },
//	}
type DriverS3 struct {

	// Region is the region the bucket lives in, which every request is signed
	// for. An S3-compatible service names its own: "us-east-1" for MinIO, "auto"
	// for Cloudflare R2.
	//
	// Required.
	//
	// Example:
	//
	//   "eu-west-1"
	Region string `json:"region"`

	// Endpoint is the URL of the service the requests go to, when it is not Amazon
	// S3 itself: an S3-compatible service, or a VPC endpoint. A custom endpoint is
	// addressed path-style, with the bucket in the URL path.
	//
	// Examples:
	//
	//   "http://localhost:9000"
	//   "https://<account-id>.r2.cloudflarestorage.com"
	Endpoint string `json:"endpoint,omitempty"`

	// AccessKeyID is the access key of the static key pair the requests are signed
	// with. It is never encoded to JSON, nor quoted in a validation error.
	//
	// Required.
	AccessKeyID string `json:"-"`

	// SecretAccessKey is the secret key of the static key pair the requests are
	// signed with. It is never encoded to JSON, nor quoted in a validation error.
	//
	// Required.
	SecretAccessKey string `json:"-"`
}

// s3BucketName is the S3 naming rule, widened to the legacy one that buckets
// created before March 2018 may still follow — uppercase letters, underscores
// and up to 255 characters — so that an old bucket is not refused here while S3
// still serves it.
var s3BucketName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{1,253}[a-zA-Z0-9]$`)

// name returns "s3".
func (DriverS3) name() string {
	return "s3"
}

// validate ensures the bucket name is one S3 accepts, that the region and the
// key pair are set, and that Endpoint, when set, is a URL the SDK can send
// requests to.
func (d DriverS3) validate(bucket string) []errorstack.Entry {
	entries := validateName(bucket, s3BucketName,
		"Must be 3 to 255 letters, digits, '.', '-' or '_', starting and ending with a letter or digit")

	entries = append(entries, required("region", d.Region)...)
	entries = append(entries, validateEndpoint(d.Endpoint)...)
	entries = append(entries, required("access_key_id", d.AccessKeyID)...)
	entries = append(entries, required("secret_access_key", d.SecretAccessKey)...)

	return entries
}

// attributes returns the region and, when set, the endpoint.
func (d DriverS3) attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		integration.Key(identifier, "region").String(d.Region),
	}, endpointAttributes(d.Endpoint)...)
}

// open opens the bucket through an S3 client built from d alone (newS3Client).
// The client holds nothing the connection has to close.
func (d DriverS3) open(ctx context.Context, bucket string) (*blob.Bucket, io.Closer, error) {
	b, err := s3blob.OpenBucket(ctx, newS3Client(d), bucket, nil)
	if err != nil {
		return nil, nil, err
	}

	return b, nil, nil
}

// newS3Client returns the S3 client d describes, built from d and from nothing
// else. The aws.Config is assembled here rather than loaded, so that it carries
// no configuration source: the SDK resolves an endpoint, a region or a credential
// from those sources alone, and with none it finds nothing in the environment or
// the shared files to resolve them from.
//
// A custom endpoint is addressed path-style — the bucket in the URL path rather
// than in the host name — because that is the one form every S3-compatible
// service accepts, and the only one MinIO accepts without extra configuration.
//
// Retries are the SDK's standard ones without its client-side retry quota, as
// Go Cloud configures the clients it loads itself: a burst of failed requests
// would otherwise exhaust the quota and fail every request after it without
// retrying, long after the service has recovered.
func newS3Client(d DriverS3) *s3.Client {
	awsCfg := aws.Config{
		Region:      d.Region,
		Credentials: credentials.NewStaticCredentialsProvider(d.AccessKeyID, d.SecretAccessKey, ""),
		Retryer: func() aws.Retryer {
			return retry.NewStandard(func(o *retry.StandardOptions) {
				o.RateLimiter = ratelimit.None
			})
		},
	}

	if d.Endpoint != "" {
		awsCfg.BaseEndpoint = aws.String(d.Endpoint)
	}

	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = d.Endpoint != ""
	})
}
