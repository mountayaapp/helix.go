package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockServer struct {
	name    string
	status  int
	err     error
	stopErr error
}

func (m *mockServer) String() string {
	return m.name
}

func (m *mockServer) Start(ctx context.Context) error {
	return nil
}

func (m *mockServer) Stop(ctx context.Context) error {
	return m.stopErr
}

func (m *mockServer) Status(ctx context.Context) (int, error) {
	return m.status, m.err
}

type mockDependency struct {
	name     string
	status   int
	err      error
	closeErr error
}

func (m *mockDependency) String() string {
	return m.name
}

func (m *mockDependency) Close(ctx context.Context) error {
	return m.closeErr
}

func (m *mockDependency) Status(ctx context.Context) (int, error) {
	return m.status, m.err
}

func resetService() {
	svc = new(service)
}

func TestServe(t *testing.T) {
	t.Run("registers valid server", func(t *testing.T) {
		resetService()

		err := Serve(&mockServer{name: "rest", status: 200})

		assert.NoError(t, err)
		assert.NotNil(t, svc.server)
	})

	t.Run("rejects nil server", func(t *testing.T) {
		resetService()

		err := Serve(nil)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Server must not be nil")
	})

	t.Run("rejects server with empty name", func(t *testing.T) {
		resetService()

		err := Serve(&mockServer{name: "", status: 200})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Server's name must be set")
	})

	t.Run("rejects second server", func(t *testing.T) {
		resetService()

		err1 := Serve(&mockServer{name: "rest", status: 200})
		err2 := Serve(&mockServer{name: "graphql", status: 200})

		assert.NoError(t, err1)
		assert.Error(t, err2)
		assert.Contains(t, err2.Error(), "already been registered")
	})

	t.Run("rejects when service is stopped", func(t *testing.T) {
		resetService()
		svc.isStopped = true

		err := Serve(&mockServer{name: "rest", status: 200})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must not be stopped")
	})

	t.Run("rejects when service is initialized", func(t *testing.T) {
		resetService()
		svc.isInitialized = true

		err := Serve(&mockServer{name: "rest", status: 200})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must not be initialized")
	})

	t.Run("error includes serve message", func(t *testing.T) {
		resetService()

		err := Serve(nil)

		assert.Contains(t, err.Error(), "Failed to register server integration")
	})
}

func TestAttach(t *testing.T) {
	t.Run("attaches valid dependency", func(t *testing.T) {
		resetService()

		err := Attach(&mockDependency{name: "postgres", status: 200})

		assert.NoError(t, err)
	})

	t.Run("rejects nil dependency", func(t *testing.T) {
		resetService()

		err := Attach(nil)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Dependency must not be nil")
	})

	t.Run("rejects dependency with empty name", func(t *testing.T) {
		resetService()

		err := Attach(&mockDependency{name: "", status: 200})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Dependency's name must be set")
	})

	t.Run("attaches multiple dependencies", func(t *testing.T) {
		resetService()

		err1 := Attach(&mockDependency{name: "postgres", status: 200})
		err2 := Attach(&mockDependency{name: "valkey", status: 200})

		assert.NoError(t, err1)
		assert.NoError(t, err2)
		assert.Len(t, svc.dependencies, 2)
	})

	t.Run("preserves dependency order", func(t *testing.T) {
		resetService()

		Attach(&mockDependency{name: "alpha", status: 200})
		Attach(&mockDependency{name: "beta", status: 200})
		Attach(&mockDependency{name: "gamma", status: 200})

		assert.Equal(t, "alpha", svc.dependencies[0].String())
		assert.Equal(t, "beta", svc.dependencies[1].String())
		assert.Equal(t, "gamma", svc.dependencies[2].String())
	})

	t.Run("rejects when service is stopped", func(t *testing.T) {
		resetService()
		svc.isStopped = true

		err := Attach(&mockDependency{name: "postgres", status: 200})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must not be stopped")
	})

	t.Run("rejects when service is initialized", func(t *testing.T) {
		resetService()
		svc.isInitialized = true

		err := Attach(&mockDependency{name: "postgres", status: 200})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must not be initialized")
	})

	t.Run("error includes attach message", func(t *testing.T) {
		resetService()

		err := Attach(nil)

		assert.Contains(t, err.Error(), "Failed to attach dependency integration")
	})
}

func TestServer(t *testing.T) {
	t.Run("returns nil when no server registered", func(t *testing.T) {
		resetService()

		assert.Nil(t, Server())
	})

	t.Run("returns registered server", func(t *testing.T) {
		resetService()
		server := &mockServer{name: "rest", status: 200}
		Serve(server)

		assert.Equal(t, server, Server())
	})
}

func TestStatus(t *testing.T) {
	t.Run("returns 200 when no integrations", func(t *testing.T) {
		resetService()

		status, err := Status(t.Context())

		assert.NoError(t, err)
		assert.Equal(t, 200, status)
	})

	t.Run("returns 200 when all healthy", func(t *testing.T) {
		resetService()
		svc.server = &mockServer{name: "rest", status: 200}
		svc.dependencies = append(svc.dependencies,
			&mockDependency{name: "postgres", status: 200},
			&mockDependency{name: "valkey", status: 200},
		)

		status, err := Status(t.Context())

		assert.NoError(t, err)
		assert.Equal(t, 200, status)
	})

	t.Run("returns highest status when dependency is unhealthy", func(t *testing.T) {
		resetService()
		svc.server = &mockServer{name: "rest", status: 200}
		svc.dependencies = append(svc.dependencies,
			&mockDependency{name: "healthy", status: 200},
			&mockDependency{name: "unhealthy", status: 503},
		)

		status, err := Status(t.Context())

		require.NoError(t, err)
		assert.Equal(t, 503, status)
	})

	t.Run("returns error when integration errors", func(t *testing.T) {
		resetService()
		svc.dependencies = append(svc.dependencies,
			&mockDependency{name: "broken", status: 503, err: errors.New("connection failed")},
		)

		status, err := Status(t.Context())

		assert.Error(t, err)
		assert.Equal(t, 503, status)
		assert.Contains(t, err.Error(), "not in a healthy state")
	})

	t.Run("returns highest status among multiple unhealthy", func(t *testing.T) {
		resetService()
		svc.dependencies = append(svc.dependencies,
			&mockDependency{name: "a", status: 200},
			&mockDependency{name: "b", status: 502},
			&mockDependency{name: "c", status: 503},
		)

		status, err := Status(t.Context())

		require.NoError(t, err)
		assert.Equal(t, 503, status)
	})

	t.Run("collects multiple errors", func(t *testing.T) {
		resetService()
		svc.dependencies = append(svc.dependencies,
			&mockDependency{name: "a", status: 503, err: errors.New("error a")},
			&mockDependency{name: "b", status: 502, err: errors.New("error b")},
		)

		status, err := Status(t.Context())

		assert.Error(t, err)
		assert.Equal(t, 503, status)
	})

	t.Run("single dependency healthy", func(t *testing.T) {
		resetService()
		svc.dependencies = append(svc.dependencies,
			&mockDependency{name: "single", status: 200},
		)

		status, err := Status(t.Context())

		assert.NoError(t, err)
		assert.Equal(t, 200, status)
	})

	t.Run("server only healthy", func(t *testing.T) {
		resetService()
		svc.server = &mockServer{name: "rest", status: 200}

		status, err := Status(t.Context())

		assert.NoError(t, err)
		assert.Equal(t, 200, status)
	})
}

func TestStart_RejectsAlreadyInitialized(t *testing.T) {
	resetService()
	svc.isInitialized = true

	err := Start(t.Context())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already been initialized")
}

func TestStart_RejectsStopped(t *testing.T) {
	resetService()
	svc.isStopped = true

	err := Start(t.Context())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Cannot initialize a stopped service")
}

func TestStart_RejectsNoServer(t *testing.T) {
	resetService()

	err := Start(t.Context())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must have a server registered")
}

func TestStart_ErrorIncludesMessage(t *testing.T) {
	resetService()
	svc.isInitialized = true

	err := Start(t.Context())

	assert.Contains(t, err.Error(), "Failed to initialize the service")
}

func TestStart_ErrorHasValidations(t *testing.T) {
	resetService()
	svc.isInitialized = true

	err := Start(t.Context())

	var stackErr *errorstack.Error
	assert.ErrorAs(t, err, &stackErr)
	assert.True(t, stackErr.HasValidations())
}

func TestStop_RejectsNotInitialized(t *testing.T) {
	resetService()

	err := Stop(t.Context())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must first be initialized")
}

func TestStop_RejectsAlreadyStopped(t *testing.T) {
	resetService()
	svc.isInitialized = true
	svc.isStopped = true

	err := Stop(t.Context())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already been stopped")
}

func TestStop_ErrorIncludesMessage(t *testing.T) {
	resetService()

	err := Stop(t.Context())

	assert.Contains(t, err.Error(), "Failed to gracefully close")
}

func TestStop_ErrorHasValidations(t *testing.T) {
	resetService()

	err := Stop(t.Context())

	var stackErr *errorstack.Error
	assert.ErrorAs(t, err, &stackErr)
	assert.True(t, stackErr.HasValidations())
}
