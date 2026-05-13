package integration

import (
	"crypto/tls"
	"crypto/x509"

	"github.com/mountayaapp/helix.go/errorstack"
)

/*
ConfigTLS is the common configuration for TLS across all integrations.
*/
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
	// This is not required by all integrations.
	RootCAPEMs [][]byte `json:"-"`
}

/*
Sanitize sets default values - if applicable - and validates the configuration.
Returns validation entries if configuration is not valid. This doesn't return
a standard error since this function shall only be called by integrations,
which collect entries from many sources before producing a final
errorstack.NewValidation:

	entries = append(entries, cfg.TLS.Sanitize()...)
*/
func (cfg *ConfigTLS) Sanitize() []errorstack.Entry {
	var entries []errorstack.Entry
	if !cfg.Enabled {
		return entries
	}

	cfg.CertPEM = normalizePEM(cfg.CertPEM)
	cfg.KeyPEM = normalizePEM(cfg.KeyPEM)
	for i := range cfg.RootCAPEMs {
		cfg.RootCAPEMs[i] = normalizePEM(cfg.RootCAPEMs[i])
	}

	if (len(cfg.CertPEM) > 0 && len(cfg.KeyPEM) == 0) || (len(cfg.CertPEM) == 0 && len(cfg.KeyPEM) > 0) {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set together; cert_pem and key_pem are required as a pair",
			Path:    []any{"config", "tls"},
		})
	}

	return entries
}

/*
ToStandardTLS tries to return a Go standard *tls.Config. Returns validation
entries if configuration is not valid. This doesn't return a standard error
since this function shall only be called by integrations.
*/
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
