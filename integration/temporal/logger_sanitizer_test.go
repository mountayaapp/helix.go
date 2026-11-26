package temporal

import (
	"encoding/json/v2"
	"os"
	"strings"
	"testing"

	"github.com/mountaya/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// captureStderr points os.Stderr at a file of its own for the length of the
// test, and returns a function reading what was written to it so far. A Service
// built afterwards writes its standard error lines there. Call it before
// servicetest.New, so that the file outlives the Service, which syncs it as it
// stops.
func captureStderr(t *testing.T) func() string {
	t.Helper()

	file, err := os.CreateTemp(t.TempDir(), "stderr")
	require.NoError(t, err)

	original := os.Stderr
	os.Stderr = file
	t.Cleanup(func() {
		os.Stderr = original
		_ = file.Close()
	})

	return func() string {
		written, err := os.ReadFile(file.Name())
		require.NoError(t, err)

		return string(written)
	}
}

// stderrField returns the value under key of the standard error line whose
// message is message.
func stderrField(t *testing.T, stderr, message, key string) any {
	t.Helper()

	for line := range strings.SplitSeq(stderr, "\n") {
		var fields map[string]any
		if json.Unmarshal([]byte(line), &fields) != nil || fields["message"] != message {
			continue
		}

		return fields[key]
	}

	require.FailNowf(t, "no line", "no standard error line says %q in %s", message, stderr)
	return nil
}

// recordAttribute returns the value of the attribute key of the exported record
// whose body is body.
func recordAttribute(t *testing.T, records []sdklog.Record, body, key string) string {
	t.Helper()

	for _, record := range records {
		if record.Body().AsString() != body {
			continue
		}

		var value string
		record.WalkAttributes(func(kv attribute.KeyValue) bool {
			if string(kv.Key) == key {
				value = kv.Value.AsString()
				return false
			}

			return true
		})

		return value
	}

	require.FailNowf(t, "no record", "no record says %q", body)
	return ""
}

// TestCustomLogger_AnActivityErrorGoesThroughTheSanitizer pins what the SDK's own
// line for a failed activity holds, as the SDK writes it: the activity's error
// under "Error", through the Service's logger, to standard error and to the
// export. With a sanitizer registered both hold what it makes of the error; with
// none, the error as it was returned, as they always did.
func TestCustomLogger_AnActivityErrorGoesThroughTheSanitizer(t *testing.T) {
	logActivityError := func(t *testing.T) (string, []sdklog.Record) {
		t.Helper()

		stderr := captureStderr(t)
		svc, rec := servicetest.New(t)

		newCustomLogger(svc).Error("Activity error.",
			"ActivityType", "charge-user",
			"Attempt", 1,
			"Error", errChargeRefused,
		)

		return stderr(), rec.Records()
	}

	t.Run("with a sanitizer, what it makes of the error", func(t *testing.T) {
		registerSanitizer(t, withoutCard)

		stderr, records := logActivityError(t)

		assert.Equal(t, "charge refused", stderrField(t, stderr, "Activity error.", "Error"))
		assert.NotContains(t, stderr, "tok_")
		assert.Equal(t, "charge refused", recordAttribute(t, records, "Activity error.", "Error"))
	})

	t.Run("with none, the error as it was returned", func(t *testing.T) {
		registerSanitizer(t, nil)

		stderr, records := logActivityError(t)

		assert.Equal(t, errChargeRefused.Error(), stderrField(t, stderr, "Activity error.", "Error"))
		assert.Equal(t, errChargeRefused.Error(), recordAttribute(t, records, "Activity error.", "Error"))
	})
}
