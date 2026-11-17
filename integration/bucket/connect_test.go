package bucket

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gocloud.dev/blob"
)

// fakeS3 is an S3-compatible endpoint answering the one request Connect and
// CheckHealth make — listing the bucket — with the status the test sets, and
// recording what it was asked. It holds no blob.
type fakeS3 struct {
	*httptest.Server

	// status is what the next listing answers: 200, or the error the test wants
	// the driver to see.
	status atomic.Int32

	mu       sync.Mutex
	paths    []string
	authKeys []string
}

// newFakeS3 starts a fakeS3 answering 200, stopped when the test ends.
func newFakeS3(t *testing.T) *fakeS3 {
	t.Helper()

	s3 := &fakeS3{}
	s3.status.Store(http.StatusOK)
	s3.Server = httptest.NewServer(http.HandlerFunc(s3.serve))
	t.Cleanup(s3.Close)

	return s3
}

// serve records the request and answers it.
func (s3 *fakeS3) serve(rw http.ResponseWriter, req *http.Request) {
	s3.mu.Lock()
	s3.paths = append(s3.paths, req.URL.Path)
	s3.authKeys = append(s3.authKeys, req.Header.Get("Authorization"))
	s3.mu.Unlock()

	// A HEAD is Exists asking for a blob, which the fake never holds.
	if req.Method == http.MethodHead {
		rw.WriteHeader(http.StatusNotFound)
		return
	}

	rw.Header().Set("Content-Type", "application/xml")

	status := int(s3.status.Load())
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
}

// requests returns the paths requested and the Authorization headers sent.
func (s3 *fakeS3) requests() ([]string, []string) {
	s3.mu.Lock()
	defer s3.mu.Unlock()

	return append([]string(nil), s3.paths...), append([]string(nil), s3.authKeys...)
}

// setHostileAWSEnv sets every source the AWS SDK's default chain reads
// credentials, a region and an endpoint from — the AWS_* variables, a profile,
// and the shared configuration and credentials files — to values of their own,
// which no Config of a test names. The endpoint they name is a decoy failing the
// test on any request it receives: DriverAWS must follow none of them.
func setHostileAWSEnv(t *testing.T) {
	t.Helper()

	decoy := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		t.Errorf("a request reached the endpoint the environment names: %s %s", req.Method, req.URL.Path)
		rw.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(decoy.Close)

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

// awsConfig returns a valid DriverAWS Config of the bucket "my-bucket" behind
// endpoint, signed for eu-west-3 with the key pair "config-key".
func awsConfig(endpoint string) Config {
	return Config{
		Driver:          DriverAWS,
		Bucket:          "my-bucket",
		Region:          "eu-west-3",
		Endpoint:        endpoint,
		AccessKeyID:     "config-key",
		SecretAccessKey: "config-secret",
	}
}

// captureOpened makes Connect hand every bucket it opens to the returned slice,
// so that a test can tell whether a failed Connect closed it.
func captureOpened(t *testing.T) *[]*blob.Bucket {
	t.Helper()

	var opened []*blob.Bucket
	original := openBucket
	openBucket = func(ctx context.Context, cfg *Config) (*blob.Bucket, error) {
		b, err := original(ctx, cfg)
		if err == nil {
			opened = append(opened, b)
		}

		return b, err
	}

	t.Cleanup(func() { openBucket = original })

	return &opened
}

// assertClosed fails the test unless b was already closed: the driver reports
// closing a bucket twice as an error.
func assertClosed(t *testing.T, b *blob.Bucket) {
	t.Helper()

	assert.Error(t, b.Close(), "the bucket must already have been closed")
}

// assertServiceUnavailable fails the test unless err is a single
// SERVICE_UNAVAILABLE entry with message, whose reason is set.
func assertServiceUnavailable(t *testing.T, err error, message string) {
	t.Helper()

	entries := errorstack.EntriesOf(err)
	require.Len(t, entries, 1, "err=%v", err)
	assert.Equal(t, message, entries[0].Message)
	assert.Equal(t, errorstack.CodeServiceUnavailable, entries[0].Extensions["code"])
	assert.NotEmpty(t, entries[0].Extensions["reason"])
}

// TestConnect_InvalidConfigOpensNothing pins that a Config that is not valid
// opens nothing, and that DriverAWS takes no region or key pair the environment
// offers in place of the ones its Config lacks.
func TestConnect_InvalidConfigOpensNothing(t *testing.T) {
	setHostileAWSEnv(t)

	original := openBucket
	openBucket = func(ctx context.Context, cfg *Config) (*blob.Bucket, error) {
		t.Errorf("Connect opened %q although its Config is not valid", cfg.Bucket)
		return nil, errors.New("unexpected")
	}
	t.Cleanup(func() { openBucket = original })

	testcases := []struct {
		name  string
		cfg   Config
		paths [][]any
	}{
		{
			name: "injected bucket",
			cfg: Config{
				Driver:          DriverAWS,
				Bucket:          "x?endpoint=https://elsewhere.example",
				Region:          "us-east-1",
				AccessKeyID:     "config-key",
				SecretAccessKey: "config-secret",
			},
			paths: [][]any{{"config", "bucket"}},
		},
		{
			name: "region and key pair left to the environment",
			cfg:  Config{Driver: DriverAWS, Bucket: "my-bucket"},
			paths: [][]any{
				{"config", "region"},
				{"config", "access_key_id"},
				{"config", "secret_access_key"},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := servicetest.New(t)

			_, err := Connect(t.Context(), svc, tc.cfg)

			entries := errorstack.EntriesOf(err)
			require.Len(t, entries, len(tc.paths))
			for i, entry := range entries {
				assert.Equal(t, tc.paths[i], entry.Path)
				assert.Equal(t, errorstack.CodeValidationFailed, entry.Extensions["code"])
			}
		})
	}
}

// TestConnect_LocalRelativeDirectory pins the regression end to end: a relative
// directory is opened under the working directory, and the blob written lands
// there rather than under the filesystem root.
func TestConnect_LocalRelativeDirectory(t *testing.T) {
	wd := t.TempDir()
	t.Chdir(wd)
	require.NoError(t, os.MkdirAll(filepath.Join("data", "uploads"), 0o755))

	svc, _ := servicetest.New(t)

	b, err := Connect(t.Context(), svc, Config{
		Driver: DriverLocal,
		Bucket: "data/uploads",
	})
	require.NoError(t, err)
	require.NoError(t, b.Write(t.Context(), "blob.txt", []byte("hello"), nil))

	content, err := os.ReadFile(filepath.Join(wd, "data", "uploads", "blob.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))
}

func TestConnect_OpenFailure(t *testing.T) {
	svc, _ := servicetest.New(t)

	_, err := Connect(t.Context(), svc, Config{
		Driver: DriverLocal,
		Bucket: filepath.Join(t.TempDir(), "missing"),
	})

	assertServiceUnavailable(t, err, "Failed to open bucket")
}

func TestConnect_Unreachable(t *testing.T) {
	testcases := []struct {
		name   string
		status int
	}{
		{name: "missing bucket", status: http.StatusNotFound},
		{name: "refused credentials", status: http.StatusForbidden},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			setHostileAWSEnv(t)

			s3 := newFakeS3(t)
			s3.status.Store(int32(tc.status))
			opened := captureOpened(t)

			svc, _ := servicetest.New(t)

			_, err := Connect(t.Context(), svc, awsConfig(s3.URL))

			assertServiceUnavailable(t, err, "Failed to reach bucket")
			require.Len(t, *opened, 1)
			assertClosed(t, (*opened)[0])
		})
	}
}

func TestConnect_AttachFailureClosesBucket(t *testing.T) {
	svc, _ := servicetest.New(t)
	cfg := Config{
		Name:   "uploads",
		Driver: DriverLocal,
		Bucket: t.TempDir(),
	}

	_, err := Connect(t.Context(), svc, cfg)
	require.NoError(t, err)

	opened := captureOpened(t)

	_, err = Connect(t.Context(), svc, cfg)

	entries := errorstack.EntriesOf(err)
	require.Len(t, entries, 1)
	assert.Equal(t, []any{"dependency", "uploads"}, entries[0].Path)
	require.Len(t, *opened, 1)
	assertClosed(t, (*opened)[0])
}

// TestConnect_AWSEndpoint pins what a DriverAWS Config turns into on the wire:
// the endpoint is the one configured, the bucket is in the path, the region is
// the one signed for, and the access key is the Config's, while every AWS_*
// variable and the shared configuration and credentials files name others.
func TestConnect_AWSEndpoint(t *testing.T) {
	setHostileAWSEnv(t)

	s3 := newFakeS3(t)
	svc, _ := servicetest.New(t)

	_, err := Connect(t.Context(), svc, awsConfig(s3.URL))
	require.NoError(t, err)

	paths, auths := s3.requests()
	require.NotEmpty(t, paths)
	assert.Equal(t, "/my-bucket", paths[0])
	assert.True(t, strings.Contains(auths[0], "Credential=config-key/"), "Authorization=%q", auths[0])
	assert.True(t, strings.Contains(auths[0], "/eu-west-3/s3/"), "Authorization=%q", auths[0])
}

// TestConnect_SubfolderRoot pins the documented meaning of a "/" subfolder — the
// bucket root — on a driver where it shows: S3 keys "/key" and "key" are two
// different objects, and the first is what "/" used to produce.
func TestConnect_SubfolderRoot(t *testing.T) {
	setHostileAWSEnv(t)

	s3 := newFakeS3(t)
	svc, _ := servicetest.New(t)

	cfg := awsConfig(s3.URL)
	cfg.Subfolder = "/"

	b, err := Connect(t.Context(), svc, cfg)
	require.NoError(t, err)

	exists, err := b.Exists(t.Context(), "key")
	require.NoError(t, err)
	assert.False(t, exists)

	paths, _ := s3.requests()
	assert.Equal(t, "/my-bucket/key", paths[len(paths)-1])
}

func TestDependency_CheckHealth(t *testing.T) {
	setHostileAWSEnv(t)

	s3 := newFakeS3(t)
	svc, _ := servicetest.New(t)

	b, err := Connect(t.Context(), svc, awsConfig(s3.URL))
	require.NoError(t, err)

	dep := dependency{conn: b.(*connection)}
	assert.NoError(t, dep.CheckHealth(t.Context()))

	s3.status.Store(http.StatusForbidden)
	err = dep.CheckHealth(t.Context())

	entries := errorstack.EntriesOf(err)
	require.Len(t, entries, 1)
	assert.Equal(t, "Integration is not in a healthy state", entries[0].Message)
	assert.Equal(t, errorstack.CodeServiceUnavailable, entries[0].Extensions["code"])

	s3.status.Store(http.StatusNotFound)
	err = dep.CheckHealth(t.Context())
	assert.ErrorIs(t, err, errNotAccessible)
}

// TestDependency_CloseIsIdempotent pins that closing twice is not an error: the
// driver reports a second Close as one, which would fail a Service's shutdown
// after anything else had closed the bucket first.
func TestDependency_CloseIsIdempotent(t *testing.T) {
	svc, _ := servicetest.New(t)

	b, err := Connect(t.Context(), svc, Config{
		Driver: DriverLocal,
		Bucket: t.TempDir(),
	})
	require.NoError(t, err)

	dep := dependency{conn: b.(*connection)}
	assert.NoError(t, dep.Close(t.Context()))
	assert.NoError(t, dep.Close(t.Context()))

	// The Service closes it a third time when the test ends, which servicetest
	// fails the test over if it errors.
}
