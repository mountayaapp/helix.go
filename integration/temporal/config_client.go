package temporal

import (
	"net"
	"strings"
	"unicode"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	"go.temporal.io/sdk/converter"
)

// ConfigClient is used to configure a Temporal client: the client-only connection
// Connect registers as a dependency, and the client a worker built by New polls
// with.
type ConfigClient struct {

	// Name identifies the connection in health output, error context and
	// telemetry, where it is the temporal.name attribute.
	//
	// Connect registers the connection under it. New ignores it: a worker and the
	// client it dials are both named by ConfigWorker.Name.
	//
	// Default:
	//
	//   "temporal"
	Name string `json:"name"`

	// Address is the Temporal server to connect to: a host:port pair, or a gRPC
	// target such as "dns:///temporal.internal:7233".
	//
	// Default:
	//
	//   "127.0.0.1:7233"
	Address string `json:"address"`

	// Namespace sets the namespace to connect to.
	//
	// Default:
	//
	//   "default"
	Namespace string `json:"namespace"`

	// APIKey authenticates the client with an API key, which Temporal Cloud
	// accepts in place of mutual TLS. The key is sent as a bearer token on every
	// request, so setting it turns TLS on — with the system's root certificates —
	// even when TLS.Enabled is false.
	APIKey string `json:"-"`

	// DataConverter customizes the serialization of workflow and activity
	// arguments and results. Clients and workers of a namespace must agree on it.
	//
	// It also encodes the Event header described at PropagateFullEvent, so that a
	// payload codec configured here — encryption, compression — applies to the
	// header the way it applies to arguments.
	//
	// Default:
	//
	//   converter.GetDefaultDataConverter()
	DataConverter converter.DataConverter `json:"-"`

	// PropagateFullEvent sends the whole Event in the "event" header of the
	// workflows, activities, signals and child workflows this client starts,
	// rather than only the allow-listed identifiers event.Event.Propagated sends
	// as baggage: the event ID and name, the organization and tenant IDs, and the
	// application name and version.
	//
	// The full Event carries personal data — the user ID, IP address, user agent,
	// device and location — and Temporal persists headers in workflow history,
	// which the server retains for the namespace's retention period and shows to
	// anyone allowed to read that history. Enable it only when a worker needs those
	// fields and the namespace's retention and access rules are acceptable for
	// them; a DataConverter with an encrypting codec keeps them unreadable to the
	// server either way.
	//
	// Default:
	//
	//   false
	PropagateFullEvent bool `json:"propagate_full_event"`

	// TLS configures TLS to communicate with the Temporal server. When enabled and
	// ServerName is empty, the host of Address is verified.
	TLS integration.ConfigTLS `json:"tls"`
}

// sanitize sets default values - when applicable - and validates the
// configuration. Returns an error if configuration is not valid.
func (cfg *ConfigClient) sanitize() error {
	if entries := cfg.validate(); len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	return nil
}

// validate sets default values - when applicable - and returns the entries
// describing what is not valid, at paths rooted at "config".
//
// It is what sanitize reports, and what ConfigWorker absorbs under
// config.client rather than wrapping a whole error.
func (cfg *ConfigClient) validate() []errorstack.Entry {
	if cfg.Address == "" {
		cfg.Address = "127.0.0.1:7233"
	}

	if cfg.Namespace == "" {
		cfg.Namespace = "default"
	}

	var entries []errorstack.Entry
	if !validAddress(cfg.Address) {
		entries = append(entries, errorstack.Entry{
			Message: "Must be a host:port address or a gRPC target",
			Path:    []any{"config", "address"},
		})
	}

	if strings.ContainsFunc(cfg.Namespace, unicode.IsSpace) {
		entries = append(entries, errorstack.Entry{
			Message: "Must not contain whitespace",
			Path:    []any{"config", "namespace"},
		})
	}

	return append(entries, cfg.TLS.Sanitize()...)
}

// name returns the name the connection registers under, falling back to the
// integration's identifier when the ConfigClient leaves it empty.
func (cfg *ConfigClient) name() string {
	if cfg.Name != "" {
		return cfg.Name
	}

	return identifier
}

// dataConverter returns the DataConverter the client encodes with: the one
// configured, or the SDK's default.
func (cfg *ConfigClient) dataConverter() converter.DataConverter {
	if cfg.DataConverter != nil {
		return cfg.DataConverter
	}

	return converter.GetDefaultDataConverter()
}

// validAddress reports whether address is something the SDK can dial: a host:port
// pair, or a gRPC target URI such as "dns:///temporal.internal:7233", which the
// SDK hands to gRPC's resolvers as it is.
//
// Whitespace is refused in both: it is never part of an address, and it is what a
// value read from a file or an environment variable most often carries by
// mistake.
func validAddress(address string) bool {
	if strings.ContainsFunc(address, unicode.IsSpace) {
		return false
	}

	if strings.Contains(address, "://") {
		return true
	}

	_, port, err := net.SplitHostPort(address)
	return err == nil && port != ""
}
