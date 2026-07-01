package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	"github.com/stretchr/testify/assert"
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

	oauthResourceIdEntry = errorstack.Entry{
		Message: "Must be set",
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
		ResourceId:           "https://api.example.com",
		AuthorizationServers: []string{"https://auth.example.com"},
		ValidateToken: func(_ context.Context, _ string) (any, time.Time, error) {
			return nil, time.Now().Add(time.Hour), nil
		},
	}
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
			wantErr: errorstack.NewValidation(oauthResourceIdEntry, oauthAuthServersEntry, oauthValidateTokenEntry),
		},
		{
			name: "compose-mode OAuth without validate token passes",
			cfg: func() Config {
				cfg := validConfig()
				oauth := validOAuth()
				oauth.Compose = true
				oauth.ValidateToken = nil
				cfg.OAuth = oauth
				return cfg
			}(),
			wantErr:     nil,
			wantAddress: ":8080",
			wantPath:    "/mcp",
		},
		{
			name: "compose-mode OAuth still requires resource id and authorization servers",
			cfg: func() Config {
				cfg := validConfig()
				cfg.OAuth = OAuthResourceServer{Enabled: true, Compose: true}
				return cfg
			}(),
			wantErr: errorstack.NewValidation(oauthResourceIdEntry, oauthAuthServersEntry),
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
