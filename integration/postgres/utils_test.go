package postgres

import (
	"crypto/tls"
	"testing"
	"time"

	"github.com/mountaya/helix.go/integration"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func TestPreComputedAttributeKeys(t *testing.T) {
	assert.Equal(t, attribute.Key("postgres.query"), attrKeyQuery)
	assert.Equal(t, attribute.Key("postgres.statement"), attrKeyStatement)
	assert.Equal(t, attribute.Key("postgres.already_prepared"), attrKeyAlreadyPrepared)
	assert.Equal(t, attribute.Key("postgres.batch.size"), attrKeyBatchSize)
	assert.Equal(t, attribute.Key("postgres.table"), attrKeyTable)
	assert.Equal(t, attribute.Key("postgres.columns"), attrKeyColumns)
	assert.Equal(t, attribute.Key("postgres.rows"), attrKeyRows)
	assert.Equal(t, attribute.Key("postgres.pool.state"), attrKeyPoolState)
}

func TestBuildAddress(t *testing.T) {
	testcases := []struct {
		name     string
		config   Config
		expected string
	}{
		{
			name: "plain credentials",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "app",
				User:     "helix",
				Password: "s3cret",
			},
			expected: "postgres://helix:s3cret@127.0.0.1:5432/app?sslmode=disable",
		},
		{
			name: "at sign in password is escaped",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "app",
				User:     "helix",
				Password: "p@ssword",
			},
			expected: "postgres://helix:p%40ssword@127.0.0.1:5432/app?sslmode=disable",
		},
		{
			name: "space in password is escaped",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "app",
				User:     "helix",
				Password: "pa ss",
			},
			expected: "postgres://helix:pa%20ss@127.0.0.1:5432/app?sslmode=disable",
		},
		{
			name: "time zone is a run-time parameter",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "app",
				User:     "helix",
				Password: "s3cret",
				TimeZone: "Europe/Paris",
			},
			expected: "postgres://helix:s3cret@127.0.0.1:5432/app?sslmode=disable&timezone=Europe%2FParis",
		},
		{
			name: "number sign in password is escaped",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "app",
				User:     "helix",
				Password: "pa#ss",
			},
			expected: "postgres://helix:pa%23ss@127.0.0.1:5432/app?sslmode=disable",
		},
		{
			name: "slash in password is escaped",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "app",
				User:     "helix",
				Password: "pa/ss",
			},
			expected: "postgres://helix:pa%2Fss@127.0.0.1:5432/app?sslmode=disable",
		},
		{
			name: "percent sign in password is escaped",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "app",
				User:     "helix",
				Password: "pa%ss",
			},
			expected: "postgres://helix:pa%25ss@127.0.0.1:5432/app?sslmode=disable",
		},
		{
			name: "at sign in user is escaped",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "app",
				User:     "he@lix",
				Password: "s3cret",
			},
			expected: "postgres://he%40lix:s3cret@127.0.0.1:5432/app?sslmode=disable",
		},
		{
			name: "space in database is escaped",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "my app",
				User:     "helix",
				Password: "s3cret",
			},
			expected: "postgres://helix:s3cret@127.0.0.1:5432/my%20app?sslmode=disable",
		},
		{
			name: "bracketed IPv6 address is preserved",
			config: Config{
				Address:  "[::1]:5432",
				Database: "app",
				User:     "helix",
				Password: "s3cret",
			},
			expected: "postgres://helix:s3cret@[::1]:5432/app?sslmode=disable",
		},
		{
			name: "enabled TLS requires TLS",
			config: Config{
				Address:  "127.0.0.1:5432",
				Database: "app",
				User:     "helix",
				Password: "s3cret",
				TLS:      integration.ConfigTLS{Enabled: true},
			},
			expected: "postgres://helix:s3cret@127.0.0.1:5432/app?sslmode=require",
		},
		{
			name: "application name is escaped",
			config: Config{
				Address:         "127.0.0.1:5432",
				Database:        "app",
				User:            "helix",
				Password:        "s3cret",
				ApplicationName: "billing & reports",
			},
			expected: "postgres://helix:s3cret@127.0.0.1:5432/app?application_name=billing+%26+reports&sslmode=disable",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, buildAddress(&tc.config))
		})
	}
}

// TestPoolConfig_TLS pins the TLS guarantee on the configuration the driver is
// handed, connection attempt by connection attempt: with TLS enabled none of them
// is plaintext, and each one knows the server name to verify.
//
// It is the regression test for a plaintext fallback. The TLS settings used to be
// set on the primary attempt only, leaving the plaintext fallback of the driver's
// default sslmode in place, and with no ServerName the handshake always failed —
// so every connection silently landed on the fallback.
func TestPoolConfig_TLS(t *testing.T) {

	// The environment must not be able to bring the plaintext fallback back.
	t.Setenv("PGSSLMODE", "prefer")

	base := &tls.Config{}

	t.Run("enabled TLS leaves no plaintext attempt", func(t *testing.T) {
		cfg := Config{
			Address:  "db.example.com:5432",
			Database: "app",
			User:     "helix",
			Password: "s3cret",
			TLS:      integration.ConfigTLS{Enabled: true},
		}

		opts, err := poolConfig(&cfg, base)
		require.NoError(t, err)

		require.NotNil(t, opts.ConnConfig.TLSConfig)
		assert.Equal(t, "db.example.com", opts.ConnConfig.TLSConfig.ServerName,
			"ServerName must default to the dialed host, or verification cannot succeed")
		assert.Empty(t, opts.ConnConfig.Fallbacks, "a single host must have no fallback attempt")
		assert.Empty(t, base.ServerName, "the shared tls.Config must not be modified")
	})

	t.Run("every host of a multi-host address uses TLS with its own server name", func(t *testing.T) {
		cfg := Config{
			Address:  "primary.example.com:5432,replica.example.com:5433",
			Database: "app",
			User:     "helix",
			Password: "s3cret",
			TLS:      integration.ConfigTLS{Enabled: true},
		}

		opts, err := poolConfig(&cfg, base)
		require.NoError(t, err)

		require.NotNil(t, opts.ConnConfig.TLSConfig)
		assert.Equal(t, "primary.example.com", opts.ConnConfig.TLSConfig.ServerName)

		require.Len(t, opts.ConnConfig.Fallbacks, 1)
		require.NotNil(t, opts.ConnConfig.Fallbacks[0].TLSConfig)
		assert.Equal(t, "replica.example.com", opts.ConnConfig.Fallbacks[0].TLSConfig.ServerName)
	})

	t.Run("a server name set in the Config is kept", func(t *testing.T) {
		cfg := Config{
			Address:  "10.0.0.7:5432",
			Database: "app",
			User:     "helix",
			Password: "s3cret",
			TLS:      integration.ConfigTLS{Enabled: true},
		}

		named := &tls.Config{ServerName: "db.internal"}

		opts, err := poolConfig(&cfg, named)
		require.NoError(t, err)

		assert.Same(t, named, opts.ConnConfig.TLSConfig)
	})

	t.Run("disabled TLS is plaintext only", func(t *testing.T) {
		cfg := Config{
			Address:  "db.example.com:5432",
			Database: "app",
			User:     "helix",
			Password: "s3cret",
		}

		opts, err := poolConfig(&cfg, nil)
		require.NoError(t, err)

		assert.Nil(t, opts.ConnConfig.TLSConfig,
			"the driver's default would attempt TLS without verifying the certificate first")
		assert.Empty(t, opts.ConnConfig.Fallbacks)
	})
}

func TestPoolConfig_Pool(t *testing.T) {
	t.Run("zero values keep the driver's defaults", func(t *testing.T) {
		cfg := Config{Address: "127.0.0.1:5432", Database: "app", User: "helix", Password: "s3cret"}

		opts, err := poolConfig(&cfg, nil)
		require.NoError(t, err)

		assert.Positive(t, opts.MaxConns)
		assert.Zero(t, opts.MinConns)
		assert.Equal(t, time.Hour, opts.MaxConnLifetime)
		assert.Equal(t, 30*time.Minute, opts.MaxConnIdleTime)
		assert.Equal(t, time.Minute, opts.HealthCheckPeriod)
		assert.Zero(t, opts.ConnConfig.ConnectTimeout)
		assert.NotContains(t, opts.ConnConfig.RuntimeParams, "application_name")
	})

	t.Run("set values are applied", func(t *testing.T) {
		cfg := Config{
			Address:           "127.0.0.1:5432",
			Database:          "app",
			User:              "helix",
			Password:          "s3cret",
			ApplicationName:   "billing",
			MaxConns:          20,
			MinConns:          2,
			MaxConnLifetime:   10 * time.Minute,
			MaxConnIdleTime:   time.Minute,
			HealthCheckPeriod: 15 * time.Second,
			ConnectTimeout:    3 * time.Second,
		}

		opts, err := poolConfig(&cfg, nil)
		require.NoError(t, err)

		assert.Equal(t, int32(20), opts.MaxConns)
		assert.Equal(t, int32(2), opts.MinConns)
		assert.Equal(t, 10*time.Minute, opts.MaxConnLifetime)
		assert.Equal(t, time.Minute, opts.MaxConnIdleTime)
		assert.Equal(t, 15*time.Second, opts.HealthCheckPeriod)
		assert.Equal(t, 3*time.Second, opts.ConnConfig.ConnectTimeout)
		assert.Equal(t, "billing", opts.ConnConfig.RuntimeParams["application_name"])
	})

	t.Run("an address the driver cannot parse is an error", func(t *testing.T) {
		cfg := Config{Address: "127.0.0.1:port", Database: "app", User: "helix", Password: "s3cret"}

		_, err := poolConfig(&cfg, nil)
		assert.Error(t, err)
	})
}

func TestPoolConfig_TimeZone(t *testing.T) {
	t.Run("no time zone leaves the session and the scans to their defaults", func(t *testing.T) {
		cfg := Config{Address: "127.0.0.1:5432", Database: "app", User: "helix", Password: "s3cret"}

		opts, err := poolConfig(&cfg, nil)
		require.NoError(t, err)

		assert.NotContains(t, opts.ConnConfig.RuntimeParams, "timezone")
		assert.Nil(t, opts.AfterConnect)
	})

	t.Run("a time zone sets the session's and registers the scan location", func(t *testing.T) {
		cfg := Config{Address: "127.0.0.1:5432", Database: "app", User: "helix", Password: "s3cret", TimeZone: "Europe/Paris"}

		opts, err := poolConfig(&cfg, nil)
		require.NoError(t, err)

		assert.Equal(t, "Europe/Paris", opts.ConnConfig.RuntimeParams["timezone"])
		assert.NotNil(t, opts.AfterConnect)
	})

	t.Run("a time zone the time zone database does not know is an error", func(t *testing.T) {
		cfg := Config{Address: "127.0.0.1:5432", Database: "app", User: "helix", Password: "s3cret", TimeZone: "Mars/Olympus_Mons"}

		_, err := poolConfig(&cfg, nil)
		assert.Error(t, err)
	})
}

func TestScanTimestamptzIn(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	require.NoError(t, err)

	m := pgtype.NewMap()
	scanTimestamptzIn(m, paris)

	instant := time.Date(2026, time.January, 15, 11, 0, 0, 0, time.UTC)

	t.Run("a timestamptz scans in the location, at the same instant", func(t *testing.T) {
		encoded, err := m.Encode(pgtype.TimestamptzOID, pgtype.BinaryFormatCode, instant, nil)
		require.NoError(t, err)

		var scanned time.Time
		require.NoError(t, m.Scan(pgtype.TimestamptzOID, pgtype.BinaryFormatCode, encoded, &scanned))

		assert.Equal(t, paris, scanned.Location())
		assert.True(t, scanned.Equal(instant))
		assert.Equal(t, 12, scanned.Hour())
	})

	t.Run("an array of timestamptz scans in the location", func(t *testing.T) {
		encoded, err := m.Encode(pgtype.TimestamptzArrayOID, pgtype.BinaryFormatCode, []time.Time{instant, instant.Add(time.Hour)}, nil)
		require.NoError(t, err)

		var scanned []time.Time
		require.NoError(t, m.Scan(pgtype.TimestamptzArrayOID, pgtype.BinaryFormatCode, encoded, &scanned))

		require.Len(t, scanned, 2)
		for _, value := range scanned {
			assert.Equal(t, paris, value.Location())
		}
		assert.True(t, scanned[1].Equal(instant.Add(time.Hour)))
	})

	t.Run("a range of timestamptz scans in the location", func(t *testing.T) {
		value := pgtype.Range[pgtype.Timestamptz]{
			Lower:     pgtype.Timestamptz{Time: instant, Valid: true},
			Upper:     pgtype.Timestamptz{Time: instant.Add(time.Hour), Valid: true},
			LowerType: pgtype.Inclusive,
			UpperType: pgtype.Exclusive,
			Valid:     true,
		}
		encoded, err := m.Encode(pgtype.TstzrangeOID, pgtype.BinaryFormatCode, value, nil)
		require.NoError(t, err)

		var scanned pgtype.Range[pgtype.Timestamptz]
		require.NoError(t, m.Scan(pgtype.TstzrangeOID, pgtype.BinaryFormatCode, encoded, &scanned))

		assert.Equal(t, paris, scanned.Lower.Time.Location())
		assert.Equal(t, paris, scanned.Upper.Time.Location())
	})

	t.Run("the array of ranges and the multirange are built on the same element", func(t *testing.T) {
		tstzrange, ok := m.TypeForOID(pgtype.TstzrangeOID)
		require.True(t, ok)

		array, ok := m.TypeForOID(pgtype.TstzrangeArrayOID)
		require.True(t, ok)
		assert.Same(t, tstzrange, array.Codec.(*pgtype.ArrayCodec).ElementType)

		multirange, ok := m.TypeForOID(pgtype.TstzmultirangeOID)
		require.True(t, ok)
		assert.Same(t, tstzrange, multirange.Codec.(*pgtype.MultirangeCodec).ElementType)
	})
}
