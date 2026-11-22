package bucket

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// injections are bucket names that would each reshape a request if they reached
// a URL unchecked — adding a query parameter of their own, redirecting the
// request, dropping its credentials — or move its path. Every cloud driver's
// naming rule refuses them.
var injections = []string{
	"x?endpoint=https://elsewhere.example",
	"x?anonymous=true",
	"x?hostname_immutable=true&endpoint=https://elsewhere.example",
	"x/../y",
	"x#fragment",
	"user@x",
	"x:443",
	"x y",
}

// missing returns the entry of an empty driver field the provider requires.
func missing(field string) errorstack.Entry {
	return errorstack.Entry{Message: "Must be set", Path: driverPath(field)}
}

func TestDriver_Name(t *testing.T) {
	assert.Equal(t, "s3", DriverS3{}.name())
	assert.Equal(t, "azure", DriverAzure{}.name())
	assert.Equal(t, "google", DriverGoogleCloud{}.name())
	assert.Equal(t, "local", DriverLocal{}.name())
}

// TestIsNil pins that a nil pointer to a driver counts as no driver, so that
// Config refuses it rather than panicking on its first method call.
func TestIsNil(t *testing.T) {
	assert.True(t, isNil(nil))
	assert.True(t, isNil((*DriverS3)(nil)))
	assert.True(t, isNil((*DriverLocal)(nil)))
	assert.False(t, isNil(DriverLocal{}))
	assert.False(t, isNil(&DriverLocal{}))
}

func TestDriverS3_Validate(t *testing.T) {
	invalidName := errorstack.Entry{
		Message: "Must be 3 to 255 letters, digits, '.', '-' or '_', starting and ending with a letter or digit",
		Path:    []any{"config", "bucket"},
	}

	invalidEndpoint := errorstack.Entry{
		Message: "Must be an http or https URL without credentials, query or fragment",
		Path:    driverPath("endpoint"),
	}

	testcases := []struct {
		name    string
		driver  DriverS3
		bucket  string
		entries []errorstack.Entry
	}{
		{
			name:   "bucket name is valid",
			driver: s3Driver(""),
			bucket: "my-bucket.2024",
		},
		{
			name:   "legacy bucket name is valid",
			driver: s3Driver(""),
			bucket: "My_Legacy_Bucket",
		},
		{
			name:   "empty bucket is left to the shared check",
			driver: s3Driver(""),
		},
		{
			name:    "too short bucket is invalid",
			driver:  s3Driver(""),
			bucket:  "ab",
			entries: []errorstack.Entry{invalidName},
		},
		{
			name:    "bucket ending with a hyphen is invalid",
			driver:  s3Driver(""),
			bucket:  "my-bucket-",
			entries: []errorstack.Entry{invalidName},
		},
		{
			name:   "endpoint is valid",
			driver: s3Driver("http://localhost:9000"),
			bucket: "my-bucket",
		},
		{
			name:   "endpoint with a path is valid",
			driver: s3Driver("https://storage.example.com/s3"),
			bucket: "my-bucket",
		},
		{
			name:    "missing region is invalid",
			driver:  DriverS3{AccessKeyID: "access-key", SecretAccessKey: "secret-key"},
			bucket:  "my-bucket",
			entries: []errorstack.Entry{missing("region")},
		},
		{
			name:    "missing access key is invalid",
			driver:  DriverS3{Region: "us-east-1", SecretAccessKey: "secret-key"},
			bucket:  "my-bucket",
			entries: []errorstack.Entry{missing("access_key_id")},
		},
		{
			name:    "missing secret key is invalid",
			driver:  DriverS3{Region: "us-east-1", AccessKeyID: "access-key"},
			bucket:  "my-bucket",
			entries: []errorstack.Entry{missing("secret_access_key")},
		},
		{
			name:    "missing region and key pair are each invalid",
			driver:  DriverS3{Endpoint: "http://localhost:9000"},
			bucket:  "my-bucket",
			entries: []errorstack.Entry{missing("region"), missing("access_key_id"), missing("secret_access_key")},
		},
		{
			name:    "endpoint without scheme is invalid",
			driver:  s3Driver("localhost:9000"),
			bucket:  "my-bucket",
			entries: []errorstack.Entry{invalidEndpoint},
		},
		{
			name:    "endpoint with another scheme is invalid",
			driver:  s3Driver("ftp://localhost:9000"),
			bucket:  "my-bucket",
			entries: []errorstack.Entry{invalidEndpoint},
		},
		{
			name:    "endpoint with credentials is invalid",
			driver:  s3Driver("https://key:secret@localhost:9000"),
			bucket:  "my-bucket",
			entries: []errorstack.Entry{invalidEndpoint},
		},
		{
			name:    "endpoint with a query is invalid",
			driver:  s3Driver("https://localhost:9000?anonymous=true"),
			bucket:  "my-bucket",
			entries: []errorstack.Entry{invalidEndpoint},
		},
		{
			name:    "endpoint with a fragment is invalid",
			driver:  s3Driver("https://localhost:9000#x"),
			bucket:  "my-bucket",
			entries: []errorstack.Entry{invalidEndpoint},
		},
	}

	for _, injection := range injections {
		testcases = append(testcases, struct {
			name    string
			driver  DriverS3
			bucket  string
			entries []errorstack.Entry
		}{
			name:    "injection " + injection + " is invalid",
			driver:  s3Driver(""),
			bucket:  injection,
			entries: []errorstack.Entry{invalidName},
		})
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.entries, tc.driver.validate(tc.bucket))
		})
	}
}

// TestNewS3Client pins that the S3 client is built from the driver and from
// nothing else: its region, its key pair and, when set, its endpoint addressed
// path-style, while every AWS_* variable and the shared configuration and
// credentials files name others (setHostileS3Env).
func TestNewS3Client(t *testing.T) {
	setHostileS3Env(t)

	testcases := []struct {
		name         string
		driver       DriverS3
		baseEndpoint string
		usePathStyle bool
	}{
		{
			name:         "endpoint is the one configured, path-style",
			driver:       s3Driver("https://storage.example.com"),
			baseEndpoint: "https://storage.example.com",
			usePathStyle: true,
		},
		{
			name:   "no endpoint leaves the provider's own, virtual-hosted",
			driver: s3Driver(""),
		},
		{
			name:   "region carrying a query stays the region",
			driver: DriverS3{Region: "x&endpoint=https://elsewhere.example", AccessKeyID: "access-key", SecretAccessKey: "secret-key"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			options := newS3Client(tc.driver).Options()

			assert.Equal(t, tc.driver.Region, options.Region)
			assert.Equal(t, tc.usePathStyle, options.UsePathStyle)

			if tc.baseEndpoint == "" {
				assert.Nil(t, options.BaseEndpoint)
			} else {
				require.NotNil(t, options.BaseEndpoint)
				assert.Equal(t, tc.baseEndpoint, *options.BaseEndpoint)
			}

			creds, err := options.Credentials.Retrieve(t.Context())
			require.NoError(t, err)

			assert.Equal(t, tc.driver.AccessKeyID, creds.AccessKeyID)
			assert.Equal(t, tc.driver.SecretAccessKey, creds.SecretAccessKey)
			assert.Empty(t, creds.SessionToken)
		})
	}
}

func TestDriverAzure_Validate(t *testing.T) {
	invalidName := errorstack.Entry{
		Message: "Must be 3 to 63 lowercase letters, digits or single hyphens, starting and ending with a letter or digit",
		Path:    []any{"config", "bucket"},
	}

	invalidAccountName := errorstack.Entry{
		Message: "Must be 3 to 24 lowercase letters or digits",
		Path:    driverPath("account_name"),
	}

	invalidAccountKey := errorstack.Entry{
		Message: "Must be a base64-encoded key",
		Path:    driverPath("account_key"),
	}

	invalidEndpoint := errorstack.Entry{
		Message: "Must be an http or https URL without credentials, query or fragment",
		Path:    driverPath("endpoint"),
	}

	withAccountName := func(name string) DriverAzure {
		d := azureDriver("")
		d.AccountName = name
		return d
	}

	withAccountKey := func(key string) DriverAzure {
		d := azureDriver("")
		d.AccountKey = key
		return d
	}

	testcases := []struct {
		name    string
		driver  DriverAzure
		bucket  string
		entries []errorstack.Entry
	}{
		{
			name:   "container name is valid",
			driver: azureDriver(""),
			bucket: "my-container-1",
		},
		{
			name:   "reserved container is valid",
			driver: azureDriver(""),
			bucket: "$web",
		},
		{
			name:    "doubled hyphen is invalid",
			driver:  azureDriver(""),
			bucket:  "my--container",
			entries: []errorstack.Entry{invalidName},
		},
		{
			name:    "too long container is invalid",
			driver:  azureDriver(""),
			bucket:  strings.Repeat("a", 64),
			entries: []errorstack.Entry{invalidName},
		},
		{
			name:    "underscore is invalid",
			driver:  azureDriver(""),
			bucket:  "my_container",
			entries: []errorstack.Entry{invalidName},
		},
		{
			name:    "missing account name and key are each invalid",
			driver:  DriverAzure{},
			bucket:  "my-container",
			entries: []errorstack.Entry{missing("account_name"), missing("account_key")},
		},
		{
			name:    "uppercase account name is invalid",
			driver:  withAccountName("MyAccount"),
			bucket:  "my-container",
			entries: []errorstack.Entry{invalidAccountName},
		},
		{
			name:    "too long account name is invalid",
			driver:  withAccountName(strings.Repeat("a", 25)),
			bucket:  "my-container",
			entries: []errorstack.Entry{invalidAccountName},
		},
		{
			name:    "account name reshaping the host is invalid",
			driver:  withAccountName("x.elsewhere.example/y"),
			bucket:  "my-container",
			entries: []errorstack.Entry{invalidAccountName},
		},
		{
			name:    "account key that is not base64 is invalid",
			driver:  withAccountKey("not base64!"),
			bucket:  "my-container",
			entries: []errorstack.Entry{invalidAccountKey},
		},
		{
			name:   "endpoint of the service is valid",
			driver: azureDriver("https://configaccount.blob.core.usgovcloudapi.net"),
			bucket: "my-container",
		},
		{
			name:   "endpoint with the account in its path is valid",
			driver: azureDriver("http://azurite:10000/devstoreaccount1"),
			bucket: "my-container",
		},
		{
			name:    "endpoint with a query is invalid",
			driver:  azureDriver("https://configaccount.blob.core.windows.net?sv=x"),
			bucket:  "my-container",
			entries: []errorstack.Entry{invalidEndpoint},
		},
		{
			name:    "endpoint without scheme is invalid",
			driver:  azureDriver("127.0.0.1:10000/devstoreaccount1"),
			bucket:  "my-container",
			entries: []errorstack.Entry{invalidEndpoint},
		},
	}

	for _, injection := range injections {
		testcases = append(testcases, struct {
			name    string
			driver  DriverAzure
			bucket  string
			entries []errorstack.Entry
		}{
			name:    "injection " + injection + " is invalid",
			driver:  azureDriver(""),
			bucket:  injection,
			entries: []errorstack.Entry{invalidName},
		})
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.entries, tc.driver.validate(tc.bucket))
		})
	}
}

// TestNewContainerClient pins the URL of the container client: the driver's
// Endpoint used as given, account path included, or the account's public service
// when it is empty, while every AZURE_STORAGE_* variable names another account
// and service (setHostileAzureEnv).
func TestNewContainerClient(t *testing.T) {
	setHostileAzureEnv(t)

	testcases := []struct {
		name      string
		endpoint  string
		container string
		url       string
	}{
		{
			name:      "no endpoint is the account's public service",
			container: "my-container",
			url:       "https://configaccount.blob.core.windows.net/my-container",
		},
		{
			name:      "sovereign cloud",
			endpoint:  "https://configaccount.blob.core.usgovcloudapi.net",
			container: "my-container",
			url:       "https://configaccount.blob.core.usgovcloudapi.net/my-container",
		},
		{
			name:      "azurite under any host",
			endpoint:  "http://azurite:10000/devstoreaccount1",
			container: "my-container",
			url:       "http://azurite:10000/devstoreaccount1/my-container",
		},
		{
			name:      "endpoint with a trailing slash",
			endpoint:  "http://127.0.0.1:10000/devstoreaccount1/",
			container: "my-container",
			url:       "http://127.0.0.1:10000/devstoreaccount1/my-container",
		},
		{
			name:      "reserved container",
			container: "$web",
			url:       "https://configaccount.blob.core.windows.net/$web",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := newContainerClient(azureDriver(tc.endpoint), tc.container)
			require.NoError(t, err)

			assert.Equal(t, tc.url, client.URL())
		})
	}
}

func TestDriverGoogleCloud_Validate(t *testing.T) {
	invalidName := errorstack.Entry{
		Message: "Must be 3 to 222 lowercase letters, digits, '.', '-' or '_', starting and ending with a letter or digit",
		Path:    []any{"config", "bucket"},
	}

	invalidKey := errorstack.Entry{
		Message: "Must be a service account key in JSON",
		Path:    driverPath("credentials_json"),
	}

	invalidEndpoint := errorstack.Entry{
		Message: "Must be an http or https URL of the service domain, without path, credentials, query or fragment",
		Path:    driverPath("endpoint"),
	}

	valid := googleDriver(t, "")

	withKey := func(key string) DriverGoogleCloud {
		return DriverGoogleCloud{CredentialsJSON: key}
	}

	withEndpoint := func(endpoint string) DriverGoogleCloud {
		d := valid
		d.Endpoint = endpoint
		return d
	}

	// A key that only breaks one rule: the one the case names.
	keyWith := func(field, value string) string {
		var fields map[string]string
		require.NoError(t, json.Unmarshal([]byte(valid.CredentialsJSON), &fields))
		fields[field] = value
		return encodeKey(t, fields)
	}

	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ecdsaDER, err := x509.MarshalPKCS8PrivateKey(ecdsaKey)
	require.NoError(t, err)

	testcases := []struct {
		name    string
		driver  DriverGoogleCloud
		bucket  string
		entries []errorstack.Entry
	}{
		{
			name:   "bucket name is valid",
			driver: valid,
			bucket: "my_bucket.example-1",
		},
		{
			name:    "uppercase bucket is invalid",
			driver:  valid,
			bucket:  "My-Bucket",
			entries: []errorstack.Entry{invalidName},
		},
		{
			name:    "missing key is invalid",
			driver:  DriverGoogleCloud{},
			bucket:  "my-bucket",
			entries: []errorstack.Entry{missing("credentials_json")},
		},
		{
			name:    "key that is not JSON is invalid",
			driver:  withKey("not json"),
			bucket:  "my-bucket",
			entries: []errorstack.Entry{invalidKey},
		},
		{
			name:    "key of a user rather than a service account is invalid",
			driver:  withKey(keyWith("type", "authorized_user")),
			bucket:  "my-bucket",
			entries: []errorstack.Entry{invalidKey},
		},
		{
			name:    "key naming no account is invalid",
			driver:  withKey(keyWith("client_email", "")),
			bucket:  "my-bucket",
			entries: []errorstack.Entry{invalidKey},
		},
		{
			name:    "key whose private key is not PEM is invalid",
			driver:  withKey(keyWith("private_key", "not a pem block")),
			bucket:  "my-bucket",
			entries: []errorstack.Entry{invalidKey},
		},
		{
			name:    "key whose private key is not RSA is invalid",
			driver:  withKey(keyWith("private_key", string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecdsaDER})))),
			bucket:  "my-bucket",
			entries: []errorstack.Entry{invalidKey},
		},
		{
			name:   "endpoint of a domain is valid",
			driver: withEndpoint("http://localhost:4443"),
			bucket: "my-bucket",
		},
		{
			name:   "endpoint with a root path is valid",
			driver: withEndpoint("https://storage-myendpoint.p.googleapis.com/"),
			bucket: "my-bucket",
		},
		{
			name:    "endpoint with a path is invalid",
			driver:  withEndpoint("http://localhost:4443/storage/v1"),
			bucket:  "my-bucket",
			entries: []errorstack.Entry{invalidEndpoint},
		},
		{
			name:    "endpoint without scheme is invalid",
			driver:  withEndpoint("localhost:4443"),
			bucket:  "my-bucket",
			entries: []errorstack.Entry{invalidEndpoint},
		},
	}

	for _, injection := range injections {
		testcases = append(testcases, struct {
			name    string
			driver  DriverGoogleCloud
			bucket  string
			entries []errorstack.Entry
		}{
			name:    "injection " + injection + " is invalid",
			driver:  valid,
			bucket:  injection,
			entries: []errorstack.Entry{invalidName},
		})
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.entries, tc.driver.validate(tc.bucket))
		})
	}
}

func TestGoogleEndpoint(t *testing.T) {
	testcases := []struct {
		endpoint string
		want     string
	}{
		{endpoint: "", want: "https://storage.googleapis.com/storage/v1/"},
		{endpoint: "http://localhost:4443", want: "http://localhost:4443/storage/v1/"},
		{endpoint: "https://storage-myendpoint.p.googleapis.com/", want: "https://storage-myendpoint.p.googleapis.com/storage/v1/"},
	}

	for _, tc := range testcases {
		t.Run(tc.endpoint, func(t *testing.T) {
			got, err := googleEndpoint(DriverGoogleCloud{Endpoint: tc.endpoint})
			require.NoError(t, err)

			assert.Equal(t, tc.want, got)
		})
	}
}

// TestDriverLocal_Validate pins that any directory is valid, even one named like
// an injection: none is formatted into a URL.
func TestDriverLocal_Validate(t *testing.T) {
	for _, dir := range append([]string{"relative/dir", "/srv/blobs"}, injections...) {
		assert.Empty(t, DriverLocal{}.validate(dir), dir)
	}
}
