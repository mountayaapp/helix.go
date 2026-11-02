package unknown

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func TestDetect(t *testing.T) {
	provider := Detect()
	require.NotNil(t, provider)

	attrs := provider.Attributes()
	require.Len(t, attrs, 1)
	assert.Equal(t, attribute.Key("service.name"), attrs[0].Key)
	assert.NotEmpty(t, attrs[0].Value.AsString())
}

func TestAttributes(t *testing.T) {
	u := &unknown{name: "my-service"}

	assert.Equal(t, []attribute.KeyValue{attribute.String("service.name", "my-service")}, u.Attributes())
}
