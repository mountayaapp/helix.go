package bucket

import (
	"encoding/base64"
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"testing"

	"github.com/mountaya/helix.go/errorstack"

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
				Driver: DriverLocal{},
				Bucket: "anything",
			},
			after: Config{
				Driver: DriverLocal{},
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
			name: "nil pointer to a driver is no driver",
			before: Config{
				Driver: (*DriverS3)(nil),
				Bucket: "anything",
			},
			after: Config{
				Driver: (*DriverS3)(nil),
				Bucket: "anything",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "driver"}},
			),
		},
		{
			name: "pointer to a driver is valid",
			before: Config{
				Driver: &DriverLocal{},
				Bucket: "anything",
			},
			after: Config{
				Driver: &DriverLocal{},
				Bucket: "anything",
			},
			err: nil,
		},
		{
			name: "missing bucket returns error",
			before: Config{
				Driver: DriverLocal{},
			},
			after: Config{
				Driver: DriverLocal{},
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "bucket"}},
			),
		},
		{
			name: "subfolder without trailing slash gets one",
			before: Config{
				Driver:    DriverLocal{},
				Bucket:    "anything",
				Subfolder: "my/subfolder",
			},
			after: Config{
				Driver:    DriverLocal{},
				Bucket:    "anything",
				Subfolder: "my/subfolder/",
			},
			err: nil,
		},
		{
			name: "subfolder with a leading slash loses it",
			before: Config{
				Driver:    DriverLocal{},
				Bucket:    "anything",
				Subfolder: "/my/subfolder/",
			},
			after: Config{
				Driver:    DriverLocal{},
				Bucket:    "anything",
				Subfolder: "my/subfolder/",
			},
			err: nil,
		},
		{
			name: "subfolder with repeated slashes at either end keeps one trailing",
			before: Config{
				Driver:    DriverLocal{},
				Bucket:    "anything",
				Subfolder: "//my/subfolder//",
			},
			after: Config{
				Driver:    DriverLocal{},
				Bucket:    "anything",
				Subfolder: "my/subfolder/",
			},
			err: nil,
		},
		{
			name: "subfolder as root slash is no subfolder",
			before: Config{
				Driver:    DriverLocal{},
				Bucket:    "anything",
				Subfolder: "/",
			},
			after: Config{
				Driver: DriverLocal{},
				Bucket: "anything",
			},
			err: nil,
		},
		{
			name: "empty subfolder is valid",
			before: Config{
				Driver:    DriverLocal{},
				Bucket:    "anything",
				Subfolder: "",
			},
			after: Config{
				Driver:    DriverLocal{},
				Bucket:    "anything",
				Subfolder: "",
			},
			err: nil,
		},
		{
			name: "s3 without region and key pair returns an error each, under driver",
			before: Config{
				Driver: DriverS3{Endpoint: "http://localhost:9000"},
				Bucket: "my-bucket",
			},
			after: Config{
				Driver: DriverS3{Endpoint: "http://localhost:9000"},
				Bucket: "my-bucket",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "driver", "region"}},
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "driver", "access_key_id"}},
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "driver", "secret_access_key"}},
			),
		},
		{
			name: "every error combined",
			before: Config{
				Driver:    DriverGoogleCloud{Endpoint: "http://localhost:4443/storage/v1"},
				Bucket:    "Not_Valid?",
				Subfolder: "not/a/valid/path",
			},
			after: Config{
				Driver:    DriverGoogleCloud{Endpoint: "http://localhost:4443/storage/v1"},
				Bucket:    "Not_Valid?",
				Subfolder: "not/a/valid/path/",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{
					Message: "Must be 3 to 222 lowercase letters, digits, '.', '-' or '_', starting and ending with a letter or digit",
					Path:    []any{"config", "bucket"},
				},
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "driver", "credentials_json"}},
				errorstack.Entry{
					Message: "Must be an http or https URL of the service domain, without path, credentials, query or fragment",
					Path:    []any{"config", "driver", "endpoint"},
				},
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

// TestConfig_SanitizeReadsNoCredentials pins that no cloud driver reads a
// credential from the environment to validate a Config: each takes its own from
// the driver alone, so one the environment offers never stands in for a missing
// one.
func TestConfig_SanitizeReadsNoCredentials(t *testing.T) {
	setHostileS3Env(t)
	setHostileAzureEnv(t)
	setHostileGoogleEnv(t)

	testcases := []struct {
		name string
		cfg  Config
		err  error
	}{
		{
			name: "s3 with its key pair and region",
			cfg:  Config{Bucket: "my-bucket", Driver: s3Driver("")},
		},
		{
			name: "s3 without them",
			cfg:  Config{Bucket: "my-bucket", Driver: DriverS3{}},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "driver", "region"}},
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "driver", "access_key_id"}},
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "driver", "secret_access_key"}},
			),
		},
		{
			name: "azure with its account and key",
			cfg:  Config{Bucket: "my-container", Driver: azureDriver("")},
		},
		{
			name: "azure without them",
			cfg:  Config{Bucket: "my-container", Driver: DriverAzure{}},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "driver", "account_name"}},
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "driver", "account_key"}},
			),
		},
		{
			name: "google with its key",
			cfg:  Config{Bucket: "my-bucket", Driver: googleDriver(t, "")},
		},
		{
			name: "google without it",
			cfg:  Config{Bucket: "my-bucket", Driver: DriverGoogleCloud{}},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "driver", "credentials_json"}},
			),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.err, tc.cfg.sanitize())
		})
	}
}

// jsonCodecs are the codecs a Config may meet: encoding/json/v2, and the v1
// encoding/json that zap still logs a reflected field with. What the tests below
// pin must hold for both.
var jsonCodecs = []struct {
	name      string
	marshal   func(any) ([]byte, error)
	unmarshal func([]byte, any) error
}{
	{
		name:      "v2",
		marshal:   func(v any) ([]byte, error) { return json.Marshal(v) },
		unmarshal: func(b []byte, v any) error { return json.Unmarshal(b, v) },
	},
	{
		name:      "v1",
		marshal:   jsonv1.Marshal,
		unmarshal: jsonv1.Unmarshal,
	},
}

// TestConfig_CredentialsAreNeverEncoded pins that a Config encoded to JSON —
// logged, or written to a file — carries its driver's fields but no part of a
// credential, whichever the driver and whichever the codec.
func TestConfig_CredentialsAreNeverEncoded(t *testing.T) {
	testcases := []struct {
		name    string
		driver  Driver
		secrets []string
		encoded string
	}{
		{
			name:    "s3",
			driver:  DriverS3{Region: "eu-west-1", AccessKeyID: "the-access-key", SecretAccessKey: "the-secret-key"},
			secrets: []string{"the-access-key", "the-secret-key"},
			encoded: `"driver":{"region":"eu-west-1"}`,
		},
		{
			name:    "azure",
			driver:  DriverAzure{AccountName: "theaccount", AccountKey: base64.StdEncoding.EncodeToString([]byte("the-account-key"))},
			secrets: []string{"theaccount", base64.StdEncoding.EncodeToString([]byte("the-account-key"))},
			encoded: `"driver":{}`,
		},
		{
			name:    "google",
			driver:  DriverGoogleCloud{CredentialsJSON: serviceAccountKey(t, "the-service-account@helix.iam.gserviceaccount.com", "https://oauth2.googleapis.com/token"), Endpoint: "http://localhost:4443"},
			secrets: []string{"the-service-account", "private_key"},
			encoded: `"driver":{"endpoint":"http://localhost:4443"}`,
		},
	}

	for _, codec := range jsonCodecs {
		for _, tc := range testcases {
			t.Run(codec.name+"/"+tc.name, func(t *testing.T) {
				encoded, err := codec.marshal(Config{Bucket: "my-bucket", Driver: tc.driver})
				require.NoError(t, err)

				for _, secret := range tc.secrets {
					assert.NotContains(t, string(encoded), secret)
				}

				assert.Contains(t, string(encoded), tc.encoded)
			})
		}
	}
}

// TestConfig_DriverIsNotDecoded pins that a driver cannot come from JSON, with
// either codec: it is set in code, along with the credentials it holds. A Config
// without one decodes.
func TestConfig_DriverIsNotDecoded(t *testing.T) {
	for _, codec := range jsonCodecs {
		t.Run(codec.name, func(t *testing.T) {
			var cfg Config
			assert.Error(t, codec.unmarshal([]byte(`{"bucket":"my-bucket","driver":{"region":"eu-west-1"}}`), &cfg))

			cfg = Config{}
			require.NoError(t, codec.unmarshal([]byte(`{"name":"uploads","bucket":"my-bucket","subfolder":"a/"}`), &cfg))
			assert.Equal(t, Config{Name: "uploads", Bucket: "my-bucket", Subfolder: "a/"}, cfg)
		})
	}
}
