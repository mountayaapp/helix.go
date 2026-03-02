package bucket

import (
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/stretchr/testify/assert"
)

func TestConfig_Sanitize(t *testing.T) {
	testcases := []struct {
		name   string
		before Config
		after  Config
		err    error
	}{
		{
			name: "valid driver and bucket is valid",
			before: Config{
				Driver: DriverLocal,
				Bucket: "anything",
			},
			after: Config{
				Driver: DriverLocal,
				Bucket: "anything",
			},
			err: nil,
		},
		{
			name: "missing bucket returns error",
			before: Config{
				Driver: DriverLocal,
			},
			after: Config{
				Driver: DriverLocal,
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "Bucket must be set and not be empty",
						Path:    []string{"Config", "Bucket"},
					},
				},
			},
		},
		{
			name:   "empty config returns driver and bucket errors",
			before: Config{},
			after:  Config{},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "Driver must be set and not be nil",
						Path:    []string{"Config", "Driver"},
					},
					{
						Message: "Bucket must be set and not be empty",
						Path:    []string{"Config", "Bucket"},
					},
				},
			},
		},
		{
			name: "subfolder without trailing slash returns error",
			before: Config{
				Driver:    DriverLocal,
				Bucket:    "anything",
				Subfolder: "not/a/valid/path",
			},
			after: Config{
				Driver:    DriverLocal,
				Bucket:    "anything",
				Subfolder: "not/a/valid/path",
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "Subfolder must end with a trailing slash",
						Path:    []string{"Config", "Subfolder"},
					},
				},
			},
		},
		{
			name: "subfolder with trailing slash is valid",
			before: Config{
				Driver:    DriverLocal,
				Bucket:    "anything",
				Subfolder: "valid/path/",
			},
			after: Config{
				Driver:    DriverLocal,
				Bucket:    "anything",
				Subfolder: "valid/path/",
			},
			err: nil,
		},
		{
			name: "missing driver returns error",
			before: Config{
				Bucket: "anything",
			},
			after: Config{
				Bucket: "anything",
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "Driver must be set and not be nil",
						Path:    []string{"Config", "Driver"},
					},
				},
			},
		},
		{
			name: "subfolder as root slash is valid",
			before: Config{
				Driver:    DriverLocal,
				Bucket:    "anything",
				Subfolder: "/",
			},
			after: Config{
				Driver:    DriverLocal,
				Bucket:    "anything",
				Subfolder: "/",
			},
			err: nil,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.before.sanitize()

			assert.Equal(t, tc.after, tc.before)
			assert.Equal(t, tc.err, err)
		})
	}
}
