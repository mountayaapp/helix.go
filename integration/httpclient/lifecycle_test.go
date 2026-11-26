package httpclient

import (
	"testing"

	"github.com/mountaya/helix.go/integration"

	"github.com/stretchr/testify/assert"
)

// TestReturnedHandleCarriesNoLifecycle pins the invariant: what Connect hands back
// must not implement integration.Dependency, so that no type assertion can reach
// Close or CheckHealth. The Service owns the connection's lifecycle.
//
// If this fails, someone moved the lifecycle methods back onto the type the caller
// holds, and `db.(integration.Dependency).Close(ctx)` compiles again.
func TestReturnedHandleCarriesNoLifecycle(t *testing.T) {
	var handle any = (*connection)(nil)

	_, isDependency := handle.(integration.Dependency)
	assert.False(t, isDependency,
		"the value handed to a caller must not implement integration.Dependency")

	// The adapter, which is what actually gets registered, does implement it.
	var registered any = dependency{}
	_, isDependency = registered.(integration.Dependency)
	assert.True(t, isDependency)
}
