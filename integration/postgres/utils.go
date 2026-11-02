package postgres

import (
	"context"
	"crypto/tls"
	"net/url"
	"time"

	"github.com/mountayaapp/helix.go/integration"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pre-computed attribute keys to avoid allocations on every call.
//
// The database is not among them: it is fixed for the life of the connection, so
// it is handed to the Instrument once and stamped by it rather than re-set on
// every span.
var (
	attrKeyQuery           = integration.Key(identifier, "query")
	attrKeyStatement       = integration.Key(identifier, "statement")
	attrKeyAlreadyPrepared = integration.Key(identifier, "already_prepared")
	attrKeyBatchSize       = integration.Key(identifier, "batch.size")
	attrKeyTable           = integration.Key(identifier, "table")
	attrKeyColumns         = integration.Key(identifier, "columns")
	attrKeyRows            = integration.Key(identifier, "rows")
	attrKeyPoolState       = integration.Key(identifier, "pool.state")
)

// buildAddress builds the PostgreSQL connection URI from the Config. Credentials
// and database name are percent-encoded, so values holding characters that are
// significant in a URI - such as "@" in a password - are preserved as-is.
//
// The sslmode is always explicit, so that neither the driver's default nor a
// PGSSLMODE in the environment decides it. "disable" is plaintext only. "require"
// is TLS only: it leaves no plaintext attempt for the driver to fall back to,
// and requireTLS then replaces the TLS settings it implies with the Config's.
func buildAddress(cfg *Config) string {
	sslmode := "disable"
	if cfg.TLS.Enabled {
		sslmode = "require"
	}

	query := url.Values{"sslmode": {sslmode}}
	if cfg.ApplicationName != "" {
		query.Set("application_name", cfg.ApplicationName)
	}

	// A run-time parameter of every connection, the dedicated one of a Listener
	// included, which copies the pool's connection configuration.
	if cfg.TimeZone != "" {
		query.Set("timezone", cfg.TimeZone)
	}

	address := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     cfg.Address,
		Path:     "/" + cfg.Database,
		RawQuery: query.Encode(),
	}

	return address.String()
}

// poolConfig builds the driver's pool configuration from the Config, which must
// already be sanitized. tlsConfig is the Config's TLS converted by
// integration.ConfigTLS.ToStandardTLS, and is nil when TLS is disabled.
//
// The error is the driver failing to parse the connection URI, which can only be
// a problem with the Config, or a TimeZone the time zone database does not know,
// which sanitize has refused already.
func poolConfig(cfg *Config, tlsConfig *tls.Config) (*pgxpool.Config, error) {
	opts, err := pgxpool.ParseConfig(buildAddress(cfg))
	if err != nil {
		return nil, err
	}

	if tlsConfig != nil {
		requireTLS(&opts.ConnConfig.Config, tlsConfig)
	}

	// Zero leaves the default ParseConfig set, which is what the Config documents.
	if cfg.MaxConns > 0 {
		opts.MaxConns = cfg.MaxConns
	}

	if cfg.MinConns > 0 {
		opts.MinConns = cfg.MinConns
	}

	if cfg.MaxConnLifetime > 0 {
		opts.MaxConnLifetime = cfg.MaxConnLifetime
	}

	if cfg.MaxConnIdleTime > 0 {
		opts.MaxConnIdleTime = cfg.MaxConnIdleTime
	}

	if cfg.HealthCheckPeriod > 0 {
		opts.HealthCheckPeriod = cfg.HealthCheckPeriod
	}

	if cfg.ConnectTimeout > 0 {
		opts.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	}

	// The session's TimeZone is set by buildAddress; what is left is the location
	// the driver scans a timestamptz into, which is per connection, on its type map.
	if cfg.TimeZone != "" {
		location, err := time.LoadLocation(cfg.TimeZone)
		if err != nil {
			return nil, err
		}

		opts.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
			scanTimestamptzIn(conn.TypeMap(), location)
			return nil
		}
	}

	return opts, nil
}

// scanTimestamptzIn makes m scan every timestamptz into location: alone, in an
// array, in a range, in an array of ranges and in a multirange, which are the
// types the driver builds on timestamptz. It changes the location a scanned value
// is expressed in, never the instant, and nothing about how a value is sent.
func scanTimestamptzIn(m *pgtype.Map, location *time.Location) {
	timestamptz := &pgtype.Type{Name: "timestamptz", OID: pgtype.TimestamptzOID, Codec: &pgtype.TimestamptzCodec{ScanLocation: location}}
	tstzrange := &pgtype.Type{Name: "tstzrange", OID: pgtype.TstzrangeOID, Codec: &pgtype.RangeCodec{ElementType: timestamptz}}

	m.RegisterType(timestamptz)
	m.RegisterType(&pgtype.Type{Name: "_timestamptz", OID: pgtype.TimestamptzArrayOID, Codec: &pgtype.ArrayCodec{ElementType: timestamptz}})
	m.RegisterType(tstzrange)
	m.RegisterType(&pgtype.Type{Name: "_tstzrange", OID: pgtype.TstzrangeArrayOID, Codec: &pgtype.ArrayCodec{ElementType: tstzrange}})
	m.RegisterType(&pgtype.Type{Name: "tstzmultirange", OID: pgtype.TstzmultirangeOID, Codec: &pgtype.MultirangeCodec{ElementType: tstzrange}})
}

// requireTLS hands every connection attempt in connConfig — the primary one and
// each fallback — the Config's TLS settings, so that none of them is plaintext.
//
// This is the whole of the TLS guarantee, and the reason it is spelled out per
// attempt: pgx tries its fallbacks in order on any failure that is not an error
// from the server, and under its default sslmode one of them is plaintext. Set
// on the primary attempt only, a handshake that failed — on a certificate that
// did not verify, say — would carry on in plaintext without a word.
//
// pgx hands the tls.Config to crypto/tls as it is, and ToStandardTLS leaves
// ServerName empty when the Config does not set one. Each attempt therefore gets
// the host it dials as its ServerName: without one, a handshake that verifies the
// certificate cannot succeed, and with one the server receives SNI, which some
// hosted PostgreSQL offerings route on.
func requireTLS(connConfig *pgconn.Config, tlsConfig *tls.Config) {
	connConfig.TLSConfig = tlsFor(tlsConfig, connConfig.Host)
	for _, fallback := range connConfig.Fallbacks {
		fallback.TLSConfig = tlsFor(tlsConfig, fallback.Host)
	}
}

// tlsFor returns tlsConfig for an attempt dialing host: tlsConfig itself when it
// names a server, and otherwise a copy naming host.
func tlsFor(tlsConfig *tls.Config, host string) *tls.Config {
	if tlsConfig.ServerName != "" {
		return tlsConfig
	}

	perHost := tlsConfig.Clone()
	perHost.ServerName = host

	return perHost
}
