package unknown

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestBuild(t *testing.T) {
	provider := build()

	assert.NotNil(t, provider)
	assert.Equal(t, "unknown", provider.String())
}

func TestGet(t *testing.T) {
	provider := Get()

	assert.NotNil(t, provider)
	assert.Equal(t, "unknown", provider.String())
}

func TestString(t *testing.T) {
	u := &unknown{name: "test-service"}

	assert.Equal(t, "unknown", u.String())
}

func TestAttributes(t *testing.T) {
	u := &unknown{name: "my-service"}

	attrs := u.Attributes()

	assert.Len(t, attrs, 1)
	assert.Equal(t, attribute.String("service.name", "my-service"), attrs[0])
}

func TestAttributes_Empty(t *testing.T) {
	u := &unknown{name: ""}

	attrs := u.Attributes()

	assert.Len(t, attrs, 1)
	assert.Equal(t, attribute.String("service.name", ""), attrs[0])
}
