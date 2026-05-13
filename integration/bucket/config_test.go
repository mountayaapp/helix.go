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
			name:   "empty config returns driver and bucket errors",
			before: Config{},
			after:  Config{},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "driver"}},
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "bucket"}},
			),
		},
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
			name: "missing driver returns error",
			before: Config{
				Bucket: "anything",
			},
			after: Config{
				Bucket: "anything",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "driver"}},
			),
		},
		{
			name: "missing bucket returns error",
			before: Config{
				Driver: DriverLocal,
			},
			after: Config{
				Driver: DriverLocal,
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "bucket"}},
			),
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
			err: errorstack.NewValidation(
				errorstack.Entry{
					Message: "Must end with a trailing slash",
					Path:    []any{"config", "subfolder"},
				},
			),
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
		{
			name: "empty subfolder is valid",
			before: Config{
				Driver:    DriverLocal,
				Bucket:    "anything",
				Subfolder: "",
			},
			after: Config{
				Driver:    DriverLocal,
				Bucket:    "anything",
				Subfolder: "",
			},
			err: nil,
		},
		{
			name: "all three errors combined",
			before: Config{
				Subfolder: "not/a/valid/path",
			},
			after: Config{
				Subfolder: "not/a/valid/path",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "driver"}},
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "bucket"}},
				errorstack.Entry{
					Message: "Must end with a trailing slash",
					Path:    []any{"config", "subfolder"},
				},
			),
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
