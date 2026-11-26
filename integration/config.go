package integration

import (
	"crypto/tls"
	"crypto/x509"

	"github.com/mountaya/helix.go/errorstack"
)

// ConfigTLS is the common configuration for TLS across all integrations.
type ConfigTLS struct {

	// Enabled enables TLS for the integration. When disabled, other fields are
	// ignored and can be empty.
	Enabled bool `json:"enabled"`

	// ServerName is used to verify the hostname on the returned certificates. It
	// is also included in the client's handshake to support virtual hosting unless
	// it is an IP address.
	ServerName string `json:"server_name,omitempty"`

	// InsecureSkipVerify controls whether a client verifies the server's certificate
	// chain and host name. If InsecureSkipVerify is true, crypto/tls accepts any
	// certificate presented by the server and any host name in that certificate.
	// In this mode, TLS is susceptible to machine-in-the-middle attacks unless
	// custom verification is used.
	InsecureSkipVerify bool `json:"insecure_skip_verify"`

	// CertPEM is the PEM-encoded certificate bytes.
	CertPEM []byte `json:"-"`

	// KeyPEM is the PEM-encoded private key bytes.
	KeyPEM []byte `json:"-"`

	// RootCAPEMs allows to provide the RootCAs pool from PEM-encoded certificates.
	// This is not required by all integrations. Clients only.
	RootCAPEMs [][]byte `json:"-"`

	// ClientCAPEMs enables mutual TLS on a server: clients must then present a
	// certificate signed by one of these PEM-encoded authorities. Servers only.
	ClientCAPEMs [][]byte `json:"-"`
}

// Sanitize sets default values - if applicable - and validates the configuration.
// Returns validation entries if configuration is not valid. This doesn't return
// a standard error since this function shall only be called by integrations,
// which collect entries from many sources before producing a final
// errorstack.NewValidation:
//
//	entries = append(entries, cfg.TLS.Sanitize()...)
func (cfg *ConfigTLS) Sanitize() []errorstack.Entry {
	var entries []errorstack.Entry
	if !cfg.Enabled {
		return entries
	}

	// The PEM slices are rebuilt rather than normalized in place: a Config is
	// passed by value, but its slices still share their backing arrays with the
	// caller's, which must not change under them.
	cfg.CertPEM = normalizePEM(cfg.CertPEM)
	cfg.KeyPEM = normalizePEM(cfg.KeyPEM)
	cfg.RootCAPEMs = normalizePEMs(cfg.RootCAPEMs)
	cfg.ClientCAPEMs = normalizePEMs(cfg.ClientCAPEMs)

	if (len(cfg.CertPEM) > 0 && len(cfg.KeyPEM) == 0) || (len(cfg.CertPEM) == 0 && len(cfg.KeyPEM) > 0) {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set together; cert_pem and key_pem are required as a pair",
			Path:    []any{"config", "tls"},
		})
	}

	return entries
}

// ToStandardTLS tries to return a Go standard *tls.Config for a client. Returns
// validation entries if configuration is not valid. This doesn't return a standard
// error since this function shall only be called by integrations.
//
// ServerName is left empty when the Config does not set it. Most drivers derive it
// from the address they dial; one that does not must set it before use, since a
// client without it cannot verify the server's certificate.
func (cfg *ConfigTLS) ToStandardTLS() (*tls.Config, []errorstack.Entry) {
	var entries []errorstack.Entry
	if !cfg.Enabled {
		return nil, entries
	}

	var cert tls.Certificate
	if len(cfg.CertPEM) > 0 && len(cfg.KeyPEM) > 0 {
		var err error

		cert, err = tls.X509KeyPair(cfg.CertPEM, cfg.KeyPEM)
		if err != nil {
			entries = append(entries, errorstack.Entry{
				Message: "Must be a valid X509 key pair",
				Path:    []any{"config", "tls"},
			})

			return nil, entries
		}
	}

	tlsConfig := &tls.Config{
		ServerName:         cfg.ServerName,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	}

	if len(cert.Certificate) > 0 {
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	if len(cfg.RootCAPEMs) == 0 {
		return tlsConfig, nil
	}

	caCertPool := x509.NewCertPool()
	for _, ca := range cfg.RootCAPEMs {
		ok := caCertPool.AppendCertsFromPEM(ca)
		if !ok {
			entries = append(entries, errorstack.Entry{
				Message: "Must be a valid PEM-encoded root certificate",
				Path:    []any{"config", "tls", "root_ca_pems"},
			})
		}
	}

	if len(entries) > 0 {
		return nil, entries
	}

	tlsConfig.RootCAs = caCertPool
	return tlsConfig, nil
}

// ToServerTLS returns the *tls.Config a server listens with. Returns validation
// entries if configuration is not valid.
//
// A server needs a certificate, so CertPEM and KeyPEM are required when TLS is
// enabled. ClientCAPEMs, when set, turns on mutual TLS. The client-only fields —
// ServerName, InsecureSkipVerify, RootCAPEMs — mean nothing to a server and are
// ignored. Returns nil and no entries when TLS is disabled.
func (cfg *ConfigTLS) ToServerTLS() (*tls.Config, []errorstack.Entry) {
	if !cfg.Enabled {
		return nil, nil
	}

	if len(cfg.CertPEM) == 0 || len(cfg.KeyPEM) == 0 {
		return nil, []errorstack.Entry{{
			Message: "Must be set; a server requires cert_pem and key_pem",
			Path:    []any{"config", "tls"},
		}}
	}

	cert, err := tls.X509KeyPair(cfg.CertPEM, cfg.KeyPEM)
	if err != nil {
		return nil, []errorstack.Entry{{
			Message: "Must be a valid X509 key pair",
			Path:    []any{"config", "tls"},
		}}
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	if len(cfg.ClientCAPEMs) == 0 {
		return tlsConfig, nil
	}

	pool := x509.NewCertPool()
	for _, ca := range cfg.ClientCAPEMs {
		if !pool.AppendCertsFromPEM(ca) {
			return nil, []errorstack.Entry{{
				Message: "Must be a valid PEM-encoded certificate authority",
				Path:    []any{"config", "tls", "client_ca_pems"},
			}}
		}
	}

	tlsConfig.ClientCAs = pool
	tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert

	return tlsConfig, nil
}
