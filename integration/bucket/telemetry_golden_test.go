package bucket

import (
	"io"
	"strings"
	"testing"

	"github.com/mountaya/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTelemetryGolden pins what this integration emits, so that changing how a
// span is produced cannot silently change what is produced. It records span
// names, emitting scope, kind and attribute keys - never values, timings or IDs,
// which differ on every run.
//
// A diff here is not automatically a failure: it is a change to the library's
// observable output, and it has to be an intentional one. Update the golden in
// the same commit that changes the instrumentation, never separately.
func TestTelemetryGolden(t *testing.T) {
	svc, rec := servicetest.New(t)

	b, err := Connect(t.Context(), svc, Config{
		Name:      "golden",
		Driver:    DriverLocal{},
		Bucket:    t.TempDir(),
		Subfolder: "prefix/",
	})
	require.NoError(t, err)

	require.NoError(t, b.Write(t.Context(), "a/b.txt", []byte("hello"), nil))
	_, err = b.Read(t.Context(), "a/b.txt")
	require.NoError(t, err)

	w, err := b.NewWriter(t.Context(), "a/c.txt", nil)
	require.NoError(t, err)
	_, err = w.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	r, err := b.NewReader(t.Context(), "a/c.txt")
	require.NoError(t, err)
	_, err = io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())

	exists, err := b.Exists(t.Context(), "a/b.txt")
	require.NoError(t, err)
	assert.True(t, exists)
	require.NoError(t, b.Delete(t.Context(), "a/b.txt"))

	var got []string
	for _, s := range servicetest.Summarize(rec.Spans()) {
		got = append(got, s.String())
	}

	// Six operations, six spans, and none for Connect's own check that the bucket
	// can be listed. gocloud.dev/blob emits its own span for each operation
	// through the global tracer provider - carrying a single gocdk_method
	// attribute, in a vocabulary nothing else here uses - and the Service
	// suppresses that scope, so one operation means one span. Before the
	// suppression this golden held twice as many.
	//
	// The streamed operations end when their reader or writer is closed.
	want := []string{
		"github.com/mountaya/helix.go | client | Bucket: Write | bucket.bucket,bucket.driver,bucket.key,bucket.name,bucket.subfolder",
		"github.com/mountaya/helix.go | client | Bucket: Read | bucket.bucket,bucket.driver,bucket.key,bucket.name,bucket.subfolder",
		"github.com/mountaya/helix.go | client | Bucket: NewWriter | bucket.bucket,bucket.driver,bucket.key,bucket.name,bucket.subfolder",
		"github.com/mountaya/helix.go | client | Bucket: NewReader | bucket.bucket,bucket.driver,bucket.key,bucket.name,bucket.subfolder",
		"github.com/mountaya/helix.go | client | Bucket: Exists | bucket.bucket,bucket.driver,bucket.key,bucket.name,bucket.subfolder",
		"github.com/mountaya/helix.go | client | Bucket: Delete | bucket.bucket,bucket.driver,bucket.key,bucket.name,bucket.subfolder",
	}

	assert.Equal(t, want, got)

	// The suppression is what the span count above depends on, so it is asserted
	// directly rather than left implied: no gocloud scope may reach the exporter.
	assert.Equal(t, []string{"github.com/mountaya/helix.go"}, servicetest.Scopes(rec.Spans()))

	servicetest.AssertConvention(t, rec.Spans())

	// One histogram in the integration's namespace. gocloud.dev/blob records
	// metrics of its own through the global meter provider, which the Service
	// does not filter, so only this namespace is pinned.
	var metrics []string
	for _, m := range rec.Metrics() {
		if strings.HasPrefix(m.Name, identifier+".") {
			metrics = append(metrics, m.Name)
		}
	}

	assert.Equal(t, []string{"bucket.operation.duration"}, metrics)
}
