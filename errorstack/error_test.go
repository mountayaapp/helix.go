package errorstack

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	testcases := []struct {
		name     string
		input    *Error
		expected *Error
	}{
		{
			name:  "simple text message",
			input: New("This is a simple text example"),
			expected: &Error{
				Message:     "This is a simple text example",
				Validations: []Validation{},
			},
		},
		{
			name:  "with integration option",
			input: New("This is a text example with integration", WithIntegration("bucket")),
			expected: &Error{
				Integration: "bucket",
				Message:     "This is a text example with integration",
				Validations: []Validation{},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.input

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestNew_AlwaysInitializesValidations(t *testing.T) {
	err := New("test")

	assert.NotNil(t, err.Validations)
	assert.Len(t, err.Validations, 0)
}

func TestNew_MultipleOptions(t *testing.T) {
	err := New("test", WithIntegration("rest"))

	assert.Equal(t, "rest", err.Integration)
	assert.Equal(t, "test", err.Message)
}

func TestNewFromError(t *testing.T) {
	testcases := []struct {
		name     string
		input    error
		opts     []With
		expected *Error
	}{
		{
			name:     "nil error returns nil",
			input:    nil,
			expected: nil,
		},
		{
			name:  "standard error",
			input: errors.New("something went wrong"),
			expected: &Error{
				Message:     "something went wrong",
				Validations: []Validation{},
			},
		},
		{
			name:  "standard error with integration option",
			input: errors.New("something went wrong"),
			opts:  []With{WithIntegration("postgres")},
			expected: &Error{
				Integration: "postgres",
				Message:     "something went wrong",
				Validations: []Validation{},
			},
		},
		{
			name:  "helix error is flattened",
			input: New("existing helix error"),
			expected: &Error{
				Message:     "existing helix error.",
				Validations: []Validation{},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := NewFromError(tc.input, tc.opts...)

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestNewFromError_WithHelixErrorContainingValidations(t *testing.T) {
	original := New("original").WithValidations(Validation{
		Message: "field is required",
		Path:    []string{"Config", "Field"},
	})

	wrapped := NewFromError(original)

	assert.Contains(t, wrapped.Message, "original")
	assert.Contains(t, wrapped.Message, "field is required")
}

func TestNewFromError_PreservesErrorString(t *testing.T) {
	original := errors.New("connection refused")

	wrapped := NewFromError(original)

	assert.Equal(t, "connection refused", wrapped.Message)
}

func TestError_WithValidations(t *testing.T) {
	testcases := []struct {
		name        string
		input       *Error
		validations []Validation
		expected    *Error
	}{
		{
			name:        "empty validations",
			input:       New("test error"),
			validations: []Validation{},
			expected: &Error{
				Message:     "test error",
				Validations: []Validation{},
			},
		},
		{
			name:  "single validation with path",
			input: New("test error"),
			validations: []Validation{
				{
					Message: "field is required",
					Path:    []string{"Config", "Address"},
				},
			},
			expected: &Error{
				Message: "test error",
				Validations: []Validation{
					{
						Message: "field is required",
						Path:    []string{"Config", "Address"},
					},
				},
			},
		},
		{
			name:  "multiple validations",
			input: New("test error"),
			validations: []Validation{
				{
					Message: "first validation",
					Path:    []string{"a"},
				},
				{
					Message: "second validation",
					Path:    []string{"b"},
				},
			},
			expected: &Error{
				Message: "test error",
				Validations: []Validation{
					{
						Message: "first validation",
						Path:    []string{"a"},
					},
					{
						Message: "second validation",
						Path:    []string{"b"},
					},
				},
			},
		},
		{
			name:  "validation without path",
			input: New("test error"),
			validations: []Validation{
				{
					Message: "no path validation",
				},
			},
			expected: &Error{
				Message: "test error",
				Validations: []Validation{
					{
						Message: "no path validation",
					},
				},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.input.WithValidations(tc.validations...)

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestError_WithValidations_IsChainable(t *testing.T) {
	err := New("test")

	result := err.WithValidations(Validation{Message: "a"})

	assert.Same(t, err, result)
}

func TestError_WithValidations_Accumulates(t *testing.T) {
	err := New("test")
	err.WithValidations(Validation{Message: "first"})
	err.WithValidations(Validation{Message: "second"})
	err.WithValidations(Validation{Message: "third"})

	assert.Len(t, err.Validations, 3)
	assert.Equal(t, "first", err.Validations[0].Message)
	assert.Equal(t, "second", err.Validations[1].Message)
	assert.Equal(t, "third", err.Validations[2].Message)
}

func TestError_WithValidations_DeepPath(t *testing.T) {
	err := New("test").WithValidations(Validation{
		Message: "deeply nested failure",
		Path:    []string{"request", "body", "user", "address", "zip_code"},
	})

	assert.Len(t, err.Validations, 1)
	assert.Len(t, err.Validations[0].Path, 5)
}

func TestError_HasValidations(t *testing.T) {
	testcases := []struct {
		name     string
		input    *Error
		expected bool
	}{
		{
			name:     "no validations",
			input:    New("no validations"),
			expected: false,
		},
		{
			name: "with validations",
			input: New("with validations").WithValidations(Validation{
				Message: "something failed",
			}),
			expected: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.input.HasValidations()

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestError_WithChildren(t *testing.T) {
	testcases := []struct {
		name     string
		input    *Error
		children []error
		hasChild bool
	}{
		{
			name:     "nil children",
			input:    New("parent error"),
			children: nil,
			hasChild: false,
		},
		{
			name:     "single child",
			input:    New("parent error"),
			children: []error{errors.New("child error")},
			hasChild: true,
		},
		{
			name:  "multiple children",
			input: New("parent error"),
			children: []error{
				errors.New("child error 1"),
				errors.New("child error 2"),
			},
			hasChild: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			tc.input.WithChildren(tc.children...)

			assert.Equal(t, tc.hasChild, tc.input.HasChildren())
		})
	}
}

func TestError_WithChildren_ReturnsErrorInterface(t *testing.T) {
	err := New("parent")

	result := err.WithChildren(errors.New("child"))

	var _ error = result
	assert.NotNil(t, result)
}

func TestError_WithChildren_Accumulates(t *testing.T) {
	err := New("parent")
	err.WithChildren(errors.New("first"))
	err.WithChildren(errors.New("second"))
	err.WithChildren(errors.New("third"))

	assert.True(t, err.HasChildren())
}

func TestError_WithChildren_NestedHelixErrors(t *testing.T) {
	child := New("child error", WithIntegration("postgres"))
	child.WithValidations(Validation{
		Message: "connection timeout",
		Path:    []string{"Config", "Address"},
	})

	parent := New("parent error")
	parent.WithChildren(child)

	assert.True(t, parent.HasChildren())
	assert.Contains(t, parent.Error(), "child error")
	assert.Contains(t, parent.Error(), "connection timeout")
}

func TestError_HasChildren(t *testing.T) {
	testcases := []struct {
		name     string
		input    *Error
		expected bool
	}{
		{
			name:     "no children",
			input:    New("no children"),
			expected: false,
		},
		{
			name: "with children",
			input: func() *Error {
				err := New("with children")
				err.WithChildren(errors.New("child"))
				return err
			}(),
			expected: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.input.HasChildren()

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestError_Error(t *testing.T) {
	testcases := []struct {
		name     string
		input    *Error
		expected string
	}{
		{
			name:     "simple message",
			input:    New("This is a simple text example"),
			expected: `This is a simple text example.`,
		},
		{
			name:     "with integration",
			input:    New("This is a text example with integration", WithIntegration("bucket")),
			expected: `bucket: This is a text example with integration.`,
		},
		{
			name: "with integration and validations",
			input: New("This is a text example with validations", WithIntegration("bucket")).WithValidations(Validation{
				Message: "Failed to validate test case",
				Path:    []string{"custom", "path"},
			}),
			expected: `bucket: This is a text example with validations. Reasons:
    - Failed to validate test case
      at custom > path.
`,
		},
		{
			name: "multiple validations",
			input: New("error with multiple validations").WithValidations(
				Validation{
					Message: "first issue",
					Path:    []string{"field", "a"},
				},
				Validation{
					Message: "second issue",
					Path:    []string{"field", "b"},
				},
			),
			expected: `error with multiple validations. Reasons:
    - first issue
      at field > a.
    - second issue
      at field > b.
`,
		},
		{
			name: "validation without path",
			input: New("validation without path").WithValidations(Validation{
				Message: "something failed",
			}),
			expected: `validation without path. Reasons:
    - something failed.
`,
		},
		{
			name: "with children",
			input: func() *Error {
				err := New("parent error")
				err.WithChildren(errors.New("child error 1"), errors.New("child error 2"))
				return err
			}(),
			expected: `parent error. Caused by:

- child error 1
- child error 2
`,
		},
		{
			name: "with validations and children",
			input: func() *Error {
				err := New("parent with validations and children", WithIntegration("test"))
				err.WithValidations(Validation{
					Message: "validation issue",
					Path:    []string{"Config"},
				})
				err.WithChildren(errors.New("child"))
				return err
			}(),
			expected: `test: parent with validations and children. Reasons:
    - validation issue
      at Config.
 Caused by:

- child
`,
		},
		{
			name:     "empty error",
			input:    &Error{},
			expected: ".",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.input.Error()

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestError_Error_OnlyIntegration(t *testing.T) {
	err := &Error{
		Integration: "rest",
		Validations: []Validation{},
	}

	assert.Equal(t, "rest: .", err.Error())
}

func TestError_ImplementsErrorInterface(t *testing.T) {
	var err error = New("test error")
	assert.NotNil(t, err)
	assert.Equal(t, "test error.", err.Error())
}

func TestWithIntegration(t *testing.T) {
	testcases := []struct {
		name        string
		integration string
		expected    string
	}{
		{
			name:        "rest",
			integration: "rest",
			expected:    "rest",
		},
		{
			name:        "temporal",
			integration: "temporal",
			expected:    "temporal",
		},
		{
			name:        "empty string",
			integration: "",
			expected:    "",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			err := New("test", WithIntegration(tc.integration))

			assert.Equal(t, tc.expected, err.Integration)
		})
	}
}

func TestError_MarshalJSON(t *testing.T) {
	testcases := []struct {
		name     string
		input    *Error
		expected string
	}{
		{
			name:     "simple message",
			input:    New("something went wrong"),
			expected: `{"message": "something went wrong"}`,
		},
		{
			name: "single validation with path",
			input: New("validation error").WithValidations(
				Validation{
					Message: "field is required",
					Path:    []string{"request", "body", "email"},
				},
			),
			expected: `{
				"message": "validation error",
				"validations": [
					{
						"message": "field is required",
						"path": ["request", "body", "email"]
					}
				]
			}`,
		},
		{
			name: "multiple validations",
			input: New("multiple validations").WithValidations(
				Validation{
					Message: "name is required",
					Path:    []string{"body", "name"},
				},
				Validation{
					Message: "email is invalid",
					Path:    []string{"body", "email"},
				},
			),
			expected: `{
				"message": "multiple validations",
				"validations": [
					{
						"message": "name is required",
						"path": ["body", "name"]
					},
					{
						"message": "email is invalid",
						"path": ["body", "email"]
					}
				]
			}`,
		},
		{
			name: "validation without path",
			input: New("no path").WithValidations(
				Validation{
					Message: "something went wrong",
				},
			),
			expected: `{
				"message": "no path",
				"validations": [
					{
						"message": "something went wrong"
					}
				]
			}`,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.input)

			assert.NoError(t, err)
			assert.JSONEq(t, tc.expected, string(b))
		})
	}
}

func TestError_MarshalJSON_OmitsIntegration(t *testing.T) {
	err := New("test", WithIntegration("rest"))

	b, marshalErr := json.Marshal(err)

	assert.NoError(t, marshalErr)
	assert.NotContains(t, string(b), "integration")
	assert.NotContains(t, string(b), `"rest"`)
}

func TestError_MarshalJSON_OmitsChildren(t *testing.T) {
	err := New("parent")
	err.WithChildren(errors.New("child"))

	b, marshalErr := json.Marshal(err)

	assert.NoError(t, marshalErr)
	assert.NotContains(t, string(b), "children")
	assert.NotContains(t, string(b), "child")
}

func TestError_MarshalJSON_OmitsEmptyMessage(t *testing.T) {
	err := &Error{
		Validations: []Validation{},
	}

	b, marshalErr := json.Marshal(err)

	assert.NoError(t, marshalErr)
	assert.JSONEq(t, `{}`, string(b))
}

func TestError_UnmarshalJSON(t *testing.T) {
	input := `{
		"message": "validation failed",
		"validations": [
			{
				"message": "field required",
				"path": ["body", "name"]
			}
		]
	}`

	var err Error
	unmarshalErr := json.Unmarshal([]byte(input), &err)

	assert.NoError(t, unmarshalErr)
	assert.Equal(t, "validation failed", err.Message)
	assert.Len(t, err.Validations, 1)
	assert.Equal(t, "field required", err.Validations[0].Message)
	assert.Equal(t, []string{"body", "name"}, err.Validations[0].Path)
}

func TestError_UnmarshalJSON_NoValidations(t *testing.T) {
	input := `{"message": "simple error"}`

	var err Error
	unmarshalErr := json.Unmarshal([]byte(input), &err)

	assert.NoError(t, unmarshalErr)
	assert.Equal(t, "simple error", err.Message)
	assert.Nil(t, err.Validations)
}

func TestValidation_MarshalJSON(t *testing.T) {
	testcases := []struct {
		name     string
		input    Validation
		expected string
	}{
		{
			name:     "message only",
			input:    Validation{Message: "field required"},
			expected: `{"message":"field required"}`,
		},
		{
			name: "message with path",
			input: Validation{
				Message: "field invalid",
				Path:    []string{"request", "body"},
			},
			expected: `{"message":"field invalid","path":["request","body"]}`,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.input)

			assert.NoError(t, err)
			assert.JSONEq(t, tc.expected, string(b))
		})
	}
}
