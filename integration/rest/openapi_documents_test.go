package rest

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mountayaapp/helix.go/documents"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refusingTransport fails every request it is handed, and counts them.
type refusingTransport struct {
	attempts atomic.Int32
}

func (rt *refusingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	rt.attempts.Add(1)

	return nil, errors.New("network access is refused in this test")
}

// withoutNetwork makes every request sent through http.DefaultTransport fail for
// the rest of the test, which is the transport the description loader's reader
// uses. The test must not be parallel: the transport is the process's.
func withoutNetwork(t *testing.T) *refusingTransport {
	t.Helper()

	refusing := &refusingTransport{}
	original := http.DefaultTransport
	http.DefaultTransport = refusing
	t.Cleanup(func() { http.DefaultTransport = original })

	return refusing
}

// writeDescription writes a description to a file of its own and returns its path.
func writeDescription(t *testing.T, content string) string {
	t.Helper()

	name := filepath.Join(t.TempDir(), "openapi.yaml")
	require.NoError(t, os.WriteFile(name, []byte(content), 0o600))

	return name
}

// helixResponseStatuses lists the statuses helix ships a response component for.
func helixResponseStatuses(t *testing.T) []string {
	t.Helper()

	entries, err := fs.ReadDir(documents.OpenAPI, "openapi/components/responses")
	require.NoError(t, err)

	statuses := make([]string, 0, len(entries))
	for _, entry := range entries {
		statuses = append(statuses, strings.TrimSuffix(entry.Name(), ".yaml"))
	}

	sort.Strings(statuses)
	require.NotEmpty(t, statuses)

	return statuses
}

// A description that references every response component helix ships, by the raw
// URL a reader of the description resolves, loads with no network at all: each
// document, and every document it references in turn, is read from the copy
// embedded in the module.
func TestLoadOpenAPIDescriptions_HelixDocumentsLoadWithoutTheNetwork(t *testing.T) {
	refusing := withoutNetwork(t)

	refs := []string{
		"https://raw.githubusercontent.com/mountayaapp/helix.go/refs/tags/v0.1.0/documents/openapi/components/responses/%s.yaml",
		"https://raw.githubusercontent.com/mountayaapp/helix.go/refs/heads/main/documents/openapi/components/responses/%s.yaml",
		"https://raw.githubusercontent.com/mountayaapp/helix.go/v9.9.9/documents/openapi/components/responses/%s.yaml",
	}

	for _, ref := range refs {
		t.Run(ref, func(t *testing.T) {
			var responses strings.Builder
			for _, status := range helixResponseStatuses(t) {
				fmt.Fprintf(&responses, "        %q:\n          $ref: %s\n", status, fmt.Sprintf(ref, status))
			}

			description := writeDescription(t, `openapi: 3.0.3
info:
  title: Things
  version: 1.0.0
paths:
  /things:
    get:
      responses:
`+responses.String())

			_, err := NewOpenAPIRouter(t.Context(), description)
			require.NoError(t, err)

			doc, err := LoadOpenAPIDescriptions(t.Context(), description)
			require.NoError(t, err)

			failure := doc.Paths.Value("/things").Get.Responses.Status(http.StatusInternalServerError)
			require.NotNil(t, failure)
			require.NotNil(t, failure.Value.Content.Get("application/json"))
			assert.NotNil(t, failure.Value.Headers["X-Request-Id"], "the header the component references resolves too")
		})
	}

	assert.Zero(t, refusing.attempts.Load(), "no document may be fetched")
}

// A reference to a document this version of helix does not ship is refused at
// load, never fetched instead: the network would be reached for exactly the
// document that differs from what the server writes.
func TestLoadOpenAPIDescriptions_AnUnshippedHelixDocumentIsRefused(t *testing.T) {
	refusing := withoutNetwork(t)

	description := writeDescription(t, `openapi: 3.0.3
info:
  title: Things
  version: 1.0.0
paths:
  /things:
    get:
      responses:
        "418":
          $ref: https://raw.githubusercontent.com/mountayaapp/helix.go/refs/tags/v0.1.0/documents/openapi/components/responses/418.yaml
`)

	_, err := LoadOpenAPIDescriptions(t.Context(), description)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not ship")
	assert.Zero(t, refusing.attempts.Load())
}

// Any other remote reference is still read over the network: only helix's own
// documents are embedded.
func TestLoadOpenAPIDescriptions_OtherRemoteReferencesUseTheNetwork(t *testing.T) {
	refusing := withoutNetwork(t)

	description := writeDescription(t, `openapi: 3.0.3
info:
  title: Things
  version: 1.0.0
paths:
  /things:
    get:
      responses:
        "200":
          $ref: https://raw.githubusercontent.com/someone/else/refs/tags/v0.1.0/documents/openapi/components/responses/200.yaml
`)

	_, err := LoadOpenAPIDescriptions(t.Context(), description)
	require.Error(t, err)
	assert.Equal(t, int32(1), refusing.attempts.Load())
}

func TestHelixDocumentPath(t *testing.T) {
	testcases := []struct {
		name     string
		location string
		path     string
		ok       bool
	}{
		{
			name:     "a tag under refs/tags",
			location: "https://raw.githubusercontent.com/mountayaapp/helix.go/refs/tags/v0.28.0/documents/openapi/components/responses/500.yaml",
			path:     "openapi/components/responses/500.yaml",
			ok:       true,
		},
		{
			name:     "a branch under refs/heads",
			location: "https://raw.githubusercontent.com/mountayaapp/helix.go/refs/heads/main/documents/openapi/components/schemas/Errors.yaml",
			path:     "openapi/components/schemas/Errors.yaml",
			ok:       true,
		},
		{
			name:     "a bare tag or commit",
			location: "https://raw.githubusercontent.com/mountayaapp/helix.go/0771365/documents/openapi/components/headers/X-Request-Id.yaml",
			path:     "openapi/components/headers/X-Request-Id.yaml",
			ok:       true,
		},
		{
			name:     "another repository",
			location: "https://raw.githubusercontent.com/mountayaapp/other/refs/tags/v0.28.0/documents/openapi/components/responses/500.yaml",
		},
		{
			name:     "another host",
			location: "https://example.com/mountayaapp/helix.go/refs/tags/v0.28.0/documents/openapi/components/responses/500.yaml",
		},
		{
			name:     "plain HTTP",
			location: "http://raw.githubusercontent.com/mountayaapp/helix.go/refs/tags/v0.28.0/documents/openapi/components/responses/500.yaml",
		},
		{
			name:     "outside documents",
			location: "https://raw.githubusercontent.com/mountayaapp/helix.go/refs/tags/v0.28.0/go.mod",
		},
		{
			name:     "a reference with no path after it",
			location: "https://raw.githubusercontent.com/mountayaapp/helix.go/refs/tags/v0.28.0",
		},
		{
			name:     "a path climbing out of documents",
			location: "https://raw.githubusercontent.com/mountayaapp/helix.go/refs/tags/v0.28.0/documents/../go.mod",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			location, err := url.Parse(tc.location)
			require.NoError(t, err)

			path, ok := helixDocumentPath(location)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.path, path)
		})
	}
}
