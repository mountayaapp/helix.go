package graphql

import (
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/stretchr/testify/assert"
)

var schemaEntry = errorstack.Entry{
	Message: "Must be set",
	Path:    []any{"config", "schema"},
}

var cacheEntry = errorstack.Entry{
	Message: "Must be set",
	Path:    []any{"config", "apq", "cache"},
}

var tlsPairEntry = errorstack.Entry{
	Message: "Must be set together; cert_pem and key_pem are required as a pair",
	Path:    []any{"config", "tls"},
}

// sanitized returns the Config sanitize turns an empty one into, with change
// applied on top.
func sanitized(change func(cfg *Config)) Config {
	cfg := Config{
		Address:             ":8080",
		Path:                "/graphql",
		QueryCacheSize:      defaultQueryCacheSize,
		ParserTokenLimit:    defaultParserTokenLimit,
		ComplexityLimit:     defaultComplexityLimit,
		DepthLimit:          defaultDepthLimit,
		MaxRequestBodyBytes: defaultMaxRequestBodyBytes,
		TraceResolvers:      ResolverTracingRoot,
		ReadHeaderTimeout:   10 * time.Second,
		ReadTimeout:         30 * time.Second,
		IdleTimeout:         120 * time.Second,
	}

	if change != nil {
		change(&cfg)
	}

	return cfg
}

func TestConfig_Sanitize(t *testing.T) {
	cache := lru.New[string](1)

	testcases := []struct {
		name   string
		before Config
		after  Config
		err    error
	}{
		{
			name:   "empty config applies defaults and returns schema error",
			before: Config{},
			after:  sanitized(nil),
			err:    errorstack.NewValidation(schemaEntry),
		},
		{
			name: "custom address and path are preserved",
			before: Config{
				Address: ":9090",
				Path:    "/api/graphql",
			},
			after: sanitized(func(cfg *Config) {
				cfg.Address = ":9090"
				cfg.Path = "/api/graphql"
			}),
			err: errorstack.NewValidation(schemaEntry),
		},
		{
			name: "explicit limits and timeouts are preserved",
			before: Config{
				QueryCacheSize:      10,
				ParserTokenLimit:    100,
				ComplexityLimit:     200,
				DepthLimit:          5,
				MaxRequestBodyBytes: 1024,
				TraceResolvers:      ResolverTracingAll,
				RequestTimeout:      5 * time.Second,
				ReadHeaderTimeout:   2 * time.Second,
				ReadTimeout:         3 * time.Second,
				IdleTimeout:         4 * time.Second,
			},
			after: sanitized(func(cfg *Config) {
				cfg.QueryCacheSize = 10
				cfg.ParserTokenLimit = 100
				cfg.ComplexityLimit = 200
				cfg.DepthLimit = 5
				cfg.MaxRequestBodyBytes = 1024
				cfg.TraceResolvers = ResolverTracingAll
				cfg.RequestTimeout = 5 * time.Second
				cfg.ReadHeaderTimeout = 2 * time.Second
				cfg.ReadTimeout = 3 * time.Second
				cfg.IdleTimeout = 4 * time.Second
			}),
			err: errorstack.NewValidation(schemaEntry),
		},
		{
			name: "negative limits and timeouts are refused",
			before: Config{
				ParserTokenLimit:    -1,
				ComplexityLimit:     -1,
				DepthLimit:          -1,
				MaxRequestBodyBytes: -1,
				RequestTimeout:      -1,
				ReadHeaderTimeout:   -1,
				ReadTimeout:         -1,
				IdleTimeout:         -1,
			},
			after: sanitized(func(cfg *Config) {
				cfg.ParserTokenLimit = -1
				cfg.ComplexityLimit = -1
				cfg.DepthLimit = -1
				cfg.MaxRequestBodyBytes = -1
				cfg.RequestTimeout = -1
				cfg.ReadHeaderTimeout = -1
				cfg.ReadTimeout = -1
				cfg.IdleTimeout = -1
			}),
			err: errorstack.NewValidation(
				schemaEntry,
				errorstack.Entry{Message: "Must not be negative", Path: []any{"config", "parser_token_limit"}},
				errorstack.Entry{Message: "Must not be negative", Path: []any{"config", "complexity_limit"}},
				errorstack.Entry{Message: "Must not be negative", Path: []any{"config", "depth_limit"}},
				errorstack.Entry{Message: "Must not be negative", Path: []any{"config", "max_request_body_bytes"}},
				errorstack.Entry{Message: "Must not be negative", Path: []any{"config", "request_timeout"}},
				errorstack.Entry{Message: "Must not be negative", Path: []any{"config", "read_header_timeout"}},
				errorstack.Entry{Message: "Must not be negative", Path: []any{"config", "read_timeout"}},
				errorstack.Entry{Message: "Must not be negative", Path: []any{"config", "idle_timeout"}},
			),
		},
		{
			name:   "an unknown resolver tracing mode is refused",
			before: Config{TraceResolvers: "some"},
			after: sanitized(func(cfg *Config) {
				cfg.TraceResolvers = "some"
			}),
			err: errorstack.NewValidation(schemaEntry, errorstack.Entry{
				Message: `Must be one of "root", "all" or "none"`,
				Path:    []any{"config", "trace_resolvers"},
			}),
		},
		{
			name: "APQ enabled without cache returns schema and cache errors",
			before: Config{
				APQ: ConfigAPQ{
					Enabled: true,
				},
			},
			after: sanitized(func(cfg *Config) {
				cfg.APQ = ConfigAPQ{
					Enabled:        true,
					MaxQueryLength: defaultAPQMaxQueryLength,
				}
			}),
			err: errorstack.NewValidation(schemaEntry, cacheEntry),
		},
		{
			name: "APQ enabled with cache and custom length preserves values",
			before: Config{
				APQ: ConfigAPQ{
					Enabled:        true,
					Cache:          cache,
					MaxQueryLength: 100,
				},
			},
			after: sanitized(func(cfg *Config) {
				cfg.APQ = ConfigAPQ{
					Enabled:        true,
					Cache:          cache,
					MaxQueryLength: 100,
				}
			}),
			err: errorstack.NewValidation(schemaEntry),
		},
		{
			name: "APQ disabled applies no APQ default",
			before: Config{
				APQ: ConfigAPQ{
					Enabled: false,
				},
			},
			after: sanitized(nil),
			err:   errorstack.NewValidation(schemaEntry),
		},
		{
			name: "GraphiQL enabled applies default path",
			before: Config{
				GraphiQL: ConfigGraphiQL{
					Enabled: true,
				},
			},
			after: sanitized(func(cfg *Config) {
				cfg.GraphiQL = ConfigGraphiQL{
					Enabled: true,
					Path:    "/graphiql",
				}
			}),
			err: errorstack.NewValidation(schemaEntry),
		},
		{
			name: "GraphiQL enabled with custom path preserves path",
			before: Config{
				GraphiQL: ConfigGraphiQL{
					Enabled: true,
					Path:    "/custom/graphiql",
				},
			},
			after: sanitized(func(cfg *Config) {
				cfg.GraphiQL = ConfigGraphiQL{
					Enabled: true,
					Path:    "/custom/graphiql",
				}
			}),
			err: errorstack.NewValidation(schemaEntry),
		},
		{
			name: "GraphiQL on the GraphQL path is refused",
			before: Config{
				GraphiQL: ConfigGraphiQL{
					Enabled: true,
					Path:    "/graphql",
				},
			},
			after: sanitized(func(cfg *Config) {
				cfg.GraphiQL = ConfigGraphiQL{
					Enabled: true,
					Path:    "/graphql",
				}
			}),
			err: errorstack.NewValidation(schemaEntry, errorstack.Entry{
				Message: "Must differ from path",
				Path:    []any{"config", "graphiql", "path"},
			}),
		},
		{
			name: "GraphiQL disabled does not apply nor check its path",
			before: Config{
				GraphiQL: ConfigGraphiQL{
					Enabled: false,
					Path:    "/health",
				},
			},
			after: sanitized(func(cfg *Config) {
				cfg.GraphiQL = ConfigGraphiQL{
					Enabled: false,
					Path:    "/health",
				}
			}),
			err: errorstack.NewValidation(schemaEntry),
		},
		{
			name: "Introspection enabled only returns schema error",
			before: Config{
				Introspection: ConfigIntrospection{
					Enabled: true,
				},
			},
			after: sanitized(func(cfg *Config) {
				cfg.Introspection = ConfigIntrospection{
					Enabled: true,
				}
			}),
			err: errorstack.NewValidation(schemaEntry),
		},
		{
			name: "TLS with only CertPEM returns schema and TLS errors",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			after: sanitized(func(cfg *Config) {
				cfg.TLS = integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				}
			}),
			err: errorstack.NewValidation(schemaEntry, tlsPairEntry),
		},
		{
			name: "TLS with only KeyPEM returns schema and TLS errors",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled: true,
					KeyPEM:  []byte("key"),
				},
			},
			after: sanitized(func(cfg *Config) {
				cfg.TLS = integration.ConfigTLS{
					Enabled: true,
					KeyPEM:  []byte("key"),
				}
			}),
			err: errorstack.NewValidation(schemaEntry, tlsPairEntry),
		},
		{
			name: "TLS with both CertPEM and KeyPEM returns only schema error",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
				},
			},
			after: sanitized(func(cfg *Config) {
				cfg.TLS = integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
				}
			}),
			err: errorstack.NewValidation(schemaEntry),
		},
		{
			name: "disabled TLS ignores invalid certs",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled: false,
					CertPEM: []byte("cert"),
				},
			},
			after: sanitized(func(cfg *Config) {
				cfg.TLS = integration.ConfigTLS{
					Enabled: false,
					CertPEM: []byte("cert"),
				}
			}),
			err: errorstack.NewValidation(schemaEntry),
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

// TestConfig_Sanitize_Paths covers the rules both endpoint paths follow. A path
// the serve mux cannot parse used to make New panic, and a probe path would have
// served the endpoint outside Middleware, since the probes bypass it.
func TestConfig_Sanitize_Paths(t *testing.T) {
	const (
		slash = `Must start with "/"`
		root  = `Must not be "/"`
		clean = "Must be a clean path, without wildcards, query, fragment or whitespace"
		probe = "Must not be a health probe path"
	)

	testcases := []struct {
		path string
		want string
	}{
		{path: "/graphql", want: ""},
		{path: "/api/v1/graphql", want: ""},
		{path: "graphql", want: slash},
		{path: "/", want: root},
		{path: "/graphql/", want: clean},
		{path: "//graphql", want: clean},
		{path: "/api/../graphql", want: clean},
		{path: "/{name}", want: clean},
		{path: "/graphql?x=1", want: clean},
		{path: "/graph ql", want: clean},
		{path: integration.PathLiveness, want: probe},
		{path: integration.PathReadiness, want: probe},
	}

	for _, tc := range testcases {
		t.Run(tc.path, func(t *testing.T) {
			assert.Equal(t, tc.want, checkPath(tc.path))

			cfg := Config{Schema: testSchema(nil), Path: tc.path}
			err := cfg.sanitize()
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}

			assert.Equal(t, errorstack.NewValidation(errorstack.Entry{
				Message: tc.want,
				Path:    []any{"config", "path"},
			}), err)
		})
	}

	t.Run("GraphiQL follows the same rules", func(t *testing.T) {
		cfg := Config{Schema: testSchema(nil), GraphiQL: ConfigGraphiQL{Enabled: true, Path: integration.PathReadiness}}

		assert.Equal(t, errorstack.NewValidation(errorstack.Entry{
			Message: probe,
			Path:    []any{"config", "graphiql", "path"},
		}), cfg.sanitize())
	})
}

// TestConfig_Sanitize_QueryCacheSize covers the field's own rules, which the table
// above cannot: every case there leaves it unset, so only the default is exercised.
//
// The non-positive cases are not cosmetic. gqlgen's lru.New panics on a size below
// one, and sanitize is the only thing standing between a zero-valued Config and
// that panic at startup — so "0 becomes the default" is a crash guard, not a
// convenience.
func TestConfig_Sanitize_QueryCacheSize(t *testing.T) {
	testcases := []struct {
		name string
		size int
		want int
	}{
		{name: "unset falls back to the default", size: 0, want: defaultQueryCacheSize},
		{name: "negative falls back to the default", size: -1, want: defaultQueryCacheSize},
		{name: "an explicit size is preserved", size: 32, want: 32},
		{name: "one is a valid floor", size: 1, want: 1},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{QueryCacheSize: tc.size}
			_ = cfg.sanitize()

			assert.Equal(t, tc.want, cfg.QueryCacheSize)
			assert.Positive(t, cfg.QueryCacheSize, "lru.New panics below one, so sanitize must never leave a non-positive size")
		})
	}
}
