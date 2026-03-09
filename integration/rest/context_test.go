package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/uptrace/bunrouter"
)

func TestParamsFromContext(t *testing.T) {
	testcases := []struct {
		name       string
		registered string
		requested  string
		expected   map[string]string
		found      bool
	}{
		{
			name:       "no params",
			registered: "/hello",
			requested:  "/hello",
			expected:   map[string]string{},
			found:      false,
		},
		{
			name:       "with URL params",
			registered: "/users/:username",
			requested:  "/users/mountayaapp",
			expected: map[string]string{
				"username": "mountayaapp",
			},
			found: true,
		},
		{
			name:       "multiple URL params",
			registered: "/orgs/:org_id/users/:user_id",
			requested:  "/orgs/org_123/users/usr_456",
			expected: map[string]string{
				"org_id":  "org_123",
				"user_id": "usr_456",
			},
			found: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			router := bunrouter.New().Compat()
			router.GET(tc.registered, func(rw http.ResponseWriter, req *http.Request) {
				params, found := ParamsFromContext(req.Context())

				assert.Equal(t, tc.expected, params)
				assert.Equal(t, tc.found, found)
			})

			rw := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodGet, tc.requested, nil)

			router.ServeHTTP(rw, req)
		})
	}
}

func TestParamsFromContext_NoRouterContext(t *testing.T) {
	params, found := ParamsFromContext(t.Context())

	assert.Empty(t, params)
	assert.False(t, found)
}
