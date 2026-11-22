package bucket

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gocloud.dev/blob"
	"gocloud.dev/blob/fileblob"
)

// cloudCase is one cloud driver as the Connect tests exercise it: the hostile
// environment pointing its provider elsewhere, the fake service standing for it,
// and a valid Config reaching that fake.
type cloudCase struct {
	name    string
	hostile func(t *testing.T)
	fake    func(t *testing.T) *fakeStore
	config  func(t *testing.T, endpoint string) Config
}

// cloudCases returns the three cloud drivers, so that a behaviour every one of
// them shares is pinned for each.
func cloudCases() []cloudCase {
	return []cloudCase{
		{
			name:    "s3",
			hostile: setHostileS3Env,
			fake:    newFakeS3,
			config: func(t *testing.T, endpoint string) Config {
				return Config{Bucket: "my-bucket", Driver: s3Driver(endpoint)}
			},
		},
		{
			name:    "azure",
			hostile: setHostileAzureEnv,
			fake:    newFakeAzure,
			config: func(t *testing.T, endpoint string) Config {
				return Config{Bucket: "my-container", Driver: azureDriver(endpoint + "/configaccount")}
			},
		},
		{
			name:    "google",
			hostile: setHostileGoogleEnv,
			fake:    newFakeGCS,
			config: func(t *testing.T, endpoint string) Config {
				return Config{Bucket: "my-bucket", Driver: googleDriver(t, endpoint)}
			},
		},
	}
}

// captureOpened makes Connect hand every bucket it opens to the returned slice,
// so that a test can tell whether a failed Connect closed it.
func captureOpened(t *testing.T) *[]*blob.Bucket {
	t.Helper()

	var opened []*blob.Bucket
	original := openBucket
	openBucket = func(ctx context.Context, cfg *Config) (*blob.Bucket, io.Closer, error) {
		b, owned, err := original(ctx, cfg)
		if err == nil {
			opened = append(opened, b)
		}

		return b, owned, err
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
// opens nothing, and that no cloud driver takes a credential the environment
// offers in place of one its Config lacks.
func TestConnect_InvalidConfigOpensNothing(t *testing.T) {
	setHostileS3Env(t)
	setHostileAzureEnv(t)
	setHostileGoogleEnv(t)

	original := openBucket
	openBucket = func(ctx context.Context, cfg *Config) (*blob.Bucket, io.Closer, error) {
		t.Errorf("Connect opened %q although its Config is not valid", cfg.Bucket)
		return nil, nil, errors.New("unexpected")
	}
	t.Cleanup(func() { openBucket = original })

	testcases := []struct {
		name  string
		cfg   Config
		paths [][]any
	}{
		{
			name:  "no driver",
			cfg:   Config{Bucket: "my-bucket"},
			paths: [][]any{{"config", "driver"}},
		},
		{
			name:  "injected bucket",
			cfg:   Config{Bucket: "x?endpoint=https://elsewhere.example", Driver: s3Driver("")},
			paths: [][]any{{"config", "bucket"}},
		},
		{
			name: "s3 region and key pair left to the environment",
			cfg:  Config{Bucket: "my-bucket", Driver: DriverS3{}},
			paths: [][]any{
				{"config", "driver", "region"},
				{"config", "driver", "access_key_id"},
				{"config", "driver", "secret_access_key"},
			},
		},
		{
			name: "azure account left to the environment",
			cfg:  Config{Bucket: "my-container", Driver: DriverAzure{}},
			paths: [][]any{
				{"config", "driver", "account_name"},
				{"config", "driver", "account_key"},
			},
		},
		{
			name:  "google key left to the environment",
			cfg:   Config{Bucket: "my-bucket", Driver: DriverGoogleCloud{}},
			paths: [][]any{{"config", "driver", "credentials_json"}},
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
		Bucket: "data/uploads",
		Driver: DriverLocal{},
	})
	require.NoError(t, err)
	require.NoError(t, b.Write(t.Context(), "blob.txt", []byte("hello"), nil))

	content, err := os.ReadFile(filepath.Join(wd, "data", "uploads", "blob.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))
}

// TestConnect_LocalDirectoryNamedLikeAQuery pins that a directory is opened as
// the directory it names, even when its name reads like URL parameters: nothing
// formats it into a URL that fileblob would parse them from.
func TestConnect_LocalDirectoryNamedLikeAQuery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x?base_url=/show&secret_key_path=key")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	svc, _ := servicetest.New(t)

	b, err := Connect(t.Context(), svc, Config{
		Bucket: dir,
		Driver: DriverLocal{},
	})
	require.NoError(t, err)
	require.NoError(t, b.Write(t.Context(), "blob.txt", []byte("hello"), nil))

	content, err := os.ReadFile(filepath.Join(dir, "blob.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))
}

func TestConnect_OpenFailure(t *testing.T) {
	svc, _ := servicetest.New(t)

	_, err := Connect(t.Context(), svc, Config{
		Bucket: filepath.Join(t.TempDir(), "missing"),
		Driver: DriverLocal{},
	})

	assertServiceUnavailable(t, err, "Failed to open bucket")
}

// TestConnect_Unreachable pins, for every cloud driver, that a bucket opened but
// not reachable — missing, or refusing the credentials — fails Connect and is
// closed rather than leaked.
func TestConnect_Unreachable(t *testing.T) {
	for _, cloud := range cloudCases() {
		for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
			t.Run(cloud.name+" "+http.StatusText(status), func(t *testing.T) {
				cloud.hostile(t)

				fake := cloud.fake(t)
				fake.status.Store(int32(status))
				opened := captureOpened(t)

				svc, _ := servicetest.New(t)

				_, err := Connect(t.Context(), svc, cloud.config(t, fake.URL))

				assertServiceUnavailable(t, err, "Failed to reach bucket")
				require.Len(t, *opened, 1)
				assertClosed(t, (*opened)[0])
			})
		}
	}
}

func TestConnect_AttachFailureClosesBucket(t *testing.T) {
	svc, _ := servicetest.New(t)
	cfg := Config{
		Name:   "uploads",
		Bucket: t.TempDir(),
		Driver: DriverLocal{},
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

// TestConnect_S3Endpoint pins what a DriverS3 turns into on the wire: the
// endpoint is the one configured, the bucket is in the path, the region is the
// one signed for, and the access key is the driver's, while every AWS_* variable
// and the shared configuration and credentials files name others.
func TestConnect_S3Endpoint(t *testing.T) {
	setHostileS3Env(t)

	s3 := newFakeS3(t)
	svc, _ := servicetest.New(t)

	_, err := Connect(t.Context(), svc, Config{Bucket: "my-bucket", Driver: s3Driver(s3.URL)})
	require.NoError(t, err)

	requests := s3.requests("/")
	require.NotEmpty(t, requests)
	assert.Equal(t, "/my-bucket", requests[0].path)
	assert.True(t, strings.Contains(requests[0].authorization, "Credential=config-key/"), "Authorization=%q", requests[0].authorization)
	assert.True(t, strings.Contains(requests[0].authorization, "/eu-west-3/s3/"), "Authorization=%q", requests[0].authorization)
}

// TestConnect_AzureEndpoint pins what a DriverAzure turns into on the wire: the
// endpoint is the one configured, account path included, the container follows
// it, and the request is signed with the driver's shared key, while every
// AZURE_STORAGE_* variable names another account, key, SAS token and service.
func TestConnect_AzureEndpoint(t *testing.T) {
	setHostileAzureEnv(t)

	azure := newFakeAzure(t)
	svc, _ := servicetest.New(t)

	_, err := Connect(t.Context(), svc, Config{
		Bucket: "my-container",
		Driver: azureDriver(azure.URL + "/configaccount"),
	})
	require.NoError(t, err)

	requests := azure.requests("/")
	require.NotEmpty(t, requests)
	assert.Equal(t, "/configaccount/my-container", requests[0].path)
	assert.True(t, strings.HasPrefix(requests[0].authorization, "SharedKey configaccount:"), "Authorization=%q", requests[0].authorization)
	assert.NotContains(t, requests[0].query, "sig=", "a SAS token reached the request")
}

// TestConnect_GoogleEndpoint pins what a DriverGoogleCloud turns into on the
// wire: the endpoint is the one configured, the bucket is in the JSON API path,
// and the token is the one issued for the driver's own key, while Application
// Default Credentials, the metadata server and STORAGE_EMULATOR_HOST name others.
// The emulator variable takes the library down another path, so both are run.
func TestConnect_GoogleEndpoint(t *testing.T) {
	for _, emulator := range []bool{true, false} {
		name := "emulator variable set"
		if !emulator {
			name = "emulator variable unset"
		}

		t.Run(name, func(t *testing.T) {
			setHostileGoogleEnv(t)
			if !emulator {
				t.Setenv("STORAGE_EMULATOR_HOST", "")
			}

			gcs := newFakeGCS(t)
			svc, _ := servicetest.New(t)

			_, err := Connect(t.Context(), svc, Config{Bucket: "my-bucket", Driver: googleDriver(t, gcs.URL)})
			require.NoError(t, err)

			requests := gcs.requests("/storage/v1/")
			require.NotEmpty(t, requests)
			assert.Equal(t, "/storage/v1/b/my-bucket/o", requests[0].path)
			assert.Equal(t, "Bearer token-for-"+configServiceAccount, requests[0].authorization)
		})
	}
}

// TestConnect_SubfolderRoot pins the documented meaning of a "/" subfolder — the
// bucket root — on a driver where it shows: S3 keys "/key" and "key" are two
// different objects, and the first is what "/" used to produce.
func TestConnect_SubfolderRoot(t *testing.T) {
	setHostileS3Env(t)

	s3 := newFakeS3(t)
	svc, _ := servicetest.New(t)

	b, err := Connect(t.Context(), svc, Config{
		Bucket:    "my-bucket",
		Subfolder: "/",
		Driver:    s3Driver(s3.URL),
	})
	require.NoError(t, err)

	exists, err := b.Exists(t.Context(), "key")
	require.NoError(t, err)
	assert.False(t, exists)

	requests := s3.requests("/")
	assert.Equal(t, "/my-bucket/key", requests[len(requests)-1].path)
}

func TestDependency_CheckHealth(t *testing.T) {
	setHostileS3Env(t)

	s3 := newFakeS3(t)
	svc, _ := servicetest.New(t)

	b, err := Connect(t.Context(), svc, Config{Bucket: "my-bucket", Driver: s3Driver(s3.URL)})
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
		Bucket: t.TempDir(),
		Driver: DriverLocal{},
	})
	require.NoError(t, err)

	dep := dependency{conn: b.(*connection)}
	assert.NoError(t, dep.Close(t.Context()))
	assert.NoError(t, dep.Close(t.Context()))

	// The Service closes it a third time when the test ends, which servicetest
	// fails the test over if it errors.
}

// closeCounter stands for what a driver opens beside its bucket, counting how
// often it is closed.
type closeCounter struct {
	calls atomic.Int32
	err   error
}

// Close counts the call and returns the error the test set.
func (c *closeCounter) Close() error {
	c.calls.Add(1)
	return c.err
}

// TestConnection_CloseReleasesOwned pins that what a driver opens beside its
// bucket — the Cloud Storage client — is closed with the connection: once, after
// the bucket, however often close runs, its error reported, and on a failed
// Connect as well.
func TestConnection_CloseReleasesOwned(t *testing.T) {
	t.Run("close", func(t *testing.T) {
		b, err := fileblob.OpenBucket(t.TempDir(), nil)
		require.NoError(t, err)

		owned := &closeCounter{err: errors.New("owned failed to close")}
		conn := &connection{client: b, owned: owned}

		assert.ErrorIs(t, conn.close(), owned.err)
		assert.NoError(t, conn.close())

		assertClosed(t, b)
		assert.Equal(t, int32(1), owned.calls.Load())
	})

	t.Run("failed connect", func(t *testing.T) {
		setHostileS3Env(t)

		s3 := newFakeS3(t)
		s3.status.Store(http.StatusForbidden)

		owned := &closeCounter{}
		opened := captureOpened(t)

		withOwned := openBucket
		openBucket = func(ctx context.Context, cfg *Config) (*blob.Bucket, io.Closer, error) {
			b, _, err := withOwned(ctx, cfg)
			return b, owned, err
		}

		svc, _ := servicetest.New(t)

		_, err := Connect(t.Context(), svc, Config{Bucket: "my-bucket", Driver: s3Driver(s3.URL)})

		assertServiceUnavailable(t, err, "Failed to reach bucket")
		require.Len(t, *opened, 1)
		assertClosed(t, (*opened)[0])
		assert.Equal(t, int32(1), owned.calls.Load())
	})
}
