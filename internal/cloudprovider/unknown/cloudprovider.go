package unknown

import (
	"os"
	"path/filepath"

	"github.com/mountayaapp/helix.go/internal/cloudprovider"

	"go.opentelemetry.io/otel/attribute"
)

/*
cp is always set since the "unknown" cloud provider is used as fallback in case
no other cloud provider has been detected.
*/
var cp cloudprovider.CloudProvider

/*
unknown holds some details about the service currently running and implements the
CloudProvider interface.
*/
type unknown struct {
	name string
}

/*
init populates the cloud provider as a fallback cloud provider.
*/
func init() {
	cp = build()
}

/*
build creates the fallback cloud provider. It uses the executable name as the
service name. If the executable path cannot be determined, it falls back to
"helix". This should never happen.
*/
func build() cloudprovider.CloudProvider {
	name := "helix"

	path, err := os.Executable()
	if err == nil {
		name = filepath.Base(path)
	}

	u := &unknown{
		name: name,
	}

	return u
}

/*
Get returns the fallback cloud provider interface.
*/
func Get() cloudprovider.CloudProvider {
	return cp
}

/*
String returns the string representation of the unknown cloud provider.
*/
func (u *unknown) String() string {
	return "unknown"
}

/*
Attributes returns basic OpenTelemetry attributes.
*/
func (u *unknown) Attributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("service.name", u.name),
	}
}
