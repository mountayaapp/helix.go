package bucket

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/url"
	"regexp"

	"github.com/mountayaapp/helix.go/errorstack"

	"cloud.google.com/go/storage"
	"go.opentelemetry.io/otel/attribute"
	"gocloud.dev/blob"
	"gocloud.dev/blob/gcsblob"
	"gocloud.dev/gcp"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

// DriverGoogleCloud uses Google Cloud Storage.
//
// The requests are made as the service account whose key is CredentialsJSON: the
// key is exchanged for an access token at the key's token_uri, renewed as it
// expires. They go to Endpoint, or to Cloud Storage itself when it is empty. The
// client is built from these fields alone. Nothing is read from the environment:
// no GOOGLE_APPLICATION_CREDENTIALS, gcloud credentials, metadata server or
// STORAGE_EMULATOR_HOST, so neither the credentials nor the service the requests
// go to can change behind the caller's back.
//
// Config example:
//
//	bucket.Config{
//	  Bucket: "my-bucket",
//	  Driver: bucket.DriverGoogleCloud{
//	    CredentialsJSON: os.Getenv("MY_SERVICE_ACCOUNT_KEY"),
//	  },
//	}
type DriverGoogleCloud struct {

	// CredentialsJSON is the key of the service account the requests are made as:
	// the JSON key Google Cloud issues for it, whose "type" is "service_account".
	// It is never encoded to JSON, nor quoted in a validation error.
	//
	// Required.
	CredentialsJSON string `json:"-"`

	// Endpoint is the scheme and domain of the Cloud Storage service, when it is
	// not the public one: an emulator such as fake-gcs-server, or a Private Service
	// Connect endpoint. The requests sent to it are still authorized with
	// CredentialsJSON, whose token comes from the key's token_uri: an emulator does
	// not stand in for that exchange.
	//
	// Examples:
	//
	//   "http://localhost:4443"
	//   "https://storage-myendpoint.p.googleapis.com"
	Endpoint string `json:"endpoint,omitempty"`
}

// googleBucketName is the Cloud Storage naming rule. The rules it leaves to the
// service — no "goog" prefix, 63 characters per dot-separated component — cannot
// be used to reshape a request, so a name breaking them is refused at Connect.
var googleBucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,220}[a-z0-9]$`)

// googleDefaultEndpoint is where the requests go when Endpoint is empty: the JSON
// API of Cloud Storage.
const googleDefaultEndpoint = "https://storage.googleapis.com/storage/v1/"

// name returns "google".
func (DriverGoogleCloud) name() string {
	return "google"
}

// validate ensures the bucket name is one Cloud Storage accepts, that
// CredentialsJSON is set and is a service account key, and that Endpoint, when
// set, is the domain of a service.
func (d DriverGoogleCloud) validate(bucket string) []errorstack.Entry {
	entries := validateName(bucket, googleBucketName,
		"Must be 3 to 222 lowercase letters, digits, '.', '-' or '_', starting and ending with a letter or digit")

	entries = append(entries, required("credentials_json", d.CredentialsJSON)...)
	if d.CredentialsJSON != "" && !isServiceAccountKey(d.CredentialsJSON) {
		entries = append(entries, errorstack.Entry{
			Message: "Must be a service account key in JSON",
			Path:    driverPath("credentials_json"),
		})
	}

	return append(entries, validateServiceDomain(d.Endpoint)...)
}

// isServiceAccountKey reports whether key is the JSON key of a service account:
// of type "service_account", naming the account's email, and holding its private
// key as a PEM-encoded RSA key. The token exchange needs all three, and checking
// them here makes a malformed key a validation error rather than a failure at the
// first request.
func isServiceAccountKey(key string) bool {
	var fields struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
	}

	if err := json.Unmarshal([]byte(key), &fields); err != nil {
		return false
	}

	if fields.Type != string(google.ServiceAccount) || fields.ClientEmail == "" {
		return false
	}

	block, _ := pem.Decode([]byte(fields.PrivateKey))
	if block == nil {
		return false
	}

	if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		_, ok := parsed.(*rsa.PrivateKey)
		return ok
	}

	_, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	return err == nil
}

// attributes returns the endpoint, when set.
func (d DriverGoogleCloud) attributes() []attribute.KeyValue {
	return endpointAttributes(d.Endpoint)
}

// open opens the bucket through a Cloud Storage client built from d alone
// (newStorageClient), and hands the client back for the connection to close:
// a bucket leaves open the client it is given.
func (d DriverGoogleCloud) open(ctx context.Context, bucket string) (*blob.Bucket, io.Closer, error) {
	client, err := newStorageClient(ctx, d)
	if err != nil {
		return nil, nil, err
	}

	// With Options.Client set, gcsblob uses the client as it is, and never reads
	// STORAGE_EMULATOR_HOST, which would otherwise replace it with an
	// unauthenticated one.
	b, err := gcsblob.OpenBucket(ctx, nil, bucket, &gcsblob.Options{Client: client})
	if err != nil {
		_ = client.Close()
		return nil, nil, err
	}

	return b, client, nil
}

// newStorageClient returns the Cloud Storage client d describes, built from d and
// from nothing else. Each option shuts out a source the library would otherwise
// consult:
//
//   - The HTTP client is ours, authorized with the key, so STORAGE_EMULATOR_HOST,
//     which makes the library drop authentication, has no transport of its own to
//     strip.
//   - The endpoint is always given, as a URL the library uses verbatim, so neither
//     STORAGE_EMULATOR_HOST, a universe domain nor the mTLS settings can redirect
//     the requests.
//   - The token source stands for the credentials, so the library does not look
//     up Application Default Credentials. No test observes this one: nothing would
//     ever ask those credentials for a token.
//   - Client metrics are disabled, which GCP_STORAGE_GO_ENABLE_OTEL_METRICS cannot
//     override. They would otherwise start an exporter that looks up Application
//     Default Credentials and the metadata server.
//
// The token source outlives Connect, renewing the token as it expires, so it is
// built from a context Connect's cancellation does not reach.
func newStorageClient(ctx context.Context, d DriverGoogleCloud) (*storage.Client, error) {
	ctx = context.WithoutCancel(ctx)

	creds, err := google.CredentialsFromJSONWithType(ctx, []byte(d.CredentialsJSON), google.ServiceAccount, storage.ScopeReadWrite)
	if err != nil {
		return nil, err
	}

	hc, err := gcp.NewHTTPClient(gcp.DefaultTransport(), gcp.CredentialsTokenSource(creds))
	if err != nil {
		return nil, err
	}

	endpoint, err := googleEndpoint(d)
	if err != nil {
		return nil, err
	}

	return storage.NewClient(ctx,
		option.WithHTTPClient(&hc.Client),
		option.WithEndpoint(endpoint),
		option.WithTokenSource(creds.TokenSource),
		storage.WithDisabledClientMetrics(),
	)
}

// googleEndpoint returns the base URL of the JSON API the requests go to: under
// Endpoint when it is set, and Cloud Storage's own otherwise. Only the scheme and
// the host of Endpoint are kept.
func googleEndpoint(d DriverGoogleCloud) (string, error) {
	if d.Endpoint == "" {
		return googleDefaultEndpoint, nil
	}

	u, err := url.Parse(d.Endpoint)
	if err != nil {
		return "", err
	}

	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/storage/v1/"}).String(), nil
}
