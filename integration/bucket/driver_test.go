package bucket

import (
	"net/url"
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

func TestDriverAWS_Validate(t *testing.T) {
	invalidName := errorstack.Entry{
		Message: "Must be 3 to 255 letters, digits, '.', '-' or '_', starting and ending with a letter or digit",
		Path:    []any{"config", "bucket"},
	}

	invalidEndpoint := errorstack.Entry{
		Message: "Must be an http or https URL without credentials, query or fragment",
		Path:    []any{"config", "endpoint"},
	}

	testcases := []struct {
		name    string
		cfg     Config
		entries []errorstack.Entry
	}{
		{
			name: "bucket name is valid",
			cfg:  Config{Bucket: "my-bucket.2024"},
		},
		{
			name: "legacy bucket name is valid",
			cfg:  Config{Bucket: "My_Legacy_Bucket"},
		},
		{
			name: "empty bucket is left to the shared check",
			cfg:  Config{},
		},
		{
			name:    "too short bucket is invalid",
			cfg:     Config{Bucket: "ab"},
			entries: []errorstack.Entry{invalidName},
		},
		{
			name:    "bucket ending with a hyphen is invalid",
			cfg:     Config{Bucket: "my-bucket-"},
			entries: []errorstack.Entry{invalidName},
		},
		{
			name: "region and endpoint are valid",
			cfg: Config{
				Bucket:   "my-bucket",
				Region:   "us-east-1",
				Endpoint: "http://localhost:9000",
			},
		},
		{
			name: "endpoint with a path is valid",
			cfg:  Config{Bucket: "my-bucket", Endpoint: "https://storage.example.com/s3"},
		},
		{
			name:    "endpoint without scheme is invalid",
			cfg:     Config{Bucket: "my-bucket", Endpoint: "localhost:9000"},
			entries: []errorstack.Entry{invalidEndpoint},
		},
		{
			name:    "endpoint with another scheme is invalid",
			cfg:     Config{Bucket: "my-bucket", Endpoint: "ftp://localhost:9000"},
			entries: []errorstack.Entry{invalidEndpoint},
		},
		{
			name:    "endpoint with credentials is invalid",
			cfg:     Config{Bucket: "my-bucket", Endpoint: "https://key:secret@localhost:9000"},
			entries: []errorstack.Entry{invalidEndpoint},
		},
		{
			name:    "endpoint with a query is invalid",
			cfg:     Config{Bucket: "my-bucket", Endpoint: "https://localhost:9000?anonymous=true"},
			entries: []errorstack.Entry{invalidEndpoint},
		},
		{
			name:    "endpoint with a fragment is invalid",
			cfg:     Config{Bucket: "my-bucket", Endpoint: "https://localhost:9000#x"},
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
			cfg:     Config{Bucket: injection},
			entries: []errorstack.Entry{invalidName},
		})
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.entries, driverAWS{}.validate(&tc.cfg))
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
	}, driverLocal{}.validate(&Config{
		Bucket:   "/tmp",
		Region:   "us-east-1",
		Endpoint: "http://localhost:9000",
	}))
}

func TestDriver_URL(t *testing.T) {
	testcases := []struct {
		name   string
		driver driver
		cfg    Config
		url    string
	}{
		{
			name:   "aws without region leaves it to the SDK",
			driver: driverAWS{},
			cfg:    Config{Bucket: "my-bucket"},
			url:    "s3://my-bucket",
		},
		{
			name:   "aws with region",
			driver: driverAWS{},
			cfg:    Config{Bucket: "my-bucket", Region: "eu-west-1"},
			url:    "s3://my-bucket?region=eu-west-1",
		},
		{
			name:   "aws with endpoint is path-style",
			driver: driverAWS{},
			cfg:    Config{Bucket: "my-bucket", Region: "auto", Endpoint: "https://account.r2.cloudflarestorage.com"},
			url:    "s3://my-bucket?endpoint=https%3A%2F%2Faccount.r2.cloudflarestorage.com&region=auto&use_path_style=true",
		},
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
// or a region carrying a query string adds no parameter.
func TestDriver_URLEscapesFields(t *testing.T) {
	for _, injection := range injections {
		t.Run(injection, func(t *testing.T) {
			for _, d := range []driver{driverAWS{}, driverGoogleCloud{}, driverAzure{}} {
				u, err := d.url(&Config{Bucket: injection})
				require.NoError(t, err)

				assert.Equal(t, injection, u.Host)
				assert.Empty(t, u.Query())
			}
		})
	}

	u, err := driverAWS{}.url(&Config{Bucket: "my-bucket", Region: "x&endpoint=https://elsewhere.example"})
	require.NoError(t, err)

	assert.Equal(t, url.Values{"region": {"x&endpoint=https://elsewhere.example"}}, u.Query())

	dir := filepath.Join(t.TempDir(), "x?base_url=/show&secret_key_path=key")
	u, err = driverLocal{}.url(&Config{Bucket: dir})
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
