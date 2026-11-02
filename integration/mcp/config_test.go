package mcp

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	nameEntry = errorstack.Entry{
		Message: "Must be set",
		Path:    []any{"config", "server_info", "name"},
	}

	versionEntry = errorstack.Entry{
		Message: "Must be set",
		Path:    []any{"config", "server_info", "version"},
	}

	registerEntry = errorstack.Entry{
		Message: "Must be set",
		Path:    []any{"config", "register"},
	}

	tlsPairEntry = errorstack.Entry{
		Message: "Must be set together; cert_pem and key_pem are required as a pair",
		Path:    []any{"config", "tls"},
	}

	oauthResourceIDEntry = errorstack.Entry{
		Message: "Must be set",
		Path:    []any{"config", "oauth", "resource_id"},
	}

	oauthInvalidResourceIDEntry = errorstack.Entry{
		Message: "Must be an absolute http or https URL with a clean path and no fragment",
		Path:    []any{"config", "oauth", "resource_id"},
	}

	oauthAuthServersEntry = errorstack.Entry{
		Message: "Must contain at least one authorization server",
		Path:    []any{"config", "oauth", "authorization_servers"},
	}

	oauthValidateTokenEntry = errorstack.Entry{
		Message: "Must be set",
		Path:    []any{"config", "oauth", "validate_token"},
	}

	composeMiddlewareEntry = errorstack.Entry{
		Message: "Must be set when OAuth compose mode is enabled",
		Path:    []any{"config", "middleware"},
	}
)

// validConfig returns a Config that passes sanitize, so tests can vary a single
// dimension at a time. Register is a non-nil no-op since it is required.
func validConfig() Config {
	return Config{
		ServerInfo: ServerInfo{Name: "test-server", Version: "v1.0.0"},
		Register:   func(_ *Server) {},
	}
}

// validOAuth returns an enabled, fully-configured OAuthResourceServer.
func validOAuth() OAuthResourceServer {
	return OAuthResourceServer{
		Enabled:              true,
		ResourceID:           "https://api.example.com",
		AuthorizationServers: []string{"https://auth.example.com"},
		ValidateToken: func(_ context.Context, _ string) (*TokenInfo, error) {
			return &TokenInfo{Subject: "user-1", Expiration: time.Now().Add(time.Hour)}, nil
		},
	}
}

// passthrough is a Config.Middleware that changes nothing.
func passthrough(next http.Handler) http.Handler {
	return next
}

func TestConfig_Sanitize(t *testing.T) {
	testcases := []struct {
		name        string
		cfg         Config
		wantErr     error
		wantAddress string
		wantPath    string
	}{
		{
			name:        "defaults are applied",
			cfg:         validConfig(),
			wantErr:     nil,
			wantAddress: ":8080",
			wantPath:    "/mcp",
		},
		{
			name: "custom address and path are preserved",
			cfg: func() Config {
				cfg := validConfig()
				cfg.Address = ":9090"
				cfg.Path = "/api/mcp"
				return cfg
			}(),
			wantErr:     nil,
			wantAddress: ":9090",
			wantPath:    "/api/mcp",
		},
		{
			name:    "empty config returns name, version and register errors",
			cfg:     Config{},
			wantErr: errorstack.NewValidation(nameEntry, versionEntry, registerEntry),
		},
		{
			name: "missing name returns name error",
			cfg: Config{
				ServerInfo: ServerInfo{Version: "v1.0.0"},
				Register:   func(_ *Server) {},
			},
			wantErr: errorstack.NewValidation(nameEntry),
		},
		{
			name: "missing version returns version error",
			cfg: Config{
				ServerInfo: ServerInfo{Name: "test-server"},
				Register:   func(_ *Server) {},
			},
			wantErr: errorstack.NewValidation(versionEntry),
		},
		{
			name: "missing register returns register error",
			cfg: Config{
				ServerInfo: ServerInfo{Name: "test-server", Version: "v1.0.0"},
			},
			wantErr: errorstack.NewValidation(registerEntry),
		},
		{
			name: "invalid TLS pair returns TLS error",
			cfg: func() Config {
				cfg := validConfig()
				cfg.TLS = integration.ConfigTLS{Enabled: true, CertPEM: []byte("cert")}
				return cfg
			}(),
			wantErr: errorstack.NewValidation(tlsPairEntry),
		},
		{
			name: "negative durations and body limit are rejected",
			cfg: func() Config {
				cfg := validConfig()
				cfg.ReadHeaderTimeout = -1
				cfg.IdleTimeout = -1
				cfg.ReadTimeout = -1
				cfg.SessionTimeout = -1
				cfg.MaxRequestBodyBytes = -1
				return cfg
			}(),
			wantErr: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be a positive duration", Path: []any{"config", "read_header_timeout"}},
				errorstack.Entry{Message: "Must be a positive duration", Path: []any{"config", "idle_timeout"}},
				errorstack.Entry{Message: "Must be a positive duration", Path: []any{"config", "read_timeout"}},
				errorstack.Entry{Message: "Must be a positive duration", Path: []any{"config", "session_timeout"}},
				errorstack.Entry{Message: "Must be a positive number", Path: []any{"config", "max_request_body_bytes"}},
			),
		},
		{
			name: "empty allowed host is rejected",
			cfg: func() Config {
				cfg := validConfig()
				cfg.AllowedHosts = []string{"api.example.com", " "}
				return cfg
			}(),
			wantErr: errorstack.NewValidation(errorstack.Entry{
				Message: "Must not be empty",
				Path:    []any{"config", "allowed_hosts", 1},
			}),
		},
		{
			name: "disabled OAuth with empty fields passes",
			cfg: func() Config {
				cfg := validConfig()
				cfg.OAuth = OAuthResourceServer{}
				return cfg
			}(),
			wantErr:     nil,
			wantAddress: ":8080",
			wantPath:    "/mcp",
		},
		{
			name: "enabled and fully configured OAuth passes",
			cfg: func() Config {
				cfg := validConfig()
				cfg.OAuth = validOAuth()
				return cfg
			}(),
			wantErr:     nil,
			wantAddress: ":8080",
			wantPath:    "/mcp",
		},
		{
			name: "enabled OAuth with empty fields returns all OAuth errors",
			cfg: func() Config {
				cfg := validConfig()
				cfg.OAuth = OAuthResourceServer{Enabled: true}
				return cfg
			}(),
			wantErr: errorstack.NewValidation(oauthResourceIDEntry, oauthAuthServersEntry, oauthValidateTokenEntry),
		},
		{
			name: "compose-mode OAuth with middleware and without validate token passes",
			cfg: func() Config {
				cfg := validConfig()
				oauth := validOAuth()
				oauth.Compose = true
				oauth.ValidateToken = nil
				cfg.OAuth = oauth
				cfg.Middleware = passthrough
				return cfg
			}(),
			wantErr:     nil,
			wantAddress: ":8080",
			wantPath:    "/mcp",
		},
		{
			name: "compose-mode OAuth without middleware is rejected",
			cfg: func() Config {
				cfg := validConfig()
				oauth := validOAuth()
				oauth.Compose = true
				cfg.OAuth = oauth
				return cfg
			}(),
			wantErr: errorstack.NewValidation(composeMiddlewareEntry),
		},
		{
			name: "compose-mode OAuth still requires resource id and authorization servers",
			cfg: func() Config {
				cfg := validConfig()
				cfg.OAuth = OAuthResourceServer{Enabled: true, Compose: true}
				cfg.Middleware = passthrough
				return cfg
			}(),
			wantErr: errorstack.NewValidation(oauthResourceIDEntry, oauthAuthServersEntry),
		},
		{
			name: "path equal to the protected resource metadata path is rejected",
			cfg: func() Config {
				cfg := validConfig()
				cfg.OAuth = validOAuth()
				cfg.OAuth.ResourceID = "https://api.example.com/mcp"
				cfg.Path = "/.well-known/oauth-protected-resource/mcp"
				return cfg
			}(),
			wantErr: errorstack.NewValidation(errorstack.Entry{
				Message: "Must not be the protected resource metadata path",
				Path:    []any{"config", "path"},
			}),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			err := cfg.sanitize()

			assert.Equal(t, tc.wantErr, err)
			if tc.wantAddress != "" {
				assert.Equal(t, tc.wantAddress, cfg.Address)
			}
			if tc.wantPath != "" {
				assert.Equal(t, tc.wantPath, cfg.Path)
			}
		})
	}
}

func TestConfig_Sanitize_Defaults(t *testing.T) {
	cfg := validConfig()
	require.NoError(t, cfg.sanitize())

	assert.Equal(t, integration.DefaultReadHeaderTimeout, cfg.ReadHeaderTimeout)
	assert.Equal(t, integration.DefaultIdleTimeout, cfg.IdleTimeout)
	assert.Zero(t, cfg.ReadTimeout, "ReadTimeout is opt-in: it would end streams")
	assert.Equal(t, int64(mcpsdk.DefaultMaxRequestBodyBytes), cfg.MaxRequestBodyBytes)
	assert.Zero(t, cfg.SessionTimeout, "a stateless server has no sessions to time out")

	stateful := validConfig()
	stateful.Stateful = true
	require.NoError(t, stateful.sanitize())
	assert.Equal(t, DefaultSessionTimeout, stateful.SessionTimeout)
}

func TestConfig_Sanitize_Path(t *testing.T) {
	testcases := []struct {
		path    string
		wantMsg string
	}{
		{path: "mcp", wantMsg: `Must start with "/"`},
		{path: "/health", wantMsg: "Must not be a health probe path"},
		{path: "/ready", wantMsg: "Must not be a health probe path"},
		{path: "/mcp/{id}", wantMsg: "Must be a clean path without braces or whitespace"},
		{path: "/m cp", wantMsg: "Must be a clean path without braces or whitespace"},
		{path: "/api/../mcp", wantMsg: "Must be a clean path without braces or whitespace"},
		{path: "/api//mcp", wantMsg: "Must be a clean path without braces or whitespace"},
		{path: "/"},
		{path: "/api/mcp/"},
	}

	for _, tc := range testcases {
		t.Run(tc.path, func(t *testing.T) {
			cfg := validConfig()
			cfg.Path = tc.path

			err := cfg.sanitize()
			if tc.wantMsg == "" {
				assert.NoError(t, err)
				return
			}

			assert.Equal(t, errorstack.NewValidation(errorstack.Entry{
				Message: tc.wantMsg,
				Path:    []any{"config", "path"},
			}), err)
		})
	}
}

func TestConfig_Sanitize_ResourceID(t *testing.T) {
	for _, resourceID := range []string{
		"api.example.com",
		"/mcp",
		"ftp://api.example.com",
		"https://api.example.com/mcp#fragment",
		"https://api.example.com/{mcp}",
		"https://api.example.com/%zz",
		"https://api.example.com/api/../mcp",
	} {
		t.Run(resourceID, func(t *testing.T) {
			cfg := validConfig()
			cfg.OAuth = validOAuth()
			cfg.OAuth.ResourceID = resourceID

			assert.Equal(t, errorstack.NewValidation(oauthInvalidResourceIDEntry), cfg.sanitize())
		})
	}
}

func TestConfig_Name(t *testing.T) {
	cfg := validConfig()
	assert.Equal(t, "test-server", cfg.name(), "falls back to the MCP server's name")

	cfg.Name = "public-mcp"
	assert.Equal(t, "public-mcp", cfg.name())

	assert.Equal(t, identifier, (&Config{}).name())
}
