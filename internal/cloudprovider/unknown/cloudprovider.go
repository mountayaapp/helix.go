package unknown

import (
	"os"
	"path/filepath"

	"github.com/mountaya/helix.go/internal/cloudprovider"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// unknown holds some details about the service currently running and implements the
// CloudProvider interface.
type unknown struct {
	name string
}

// Detect returns the fallback cloud provider. It always succeeds: it names the
// service after the executable, falling back to "helix" if the executable path
// cannot be determined, which should never happen.
func Detect() cloudprovider.CloudProvider {
	name := "helix"

	path, err := os.Executable()
	if err == nil {
		name = filepath.Base(path)
	}

	return &unknown{
		name: name,
	}
}

// Attributes returns the baseline attributes: the service's name.
func (u *unknown) Attributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		semconv.ServiceName(u.name),
	}
}
