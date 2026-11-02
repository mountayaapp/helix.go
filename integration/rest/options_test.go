package rest

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRouteOptions(t *testing.T) {
	testcases := []struct {
		name     string
		config   time.Duration
		opts     []RouteOption
		expected time.Duration
	}{
		{
			name:     "no options falls back to the config default",
			config:   30 * time.Second,
			opts:     nil,
			expected: 30 * time.Second,
		},
		{
			name:     "no options and no default leaves the route unbounded",
			config:   0,
			opts:     nil,
			expected: 0,
		},
		{
			name:     "WithTimeout overrides the config default upwards",
			config:   30 * time.Second,
			opts:     []RouteOption{WithTimeout(55 * time.Second)},
			expected: 55 * time.Second,
		},
		{
			name:     "WithTimeout overrides the config default downwards",
			config:   30 * time.Second,
			opts:     []RouteOption{WithTimeout(2 * time.Second)},
			expected: 2 * time.Second,
		},
		{
			name:     "WithoutTimeout waives the config default",
			config:   30 * time.Second,
			opts:     []RouteOption{WithoutTimeout()},
			expected: 0,
		},
		{
			name:     "the last option wins",
			config:   30 * time.Second,
			opts:     []RouteOption{WithoutTimeout(), WithTimeout(5 * time.Second)},
			expected: 5 * time.Second,
		},
		{
			name:     "a nil option is skipped",
			config:   30 * time.Second,
			opts:     []RouteOption{nil, WithTimeout(5 * time.Second), nil},
			expected: 5 * time.Second,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			r := &rest{config: &Config{RequestTimeout: tc.config}}

			assert.Equal(t, tc.expected, r.resolveRouteOptions(tc.opts).timeout)
		})
	}
}

// deadlineFor registers handler at path under opts, serves one request against
// it, and reports the deadline the handler observed on its request context.
func deadlineFor(t *testing.T, config time.Duration, opts ...RouteOption) (time.Time, bool) {
	t.Helper()

	api, _ := newTestREST(t, Config{RequestTimeout: config})

	var deadline time.Time
	var ok bool
	api.GET("/budgeted", func(_ http.ResponseWriter, req *http.Request) {
		deadline, ok = req.Context().Deadline()
	}, opts...)

	serve(api.Handler(), http.MethodGet, "/budgeted", nil)

	return deadline, ok
}

func TestRoute_ConfigRequestTimeout_AppliesToPlainRoute(t *testing.T) {
	deadline, ok := deadlineFor(t, 30*time.Second)

	require.True(t, ok, "handler should observe a deadline")
	assert.WithinDuration(t, time.Now().Add(30*time.Second), deadline, time.Second)
}

func TestRoute_WithTimeout_OverridesConfigRequestTimeout(t *testing.T) {
	deadline, ok := deadlineFor(t, 30*time.Second, WithTimeout(55*time.Second))

	require.True(t, ok, "handler should observe a deadline")
	assert.WithinDuration(t, time.Now().Add(55*time.Second), deadline, time.Second)
}

func TestRoute_WithoutTimeout_LeavesNoDeadline(t *testing.T) {
	_, ok := deadlineFor(t, 30*time.Second, WithoutTimeout())

	assert.False(t, ok, "a waived budget must leave the context deadline-free")
}

// A zero RequestTimeout with no route options is the shape every API had before
// budgets existed, so it must still reach the handler untouched.
func TestRoute_NoBudget_LeavesNoDeadline(t *testing.T) {
	_, ok := deadlineFor(t, 0)

	assert.False(t, ok, "an unconfigured API must not deadline its routes")
}

func TestRoute_Budget_CancelsHandlerContextWhenSpent(t *testing.T) {
	api, _ := newTestREST(t, Config{})

	var err error
	api.GET("/slow", func(_ http.ResponseWriter, req *http.Request) {
		<-req.Context().Done()
		err = req.Context().Err()
	}, WithTimeout(10*time.Millisecond))

	serve(api.Handler(), http.MethodGet, "/slow", nil)

	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// The budget must be released as soon as the handler returns, so a fast handler
// on a generous budget does not hold a timer for the whole duration.
func TestRoute_Budget_CancelsOnceHandlerReturns(t *testing.T) {
	api, _ := newTestREST(t, Config{})

	var ctx context.Context
	api.GET("/fast", func(_ http.ResponseWriter, req *http.Request) {
		ctx = req.Context()
	}, WithTimeout(time.Hour))

	serve(api.Handler(), http.MethodGet, "/fast", nil)

	require.NotNil(t, ctx)
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}

// Every method resolves options through the same seam, so each one must carry them.
func TestRoute_EveryMethodCarriesOptions(t *testing.T) {
	testcases := []struct {
		method   string
		register func(r Router, path string, handler http.HandlerFunc, opts ...RouteOption)
	}{
		{http.MethodGet, Router.GET},
		{http.MethodHead, Router.HEAD},
		{http.MethodPost, Router.POST},
		{http.MethodPut, Router.PUT},
		{http.MethodPatch, Router.PATCH},
		{http.MethodDelete, Router.DELETE},
		{http.MethodOptions, Router.OPTIONS},
	}

	for _, tc := range testcases {
		t.Run(tc.method, func(t *testing.T) {
			api, _ := newTestREST(t, Config{})

			var ok bool
			tc.register(api, "/verb", func(_ http.ResponseWriter, req *http.Request) {
				_, ok = req.Context().Deadline()
			}, WithTimeout(30*time.Second))

			serve(api.Handler(), tc.method, "/verb", nil)

			assert.True(t, ok, "handler should observe a deadline")
		})
	}
}
