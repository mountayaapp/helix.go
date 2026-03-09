package temporal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
	"go.temporal.io/sdk/interceptor"
)

// renameTags applies the same tag renaming logic as customtracer.StartSpan
// in isolation, allowing us to test it without a full interceptor.Tracer
// implementation (which has unexported methods).
func renameTags(opts *interceptor.TracerStartSpanOptions) {
	for old, renamed := range tagRenames {
		if v := opts.Tags[old]; v != "" {
			opts.Tags[renamed] = v
			delete(opts.Tags, old)
		}
	}
}

func TestTagRenaming_WorkflowID(t *testing.T) {
	opts := &interceptor.TracerStartSpanOptions{
		Tags: map[string]string{
			"temporalWorkflowID": "wf_123",
		},
	}

	renameTags(opts)

	assert.Equal(t, "wf_123", opts.Tags["temporal.workflow.id"])
	assert.Empty(t, opts.Tags["temporalWorkflowID"])
}

func TestTagRenaming_RunID(t *testing.T) {
	opts := &interceptor.TracerStartSpanOptions{
		Tags: map[string]string{
			"temporalRunID": "run_456",
		},
	}

	renameTags(opts)

	assert.Equal(t, "run_456", opts.Tags["temporal.workflow.run_id"])
	assert.Empty(t, opts.Tags["temporalRunID"])
}

func TestTagRenaming_ActivityID(t *testing.T) {
	opts := &interceptor.TracerStartSpanOptions{
		Tags: map[string]string{
			"temporalActivityID": "act_789",
		},
	}

	renameTags(opts)

	assert.Equal(t, "act_789", opts.Tags["temporal.activity.id"])
	assert.Empty(t, opts.Tags["temporalActivityID"])
}

func TestTagRenaming_UpdateID(t *testing.T) {
	opts := &interceptor.TracerStartSpanOptions{
		Tags: map[string]string{
			"temporalUpdateID": "upd_abc",
		},
	}

	renameTags(opts)

	assert.Equal(t, "upd_abc", opts.Tags["temporal.update.id"])
	assert.Empty(t, opts.Tags["temporalUpdateID"])
}

func TestTagRenaming_AllTags(t *testing.T) {
	opts := &interceptor.TracerStartSpanOptions{
		Tags: map[string]string{
			"temporalWorkflowID": "wf_all",
			"temporalRunID":      "run_all",
			"temporalActivityID": "act_all",
			"temporalUpdateID":   "upd_all",
			"other_tag":          "preserved",
		},
	}

	renameTags(opts)

	assert.Equal(t, "wf_all", opts.Tags["temporal.workflow.id"])
	assert.Equal(t, "run_all", opts.Tags["temporal.workflow.run_id"])
	assert.Equal(t, "act_all", opts.Tags["temporal.activity.id"])
	assert.Equal(t, "upd_all", opts.Tags["temporal.update.id"])
	assert.Equal(t, "preserved", opts.Tags["other_tag"])

	// Original keys should be removed.
	assert.Empty(t, opts.Tags["temporalWorkflowID"])
	assert.Empty(t, opts.Tags["temporalRunID"])
	assert.Empty(t, opts.Tags["temporalActivityID"])
	assert.Empty(t, opts.Tags["temporalUpdateID"])
}

func TestTagRenaming_EmptyTagsNotRenamed(t *testing.T) {
	opts := &interceptor.TracerStartSpanOptions{
		Tags: map[string]string{
			"temporalWorkflowID": "",
			"temporalRunID":      "",
		},
	}

	renameTags(opts)

	// Empty values should not be renamed.
	_, hasNewKey := opts.Tags["temporal.workflow.id"]
	assert.False(t, hasNewKey)
}

func TestTagRenaming_NoTemporalTags(t *testing.T) {
	opts := &interceptor.TracerStartSpanOptions{
		Tags: map[string]string{
			"custom_tag": "custom_value",
		},
	}

	renameTags(opts)

	// Non-temporal tags should be preserved as-is.
	assert.Equal(t, "custom_value", opts.Tags["custom_tag"])
	assert.Len(t, opts.Tags, 1)
}

func TestTagRenames_MapContainsExpectedEntries(t *testing.T) {
	assert.Len(t, tagRenames, 4)
	assert.Equal(t, "temporal.workflow.id", tagRenames["temporalWorkflowID"])
	assert.Equal(t, "temporal.workflow.run_id", tagRenames["temporalRunID"])
	assert.Equal(t, "temporal.activity.id", tagRenames["temporalActivityID"])
	assert.Equal(t, "temporal.update.id", tagRenames["temporalUpdateID"])
}

func TestNoopSpan_IsCached(t *testing.T) {
	assert.NotNil(t, noopSpan)

	assert.NotPanics(t, func() {
		noopSpan.SetAttributes(attribute.String("key", "value"))
	})
}

func TestDefaultConverter_IsCached(t *testing.T) {
	assert.NotNil(t, defaultConverter)

	payload, err := defaultConverter.ToPayload("test")
	assert.NoError(t, err)

	var output string
	err = defaultConverter.FromPayload(payload, &output)
	assert.NoError(t, err)
	assert.Equal(t, "test", output)
}
