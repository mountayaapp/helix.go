package bucket

import (
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/stretchr/testify/assert"
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

// TestConfig_SanitizeRequiresNoCredentials pins that no driver reads credentials
// from the environment to validate a Config. Each of them used to refuse one
// with no static key in it, which ruled out every credential that is not a
// static key: IAM Roles for Service Accounts, instance profiles, GKE Workload
// Identity and Azure managed identity alike.
func TestConfig_SanitizeRequiresNoCredentials(t *testing.T) {
	for _, key := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_REGION",
		"GOOGLE_APPLICATION_CREDENTIALS",
		"AZURE_STORAGE_ACCOUNT", "AZURE_STORAGE_KEY", "AZURE_STORAGE_SAS_TOKEN",
	} {
		t.Setenv(key, "")
	}

	testcases := []Config{
		{Driver: DriverAWS, Bucket: "my-bucket"},
		{Driver: DriverAWS, Bucket: "my-bucket", Region: "eu-west-1"},
		{Driver: DriverGoogleCloud, Bucket: "my-bucket"},
		{Driver: DriverAzure, Bucket: "my-container"},
	}

	for _, cfg := range testcases {
		t.Run(string(cfg.Driver), func(t *testing.T) {
			assert.NoError(t, cfg.sanitize())
		})
	}
}
