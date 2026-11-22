package bucket

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// newFakeS3 starts an S3-compatible fakeStore answering the requests Connect,
// CheckHealth and Exists make: listing the bucket, and asking for a blob, which it
// never holds.
func newFakeS3(t *testing.T) *fakeStore {
	t.Helper()

	return newFakeStore(t, func(rw http.ResponseWriter, req *http.Request, status int) {
		// A HEAD is Exists asking for a blob, which the fake never holds.
		if req.Method == http.MethodHead {
			rw.WriteHeader(http.StatusNotFound)
			return
		}

		rw.Header().Set("Content-Type", "application/xml")

		switch status {
		case http.StatusOK:
			fmt.Fprint(rw, `<?xml version="1.0" encoding="UTF-8"?>`+
				`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`+
				`<Name>my-bucket</Name><KeyCount>0</KeyCount><MaxKeys>1</MaxKeys><IsTruncated>false</IsTruncated>`+
				`</ListBucketResult>`)
		case http.StatusNotFound:
			rw.WriteHeader(status)
			fmt.Fprint(rw, `<Error><Code>NoSuchBucket</Code><Message>The specified bucket does not exist</Message></Error>`)
		default:
			rw.WriteHeader(status)
			fmt.Fprint(rw, `<Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`)
		}
	})
}

// setHostileS3Env sets every source the AWS SDK's default chain reads
// credentials, a region and an endpoint from — the AWS_* variables, a profile,
// and the shared configuration and credentials files — to values of their own,
// which no driver of a test names. The endpoint they name is a decoy: DriverS3
// must follow none of them.
func setHostileS3Env(t *testing.T) {
	t.Helper()

	decoy := newDecoy(t)

	dir := t.TempDir()
	config := filepath.Join(dir, "config")
	credentials := filepath.Join(dir, "credentials")

	require.NoError(t, os.WriteFile(config, []byte(
		"[default]\nregion = ap-south-1\nendpoint_url = "+decoy.URL+"\n"+
			"[profile hostile]\nregion = ap-south-1\nendpoint_url = "+decoy.URL+"\n"), 0o600))
	require.NoError(t, os.WriteFile(credentials, []byte(
		"[default]\naws_access_key_id = from-file\naws_secret_access_key = from-file\n"+
			"[hostile]\naws_access_key_id = from-file\naws_secret_access_key = from-file\n"), 0o600))

	for key, value := range map[string]string{
		"AWS_ACCESS_KEY_ID":           "from-env",
		"AWS_SECRET_ACCESS_KEY":       "from-env",
		"AWS_SESSION_TOKEN":           "from-env",
		"AWS_REGION":                  "us-west-2",
		"AWS_DEFAULT_REGION":          "us-west-2",
		"AWS_PROFILE":                 "hostile",
		"AWS_ENDPOINT_URL":            decoy.URL,
		"AWS_ENDPOINT_URL_S3":         decoy.URL,
		"AWS_CONFIG_FILE":             config,
		"AWS_SHARED_CREDENTIALS_FILE": credentials,
		"AWS_EC2_METADATA_DISABLED":   "true",
	} {
		t.Setenv(key, value)
	}
}

// s3Driver returns a valid DriverS3 reaching endpoint — the provider's own
// when it is empty — signed for eu-west-3 with the key pair "config-key".
func s3Driver(endpoint string) DriverS3 {
	return DriverS3{
		Region:          "eu-west-3",
		Endpoint:        endpoint,
		AccessKeyID:     "config-key",
		SecretAccessKey: "config-secret",
	}
}
