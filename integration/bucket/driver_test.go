package bucket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// injections are bucket names that would each add a query parameter of their
// own to a driver URL formatted with them — redirecting requests, dropping
// credentials, or pointing fileblob at a key file — or move its path.
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

func TestDriver_Impl(t *testing.T) {
	assert.Equal(t, driverAWS{}, DriverAWS.impl())
	assert.Equal(t, driverAzure{}, DriverAzure.impl())
	assert.Equal(t, driverGoogleCloud{}, DriverGoogleCloud.impl())
	assert.Equal(t, driverLocal{}, DriverLocal.impl())
	assert.Nil(t, Driver("").impl())
	assert.Nil(t, Driver("AWS").impl())
}

// withKeyPair returns cfg with the region and the key pair DriverAWS requires,
// so that a test of another rule sees that rule's entries alone.
func withKeyPair(cfg Config) Config {
	cfg.Region = "us-east-1"
	cfg.AccessKeyID = "access-key"
	cfg.SecretAccessKey = "secret-key"

	return cfg
}

func TestDriverAWS_Validate(t *testing.T) {
	invalidName := errorstack.Entry{
		Message: "Must be 3 to 255 letters, digits, '.', '-' or '_', starting and ending with a letter or digit",
		Path:    []any{"config", "bucket"},
	}

	invalidEndpoint := errorstack.Entry{
		Message: "Must be an http or https URL without credentials, query or fragment",
		Path:    []any{"config", "endpoint"},
	}

	missingRegion := errorstack.Entry{Message: "Must be set", Path: []any{"config", "region"}}
	missingAccessKeyID := errorstack.Entry{Message: "Must be set", Path: []any{"config", "access_key_id"}}
	missingSecretAccessKey := errorstack.Entry{Message: "Must be set", Path: []any{"config", "secret_access_key"}}

	testcases := []struct {
		name    string
		cfg     Config
		entries []errorstack.Entry
	}{
		{
			name: "bucket name is valid",
			cfg:  withKeyPair(Config{Bucket: "my-bucket.2024"}),
		},
		{
			name: "legacy bucket name is valid",
			cfg:  withKeyPair(Config{Bucket: "My_Legacy_Bucket"}),
		},
		{
			name: "empty bucket is left to the shared check",
			cfg:  withKeyPair(Config{}),
		},
		{
			name:    "too short bucket is invalid",
			cfg:     withKeyPair(Config{Bucket: "ab"}),
			entries: []errorstack.Entry{invalidName},
		},
		{
			name:    "bucket ending with a hyphen is invalid",
			cfg:     withKeyPair(Config{Bucket: "my-bucket-"}),
			entries: []errorstack.Entry{invalidName},
		},
		{
			name: "region, endpoint and key pair are valid",
			cfg: Config{
				Bucket:          "my-bucket",
				Region:          "us-east-1",
				Endpoint:        "http://localhost:9000",
				AccessKeyID:     "access-key",
				SecretAccessKey: "secret-key",
			},
		},
		{
			name:    "missing region is invalid",
			cfg:     Config{Bucket: "my-bucket", AccessKeyID: "access-key", SecretAccessKey: "secret-key"},
			entries: []errorstack.Entry{missingRegion},
		},
		{
			name:    "missing access key is invalid",
			cfg:     Config{Bucket: "my-bucket", Region: "us-east-1", SecretAccessKey: "secret-key"},
			entries: []errorstack.Entry{missingAccessKeyID},
		},
		{
			name:    "missing secret key is invalid",
			cfg:     Config{Bucket: "my-bucket", Region: "us-east-1", AccessKeyID: "access-key"},
			entries: []errorstack.Entry{missingSecretAccessKey},
		},
		{
			name:    "missing region and key pair are each invalid",
			cfg:     Config{Bucket: "my-bucket", Endpoint: "http://localhost:9000"},
			entries: []errorstack.Entry{missingRegion, missingAccessKeyID, missingSecretAccessKey},
		},
		{
			name: "endpoint with a path is valid",
			cfg:  withKeyPair(Config{Bucket: "my-bucket", Endpoint: "https://storage.example.com/s3"}),
		},
		{
			name:    "endpoint without scheme is invalid",
			cfg:     withKeyPair(Config{Bucket: "my-bucket", Endpoint: "localhost:9000"}),
			entries: []errorstack.Entry{invalidEndpoint},
		},
		{
			name:    "endpoint with another scheme is invalid",
			cfg:     withKeyPair(Config{Bucket: "my-bucket", Endpoint: "ftp://localhost:9000"}),
			entries: []errorstack.Entry{invalidEndpoint},
		},
		{
			name:    "endpoint with credentials is invalid",
			cfg:     withKeyPair(Config{Bucket: "my-bucket", Endpoint: "https://key:secret@localhost:9000"}),
			entries: []errorstack.Entry{invalidEndpoint},
		},
		{
			name:    "endpoint with a query is invalid",
			cfg:     withKeyPair(Config{Bucket: "my-bucket", Endpoint: "https://localhost:9000?anonymous=true"}),
			entries: []errorstack.Entry{invalidEndpoint},
		},
		{
			name:    "endpoint with a fragment is invalid",
			cfg:     withKeyPair(Config{Bucket: "my-bucket", Endpoint: "https://localhost:9000#x"}),
			entries: []errorstack.Entry{invalidEndpoint},
		},
	}

	for _, injection := range injections {
		testcases = append(testcases, struct {
			name    string
			cfg     Config
			entries []errorstack.Entry
		}{
			name:    "injection " + injection + " is invalid",
			cfg:     withKeyPair(Config{Bucket: injection}),
			entries: []errorstack.Entry{invalidName},
		})
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.entries, driverAWS{}.validate(&tc.cfg))
		})
	}
}

// TestNewS3Client pins that the S3 client is built from the Config and from
// nothing else: its region, its key pair and, when set, its endpoint addressed
// path-style, while every AWS_* variable and the shared configuration and
// credentials files name others (setHostileAWSEnv).
func TestNewS3Client(t *testing.T) {
	setHostileAWSEnv(t)

	testcases := []struct {
		name         string
		cfg          Config
		baseEndpoint string
		usePathStyle bool
	}{
		{
			name:         "endpoint is the one configured, path-style",
			cfg:          Config{Bucket: "my-bucket", Region: "eu-west-3", Endpoint: "https://storage.example.com", AccessKeyID: "access-key", SecretAccessKey: "secret-key"},
			baseEndpoint: "https://storage.example.com",
			usePathStyle: true,
		},
		{
			name: "no endpoint leaves the provider's own, virtual-hosted",
			cfg:  Config{Bucket: "my-bucket", Region: "eu-west-3", AccessKeyID: "access-key", SecretAccessKey: "secret-key"},
		},
		{
			name: "region carrying a query stays the region",
			cfg:  Config{Bucket: "my-bucket", Region: "x&endpoint=https://elsewhere.example", AccessKeyID: "access-key", SecretAccessKey: "secret-key"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			options := newS3Client(&tc.cfg).Options()

			assert.Equal(t, tc.cfg.Region, options.Region)
			assert.Equal(t, tc.usePathStyle, options.UsePathStyle)

			if tc.baseEndpoint == "" {
				assert.Nil(t, options.BaseEndpoint)
			} else {
				require.NotNil(t, options.BaseEndpoint)
				assert.Equal(t, tc.baseEndpoint, *options.BaseEndpoint)
			}

			creds, err := options.Credentials.Retrieve(t.Context())
			require.NoError(t, err)

			assert.Equal(t, tc.cfg.AccessKeyID, creds.AccessKeyID)
			assert.Equal(t, tc.cfg.SecretAccessKey, creds.SecretAccessKey)
			assert.Empty(t, creds.SessionToken)
		})
	}
}

func TestDriverGoogleCloud_Validate(t *testing.T) {
	invalidName := errorstack.Entry{
		Message: "Must be 3 to 222 lowercase letters, digits, '.', '-' or '_', starting and ending with a letter or digit",
		Path:    []any{"config", "bucket"},
	}

	testcases := []struct {
		name    string
		cfg     Config
		entries []errorstack.Entry
	}{
		{
			name: "bucket name is valid",
			cfg:  Config{Bucket: "my_bucket.example-1"},
		},
		{
			name:    "uppercase bucket is invalid",
			cfg:     Config{Bucket: "My-Bucket"},
			entries: []errorstack.Entry{invalidName},
		},
		{
			name: "region and endpoint are refused",
			cfg:  Config{Bucket: "my-bucket", Region: "europe-west1", Endpoint: "http://localhost:4443"},
			entries: []errorstack.Entry{
				{Message: "Must be empty for the google driver", Path: []any{"config", "region"}},
				{Message: "Must be empty for the google driver", Path: []any{"config", "endpoint"}},
			},
		},
		{
			name: "key pair is refused",
			cfg:  Config{Bucket: "my-bucket", AccessKeyID: "access-key", SecretAccessKey: "secret-key"},
			entries: []errorstack.Entry{
				{Message: "Must be empty for the google driver", Path: []any{"config", "access_key_id"}},
				{Message: "Must be empty for the google driver", Path: []any{"config", "secret_access_key"}},
			},
		},
	}

	for _, injection := range injections {
		testcases = append(testcases, struct {
			name    string
			cfg     Config
			entries []errorstack.Entry
		}{
			name:    "injection " + injection + " is invalid",
			cfg:     Config{Bucket: injection},
			entries: []errorstack.Entry{invalidName},
		})
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.entries, driverGoogleCloud{}.validate(&tc.cfg))
		})
	}
}

func TestDriverAzure_Validate(t *testing.T) {
	invalidName := errorstack.Entry{
		Message: "Must be 3 to 63 lowercase letters, digits or single hyphens, starting and ending with a letter or digit",
		Path:    []any{"config", "bucket"},
	}

	invalidEndpoint := errorstack.Entry{
		Message: "Must be an http or https URL of the service domain, without path, credentials, query or fragment",
		Path:    []any{"config", "endpoint"},
	}

	testcases := []struct {
		name    string
		cfg     Config
		entries []errorstack.Entry
	}{
		{
			name: "container name is valid",
			cfg:  Config{Bucket: "my-container-1"},
		},
		{
			name: "reserved container is valid",
			cfg:  Config{Bucket: "$web"},
		},
		{
			name:    "doubled hyphen is invalid",
			cfg:     Config{Bucket: "my--container"},
			entries: []errorstack.Entry{invalidName},
		},
		{
			name:    "too long container is invalid",
			cfg:     Config{Bucket: strings.Repeat("a", 64)},
			entries: []errorstack.Entry{invalidName},
		},
		{
			name:    "underscore is invalid",
			cfg:     Config{Bucket: "my_container"},
			entries: []errorstack.Entry{invalidName},
		},
		{
			name: "endpoint of a domain is valid",
			cfg:  Config{Bucket: "my-container", Endpoint: "http://127.0.0.1:10000"},
		},
		{
			name:    "endpoint with a path is invalid",
			cfg:     Config{Bucket: "my-container", Endpoint: "http://127.0.0.1:10000/devstoreaccount1"},
			entries: []errorstack.Entry{invalidEndpoint},
		},
		{
			name:    "endpoint with a query is invalid",
			cfg:     Config{Bucket: "my-container", Endpoint: "https://blob.core.windows.net?sv=x"},
			entries: []errorstack.Entry{invalidEndpoint},
		},
		{
			name:    "region is refused",
			cfg:     Config{Bucket: "my-container", Region: "westeurope"},
			entries: []errorstack.Entry{{Message: "Must be empty for the azure driver", Path: []any{"config", "region"}}},
		},
		{
			name: "key pair is refused",
			cfg:  Config{Bucket: "my-container", AccessKeyID: "access-key", SecretAccessKey: "secret-key"},
			entries: []errorstack.Entry{
				{Message: "Must be empty for the azure driver", Path: []any{"config", "access_key_id"}},
				{Message: "Must be empty for the azure driver", Path: []any{"config", "secret_access_key"}},
			},
		},
	}

	for _, injection := range injections {
		testcases = append(testcases, struct {
			name    string
			cfg     Config
			entries []errorstack.Entry
		}{
			name:    "injection " + injection + " is invalid",
			cfg:     Config{Bucket: injection},
			entries: []errorstack.Entry{invalidName},
		})
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.entries, driverAzure{}.validate(&tc.cfg))
		})
	}
}

func TestDriverLocal_Validate(t *testing.T) {
	assert.Empty(t, driverLocal{}.validate(&Config{Bucket: "relative/dir"}))

	assert.Equal(t, []errorstack.Entry{
		{Message: "Must be empty for the local driver", Path: []any{"config", "region"}},
		{Message: "Must be empty for the local driver", Path: []any{"config", "endpoint"}},
		{Message: "Must be empty for the local driver", Path: []any{"config", "access_key_id"}},
		{Message: "Must be empty for the local driver", Path: []any{"config", "secret_access_key"}},
	}, driverLocal{}.validate(&Config{
		Bucket:          "/tmp",
		Region:          "us-east-1",
		Endpoint:        "http://localhost:9000",
		AccessKeyID:     "access-key",
		SecretAccessKey: "secret-key",
	}))
}

func TestDriver_URL(t *testing.T) {
	testcases := []struct {
		name   string
		driver urlDriver
		cfg    Config
		url    string
	}{
		{
			name:   "google",
			driver: driverGoogleCloud{},
			cfg:    Config{Bucket: "my-bucket"},
			url:    "gs://my-bucket",
		},
		{
			name:   "azure",
			driver: driverAzure{},
			cfg:    Config{Bucket: "my-container"},
			url:    "azblob://my-container",
		},
		{
			name:   "azure with endpoint splits it into protocol and domain",
			driver: driverAzure{},
			cfg:    Config{Bucket: "my-container", Endpoint: "http://127.0.0.1:10000"},
			url:    "azblob://my-container?domain=127.0.0.1%3A10000&protocol=http",
		},
		{
			name:   "local with an absolute path",
			driver: driverLocal{},
			cfg:    Config{Bucket: "/srv/blobs"},
			url:    "file:///srv/blobs",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := tc.driver.url(&tc.cfg)
			require.NoError(t, err)

			assert.Equal(t, tc.url, u.String())
		})
	}
}

// TestDriver_URLEscapesFields pins that what a Config field holds stays inside
// that field of the driver URL, even when it was never validated: a bucket name
// carrying a query string adds no parameter.
func TestDriver_URLEscapesFields(t *testing.T) {
	for _, injection := range injections {
		t.Run(injection, func(t *testing.T) {
			for _, d := range []urlDriver{driverGoogleCloud{}, driverAzure{}} {
				u, err := d.url(&Config{Bucket: injection})
				require.NoError(t, err)

				assert.Equal(t, injection, u.Host)
				assert.Empty(t, u.Query())
			}
		})
	}

	dir := filepath.Join(t.TempDir(), "x?base_url=/show&secret_key_path=key")
	u, err := driverLocal{}.url(&Config{Bucket: dir})
	require.NoError(t, err)

	assert.Equal(t, slashPath(dir), u.Path)
	assert.Empty(t, u.Query())
}

// TestDriverLocal_URLResolvesRelativePath pins the regression: a relative
// directory used to be formatted into "file://data/uploads", whose host is
// "data", so fileblob opened "/uploads" at the filesystem root.
func TestDriverLocal_URLResolvesRelativePath(t *testing.T) {
	wd := t.TempDir()
	t.Chdir(wd)

	// The working directory as the process sees it, which on macOS is the
	// temporary directory with its symbolic link resolved.
	wd, err := os.Getwd()
	require.NoError(t, err)

	u, err := driverLocal{}.url(&Config{Bucket: "data/uploads"})
	require.NoError(t, err)

	assert.Equal(t, "file", u.Scheme)
	assert.Empty(t, u.Host)
	assert.Equal(t, slashPath(filepath.Join(wd, "data", "uploads")), u.Path)
}

// slashPath returns the URL path of the absolute directory dir.
func slashPath(dir string) string {
	return "/" + strings.TrimPrefix(filepath.ToSlash(dir), "/")
}
