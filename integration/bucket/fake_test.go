package bucket

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeRequest is what a fakeStore records of a request it answered.
type fakeRequest struct {
	method        string
	path          string
	query         string
	authorization string
}

// fakeStore is an httptest server standing for a storage service. It records
// every request, and answers each with what the provider's respond function
// writes for the status the test sets: 200, or the error the test wants the driver
// to see. It holds no blob.
type fakeStore struct {
	*httptest.Server

	// status is what the next listing answers.
	status atomic.Int32

	mu       sync.Mutex
	received []fakeRequest
}

// newFakeStore starts a fakeStore answering through respond with 200, stopped
// when the test ends.
func newFakeStore(t *testing.T, respond func(rw http.ResponseWriter, req *http.Request, status int)) *fakeStore {
	t.Helper()

	store := &fakeStore{}
	store.status.Store(http.StatusOK)
	store.Server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		store.mu.Lock()
		store.received = append(store.received, fakeRequest{
			method:        req.Method,
			path:          req.URL.Path,
			query:         req.URL.RawQuery,
			authorization: req.Header.Get("Authorization"),
		})
		store.mu.Unlock()

		respond(rw, req, int(store.status.Load()))
	}))
	t.Cleanup(store.Close)

	return store
}

// requests returns the requests answered so far whose path starts with prefix.
func (store *fakeStore) requests(prefix string) []fakeRequest {
	store.mu.Lock()
	defer store.mu.Unlock()

	var found []fakeRequest
	for _, req := range store.received {
		if strings.HasPrefix(req.path, prefix) {
			found = append(found, req)
		}
	}

	return found
}

// newDecoy starts a server standing for wherever a hostile environment points a
// provider: the endpoint, the token service or the metadata server it names. Any
// request reaching it fails the test, since a driver must follow nothing the
// environment names.
func newDecoy(t *testing.T) *httptest.Server {
	t.Helper()

	decoy := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		t.Errorf("a request reached the endpoint the environment names: %s %s", req.Method, req.URL.Path)
		rw.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(decoy.Close)

	return decoy
}
