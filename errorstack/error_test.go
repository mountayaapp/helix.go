package errorstack

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
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
		{
			name:  "empty integration option",
			input: New("test", WithIntegration("")),
			expected: &Error{
				Message:     "test",
				Validations: []Validation{},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.input)
		})
	}
}

func TestWrap(t *testing.T) {
	testcases := []struct {
		name            string
		input           error
		message         string
		opts            []Option
		expectedMessage string
		expectedNil     bool
	}{
		{
			name:        "nil error returns nil",
			input:       nil,
			message:     "wrapper",
			expectedNil: true,
		},
		{
			name:            "standard error",
			input:           errors.New("something went wrong"),
			message:         "operation failed",
			expectedMessage: "operation failed",
		},
		{
			name:            "standard error with integration option",
			input:           errors.New("something went wrong"),
			message:         "operation failed",
			opts:            []Option{WithIntegration("postgres")},
			expectedMessage: "operation failed",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := Wrap(tc.input, tc.message, tc.opts...)

			if tc.expectedNil {
				assert.Nil(t, actual)
			} else {
				assert.Equal(t, tc.expectedMessage, actual.Message)
			}
		})
	}
}

func TestWrap_PreservesCause(t *testing.T) {
	original := errors.New("connection refused")

	wrapped := Wrap(original, "database error")

	assert.Equal(t, "database error", wrapped.Message)
	assert.Equal(t, original, wrapped.Unwrap())
}

func TestWrap_ErrorsIs(t *testing.T) {
	sentinel := errors.New("sentinel error")

	wrapped := Wrap(sentinel, "wrapped message")

	assert.True(t, errors.Is(wrapped, sentinel))
}

func TestWrap_ErrorsAs(t *testing.T) {
	inner := New("inner error", WithIntegration("postgres"))

	wrapped := Wrap(inner, "outer error")

	var target *Error
	assert.True(t, errors.As(wrapped, &target))
	assert.Equal(t, "outer error", target.Message)
}

func TestWrap_WrappedErrorChain(t *testing.T) {
	root := errors.New("root cause")
	level1 := Wrap(root, "level 1")
	level2 := Wrap(level1, "level 2")
	level3 := Wrap(level2, "level 3")

	assert.True(t, errors.Is(level3, root))
	assert.True(t, errors.Is(level3, level1))
	assert.True(t, errors.Is(level3, level2))

	var target *Error
	assert.True(t, errors.As(level3, &target))
	assert.Equal(t, "level 3", target.Message)
}

func TestWrap_WithValidations(t *testing.T) {
	original := errors.New("database error")
	wrapped := Wrap(original, "operation failed").WithValidations(Validation{
		Message: "connection timeout",
		Path:    []string{"Config", "Address"},
	})

	assert.True(t, wrapped.HasValidations())
	assert.True(t, errors.Is(wrapped, original))
	assert.Contains(t, wrapped.Error(), "connection timeout")
}

func TestUnwrap_NoCause(t *testing.T) {
	err := New("no cause")

	assert.Nil(t, err.Unwrap())
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
			name:     "after adding empty",
			input:    New("test").WithValidations(),
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
			assert.Equal(t, tc.expected, tc.input.HasValidations())
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
			name:     "empty children",
			input:    New("parent error"),
			children: []error{},
			hasChild: false,
		},
		{
			name:     "all nil children",
			input:    New("parent"),
			children: []error{nil, nil, nil},
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
		{
			name:     "nil children filtered",
			input:    New("parent"),
			children: []error{nil, errors.New("real"), nil},
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

func TestError_WithChildren_IsChainable(t *testing.T) {
	err := New("parent")

	result := err.WithChildren(errors.New("child"))

	assert.Same(t, err, result)
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

func TestError_WithChildren_Concurrent(t *testing.T) {
	t.Parallel()

	err := New("concurrent parent")
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			err.WithChildren(fmt.Errorf("child-%d", n))
		}(i)
	}

	// Concurrent reads while writes are happening.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = err.HasChildren()
			_ = err.Error()
		}()
	}

	wg.Wait()
	assert.True(t, err.HasChildren())
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
			name:     "empty message",
			input:    New(""),
			expected: ".",
		},
		{
			name:     "empty error",
			input:    &Error{},
			expected: ".",
		},
		{
			name:     "only integration",
			input:    &Error{Integration: "rest", Validations: []Validation{}},
			expected: "rest: .",
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
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.input.Error())
		})
	}
}

func TestError_ImplementsErrorInterface(t *testing.T) {
	var err error = New("test error")
	assert.NotNil(t, err)
	assert.Equal(t, "test error.", err.Error())
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
			name:     "omits empty message",
			input:    &Error{Validations: []Validation{}},
			expected: `{}`,
		},
		{
			name:  "single validation with path",
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

func TestError_MarshalJSON_OmitsEmptyValidations(t *testing.T) {
	err := New("simple error")

	b, marshalErr := json.Marshal(err)

	assert.NoError(t, marshalErr)
	assert.NotContains(t, string(b), "validations")
}

func TestError_MarshalJSON_RoundTrip(t *testing.T) {
	original := New("validation error").WithValidations(
		Validation{
			Message: "field is required",
			Path:    []string{"body", "email"},
		},
		Validation{
			Message: "must be at least 8 characters",
			Path:    []string{"body", "password"},
		},
	)

	b, err := json.Marshal(original)
	assert.NoError(t, err)

	var unmarshaled Error
	err = json.Unmarshal(b, &unmarshaled)
	assert.NoError(t, err)

	assert.Equal(t, original.Message, unmarshaled.Message)
	assert.Equal(t, original.Validations, unmarshaled.Validations)
}

func TestError_UnmarshalJSON(t *testing.T) {
	testcases := []struct {
		name              string
		input             string
		expectedMessage   string
		expectedErr       bool
		validationsNil    bool
		validationsLen    int
	}{
		{
			name:            "with validations",
			input:           `{"message":"validation failed","validations":[{"message":"field required","path":["body","name"]}]}`,
			expectedMessage: "validation failed",
			validationsLen:  1,
		},
		{
			name:            "no validations",
			input:           `{"message":"simple error"}`,
			expectedMessage: "simple error",
			validationsNil:  true,
		},
		{
			name:        "empty object",
			input:       `{}`,
			validationsNil: true,
		},
		{
			name:        "invalid JSON",
			input:       `not valid json`,
			expectedErr: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			var err Error
			unmarshalErr := json.Unmarshal([]byte(tc.input), &err)

			if tc.expectedErr {
				assert.Error(t, unmarshalErr)
				return
			}

			assert.NoError(t, unmarshalErr)
			assert.Equal(t, tc.expectedMessage, err.Message)
			if tc.validationsNil {
				assert.Nil(t, err.Validations)
			} else {
				assert.Len(t, err.Validations, tc.validationsLen)
			}
		})
	}
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

func TestValidation_UnmarshalJSON(t *testing.T) {
	input := `{"message":"field required","path":["body","name"]}`

	var v Validation
	err := json.Unmarshal([]byte(input), &v)

	assert.NoError(t, err)
	assert.Equal(t, "field required", v.Message)
	assert.Equal(t, []string{"body", "name"}, v.Path)
}
