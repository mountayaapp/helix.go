package bucket

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/mountaya/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"gocloud.dev/gcerrors"
)

// connectLocal connects a Bucket on a fresh temporary directory, with the
// telemetry recorded.
func connectLocal(t *testing.T, cfg Config) (Bucket, *servicetest.Recorder) {
	t.Helper()

	svc, rec := servicetest.New(t)

	cfg.Driver = DriverLocal{}
	cfg.Bucket = t.TempDir()

	b, err := Connect(t.Context(), svc, cfg)
	require.NoError(t, err)

	return b, rec
}

// spanNamed returns the one ended span called name.
func spanNamed(t *testing.T, rec *servicetest.Recorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()

	var found []sdktrace.ReadOnlySpan
	for _, span := range rec.Spans() {
		if span.Name() == name {
			found = append(found, span)
		}
	}

	require.Len(t, found, 1, "want exactly one ended span %q", name)
	return found[0]
}

// outcomes returns "<operation>=<outcome>" for every series of the operation
// duration histogram, sorted.
func outcomes(t *testing.T, rec *servicetest.Recorder) []string {
	t.Helper()

	var out []string
	for _, m := range rec.Metrics() {
		if m.Name != "bucket.operation.duration" {
			continue
		}

		histogram, ok := m.Data.(metricdata.Histogram[float64])
		require.True(t, ok, "bucket.operation.duration is a %T", m.Data)

		for _, point := range histogram.DataPoints {
			operation, _ := point.Attributes.Value("bucket.operation")
			outcome, _ := point.Attributes.Value("bucket.outcome")
			out = append(out, operation.AsString()+"="+outcome.AsString())
		}
	}

	slices.Sort(out)
	return out
}

// TestNotFound pins that a missing blob is an answer rather than a failure: the
// error is returned, marked as ErrNotFound with the driver's error still in the
// chain, and the operation is neither a span error nor an "error" outcome.
func TestNotFound(t *testing.T) {
	b, rec := connectLocal(t, Config{})

	_, err := b.Read(t.Context(), "missing")
	assert.True(t, IsNotFound(err), "Read(missing) err=%v", err)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t, gcerrors.NotFound, gcerrors.Code(err))

	_, err = b.NewReader(t.Context(), "missing")
	assert.True(t, IsNotFound(err), "NewReader(missing) err=%v", err)

	err = b.Delete(t.Context(), "missing")
	assert.True(t, IsNotFound(err), "Delete(missing) err=%v", err)

	exists, err := b.Exists(t.Context(), "missing")
	assert.NoError(t, err)
	assert.False(t, exists)

	for _, name := range []string{"Bucket: Read", "Bucket: NewReader", "Bucket: Delete", "Bucket: Exists"} {
		span := spanNamed(t, rec, name)
		assert.Equal(t, codes.Unset, span.Status().Code, "%s status", name)
		assert.Empty(t, span.Events(), "%s events", name)
	}

	assert.Equal(t, []string{"Delete=ok", "Exists=ok", "NewReader=ok", "Read=ok"}, outcomes(t, rec))
}

// TestFailureIsRecorded is the counterpart of TestNotFound: an error that is not
// a missing blob is recorded on the span and measured as an "error" outcome.
func TestFailureIsRecorded(t *testing.T) {
	b, rec := connectLocal(t, Config{})

	// A closed bucket fails every operation, with an error that is not a missing
	// blob.
	require.NoError(t, dependency{conn: b.(*connection)}.Close(t.Context()))

	_, err := b.Read(t.Context(), "key")
	require.Error(t, err)
	assert.False(t, IsNotFound(err))

	_, err = b.Exists(t.Context(), "key")
	require.Error(t, err)

	for _, name := range []string{"Bucket: Read", "Bucket: Exists"} {
		span := spanNamed(t, rec, name)
		assert.Equal(t, codes.Error, span.Status().Code, "%s status", name)
	}

	assert.Equal(t, []string{"Exists=error", "Read=error"}, outcomes(t, rec))
}

func TestIsNotFound(t *testing.T) {
	assert.False(t, IsNotFound(nil))
	assert.False(t, IsNotFound(errors.New("blob not found")))
	assert.True(t, IsNotFound(ErrNotFound))
	assert.NoError(t, asNotFound(nil))
	assert.NoError(t, asNotFound(errors.New("other")))
}

// TestStream pins that a streamed read or write is one operation lasting until
// Close, and that a blob written through NewWriter reads back through NewReader.
func TestStream(t *testing.T) {
	b, rec := connectLocal(t, Config{Subfolder: "streams"})

	w, err := b.NewWriter(t.Context(), "big.txt", &OptionsWrite{ContentType: "text/plain"})
	require.NoError(t, err)

	_, err = io.Copy(w, strings.NewReader(strings.Repeat("a", 64*1024)))
	require.NoError(t, err)

	assert.Empty(t, rec.Spans(), "the write is still open, so its span must be too")
	require.NoError(t, w.Close())
	assert.NoError(t, w.Close(), "a second Close must be a no-op")

	r, err := b.NewReader(t.Context(), "big.txt")
	require.NoError(t, err)

	content, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Len(t, content, 64*1024)

	assert.Len(t, rec.Spans(), 1, "the read is still open, so its span must be too")
	require.NoError(t, r.Close())
	assert.NoError(t, r.Close(), "a second Close must be a no-op")

	for _, name := range []string{"Bucket: NewWriter", "Bucket: NewReader"} {
		span := spanNamed(t, rec, name)
		assert.Equal(t, codes.Unset, span.Status().Code, "%s status", name)
		assert.Contains(t, span.Attributes(), attribute.String("bucket.key", "big.txt"))
		assert.Contains(t, span.Attributes(), attribute.String("bucket.subfolder", "streams/"))
	}

	assert.Equal(t, []string{"NewReader=ok", "NewWriter=ok"}, outcomes(t, rec))
}

// TestStream_AbandonedWrite pins the documented way to abandon a streamed write:
// cancel ctx, then Close. Nothing is written, and the operation is recorded as
// the failure it is.
func TestStream_AbandonedWrite(t *testing.T) {
	b, rec := connectLocal(t, Config{})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	w, err := b.NewWriter(ctx, "partial.txt", &OptionsWrite{ContentType: "text/plain"})
	require.NoError(t, err)

	_, err = w.Write([]byte("half of it"))
	require.NoError(t, err)

	cancel()
	assert.ErrorIs(t, w.Close(), context.Canceled)

	exists, err := b.Exists(t.Context(), "partial.txt")
	require.NoError(t, err)
	assert.False(t, exists, "an abandoned write must not commit what was written")

	assert.Equal(t, codes.Error, spanNamed(t, rec, "Bucket: NewWriter").Status().Code)
}

// TestSubfolder pins that keys land under the normalized subfolder. Whether "/"
// adds a leading slash cannot show on a filesystem, where "/key" and "key" are
// the same file: TestConnect_SubfolderRoot checks that on S3.
func TestSubfolder(t *testing.T) {
	testcases := []struct {
		subfolder string
		path      string
	}{
		{subfolder: "", path: "key"},
		{subfolder: "/", path: "key"},
		{subfolder: "a/b", path: "a/b/key"},
		{subfolder: "/a/b/", path: "a/b/key"},
	}

	for _, tc := range testcases {
		t.Run(tc.subfolder, func(t *testing.T) {
			svc, _ := servicetest.New(t)
			dir := t.TempDir()

			b, err := Connect(t.Context(), svc, Config{
				Driver:    DriverLocal{},
				Bucket:    dir,
				Subfolder: tc.subfolder,
			})
			require.NoError(t, err)
			require.NoError(t, b.Write(t.Context(), "key", []byte("v"), nil))

			root, err := Connect(t.Context(), svc, Config{
				Name:   "root",
				Driver: DriverLocal{},
				Bucket: dir,
			})
			require.NoError(t, err)

			exists, err := root.Exists(t.Context(), tc.path)
			require.NoError(t, err)
			assert.True(t, exists, "want the blob at %q", tc.path)
		})
	}
}

// TestMetrics pins the operation duration histogram: one series per operation
// and outcome, keyed on the connection's name rather than on anything with the
// cardinality of a key.
func TestMetrics(t *testing.T) {
	b, rec := connectLocal(t, Config{Name: "uploads"})

	require.NoError(t, b.Write(t.Context(), "a", []byte("1"), nil))
	require.NoError(t, b.Write(t.Context(), "b", []byte("2"), nil))
	_, err := b.Read(t.Context(), "a")
	require.NoError(t, err)

	var found bool
	for _, m := range rec.Metrics() {
		if m.Name != "bucket.operation.duration" {
			continue
		}

		found = true
		assert.Equal(t, "s", m.Unit)

		histogram := m.Data.(metricdata.Histogram[float64])
		for _, point := range histogram.DataPoints {
			var keys []string
			for _, kv := range point.Attributes.ToSlice() {
				keys = append(keys, string(kv.Key))
			}

			assert.Equal(t, []string{"bucket.name", "bucket.operation", "bucket.outcome"}, keys)

			name, _ := point.Attributes.Value("bucket.name")
			assert.Equal(t, "uploads", name.AsString())

			operation, _ := point.Attributes.Value("bucket.operation")
			want := map[string]uint64{"Write": 2, "Read": 1}[operation.AsString()]
			assert.Equal(t, want, point.Count, "count of %s", operation.AsString())
		}
	}

	assert.True(t, found, "bucket.operation.duration was not recorded")
	assert.Equal(t, []string{"Read=ok", "Write=ok"}, outcomes(t, rec))
}
