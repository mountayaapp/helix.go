package bucket

import (
	"encoding/json"
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_Sanitize(t *testing.T) {
	testcases := []struct {
		name   string
		before Config
		after  Config
		err    error
	}{
		{
			name:   "empty config returns driver and bucket errors",
			before: Config{},
			after:  Config{},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "driver"}},
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "bucket"}},
			),
		},
		{
			name: "valid driver and bucket is valid",
			before: Config{
				Driver: DriverLocal,
				Bucket: "anything",
			},
			after: Config{
				Driver: DriverLocal,
				Bucket: "anything",
			},
			err: nil,
		},
		{
			name: "missing driver returns error",
			before: Config{
				Bucket: "anything",
			},
			after: Config{
				Bucket: "anything",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "driver"}},
			),
		},
		{
			name: "unknown driver returns error",
			before: Config{
				Driver: Driver("ftp"),
				Bucket: "anything",
			},
			after: Config{
				Driver: Driver("ftp"),
				Bucket: "anything",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be one of: aws, azure, google, local", Path: []any{"config", "driver"}},
			),
		},
		{
			name: "missing bucket returns error",
			before: Config{
				Driver: DriverLocal,
			},
			after: Config{
				Driver: DriverLocal,
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "bucket"}},
			),
		},
		{
			name: "subfolder without trailing slash gets one",
			before: Config{
				Driver:    DriverLocal,
				Bucket:    "anything",
				Subfolder: "my/subfolder",
			},
			after: Config{
				Driver:    DriverLocal,
				Bucket:    "anything",
				Subfolder: "my/subfolder/",
			},
			err: nil,
		},
		{
			name: "subfolder with a leading slash loses it",
			before: Config{
				Driver:    DriverLocal,
				Bucket:    "anything",
				Subfolder: "/my/subfolder/",
			},
			after: Config{
				Driver:    DriverLocal,
				Bucket:    "anything",
				Subfolder: "my/subfolder/",
			},
			err: nil,
		},
		{
			name: "subfolder with repeated slashes at either end keeps one trailing",
			before: Config{
				Driver:    DriverLocal,
				Bucket:    "anything",
				Subfolder: "//my/subfolder//",
			},
			after: Config{
				Driver:    DriverLocal,
				Bucket:    "anything",
				Subfolder: "my/subfolder/",
			},
			err: nil,
		},
		{
			name: "subfolder as root slash is no subfolder",
			before: Config{
				Driver:    DriverLocal,
				Bucket:    "anything",
				Subfolder: "/",
			},
			after: Config{
				Driver: DriverLocal,
				Bucket: "anything",
			},
			err: nil,
		},
		{
			name: "empty subfolder is valid",
			before: Config{
				Driver:    DriverLocal,
				Bucket:    "anything",
				Subfolder: "",
			},
			after: Config{
				Driver:    DriverLocal,
				Bucket:    "anything",
				Subfolder: "",
			},
			err: nil,
		},
		{
			name: "aws without region and key pair returns an error each",
			before: Config{
				Driver:   DriverAWS,
				Bucket:   "my-bucket",
				Endpoint: "http://localhost:9000",
			},
			after: Config{
				Driver:   DriverAWS,
				Bucket:   "my-bucket",
				Endpoint: "http://localhost:9000",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "region"}},
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "access_key_id"}},
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "secret_access_key"}},
			),
		},
		{
			name: "every error combined",
			before: Config{
				Driver:    DriverGoogleCloud,
				Bucket:    "Not_Valid?",
				Subfolder: "not/a/valid/path",
				Region:    "europe-west1",
				Endpoint:  "http://localhost:4443",
			},
			after: Config{
				Driver:    DriverGoogleCloud,
				Bucket:    "Not_Valid?",
				Subfolder: "not/a/valid/path/",
				Region:    "europe-west1",
				Endpoint:  "http://localhost:4443",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{
					Message: "Must be 3 to 222 lowercase letters, digits, '.', '-' or '_', starting and ending with a letter or digit",
					Path:    []any{"config", "bucket"},
				},
				errorstack.Entry{Message: "Must be empty for the google driver", Path: []any{"config", "region"}},
				errorstack.Entry{Message: "Must be empty for the google driver", Path: []any{"config", "endpoint"}},
			),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.before.sanitize()

			assert.Equal(t, tc.after, tc.before)
			assert.Equal(t, tc.err, err)
		})
	}
}

// TestConfig_SanitizeReadsNoCredentials pins that no driver reads credentials
// from the environment to validate a Config. The Google Cloud and Azure drivers
// take a Config with no credential at all, which leaves every credential that is
// not a static key to their SDKs' chains: GKE Workload Identity and Azure managed
// identity alike. DriverAWS takes its key pair from the Config alone, so one set
// in the environment does not stand in for a missing one.
func TestConfig_SanitizeReadsNoCredentials(t *testing.T) {
	for _, key := range []string{
		"GOOGLE_APPLICATION_CREDENTIALS",
		"AZURE_STORAGE_ACCOUNT", "AZURE_STORAGE_KEY", "AZURE_STORAGE_SAS_TOKEN",
	} {
		t.Setenv(key, "")
	}

	t.Setenv("AWS_ACCESS_KEY_ID", "from-env")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "from-env")
	t.Setenv("AWS_REGION", "us-west-2")

	testcases := []struct {
		name string
		cfg  Config
		err  error
	}{
		{
			name: "aws with its key pair and region",
			cfg:  Config{Driver: DriverAWS, Bucket: "my-bucket", Region: "eu-west-1", AccessKeyID: "access-key", SecretAccessKey: "secret-key"},
		},
		{
			name: "aws without them",
			cfg:  Config{Driver: DriverAWS, Bucket: "my-bucket"},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "region"}},
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "access_key_id"}},
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "secret_access_key"}},
			),
		},
		{
			name: "google",
			cfg:  Config{Driver: DriverGoogleCloud, Bucket: "my-bucket"},
		},
		{
			name: "azure",
			cfg:  Config{Driver: DriverAzure, Bucket: "my-container"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.err, tc.cfg.sanitize())
		})
	}
}

// TestConfig_KeyPairIsNeverEncoded pins that a Config encoded to JSON — logged,
// or written to a file — carries no part of its key pair.
func TestConfig_KeyPairIsNeverEncoded(t *testing.T) {
	encoded, err := json.Marshal(Config{
		Driver:          DriverAWS,
		Bucket:          "my-bucket",
		Region:          "eu-west-1",
		AccessKeyID:     "the-access-key",
		SecretAccessKey: "the-secret-key",
	})
	require.NoError(t, err)

	assert.NotContains(t, string(encoded), "the-access-key")
	assert.NotContains(t, string(encoded), "the-secret-key")
	assert.Contains(t, string(encoded), `"region":"eu-west-1"`)
}
