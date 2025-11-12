package clickhouse

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_Sanitize(t *testing.T) {
	testcases := []struct {
		before Config
		after  Config
		err    error
	}{
		{
			before: Config{},
			after: Config{
				Address:  "127.0.0.1:9000",
				Database: "default",
				User:     "default",
				Password: "default",
			},
			err: nil,
		},
	}

	for _, tc := range testcases {
		err := tc.before.sanitize()

		assert.Equal(t, tc.before, tc.after)
		assert.Equal(t, tc.err, err)
	}
}
