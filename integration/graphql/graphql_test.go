package graphql

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"
	"github.com/mountaya/helix.go/service"
	"github.com/mountaya/helix.go/service/servicetest"

	gqlgen "github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// testSDL is the schema the tests serve. Thing refers to itself so that a test
// can nest a document as deep as it needs to.
const testSDL = `
	type Query {
		name: String
		other: String
		thing: Thing
	}

	type Thing {
		name: String
		child: Thing
	}

	type Mutation {
		rename(name: String!): String
	}
`

// resolverFunc resolves one root field of the test schema.
type resolverFunc func(ctx context.Context) (any, error)

// testSchema returns an executable schema for testSDL whose root fields are
// answered by resolvers, keyed by field name. A field with no resolver resolves
// to null.
//
// It is hand-built rather than generated: gqlgen's code generator is not
// something a test in this repository should have to run. It resolves root
// fields the way generated code does — concurrently, under a field context whose
// parent marks the root type, through the field middleware, with panics
// recovered through gqlgen.Recover — because that is what the extensions under
// test hook into.
func testSchema(resolvers map[string]resolverFunc) gqlgen.ExecutableSchema {
	schema := gqlparser.MustLoadSchema(&ast.Source{Name: "test", Input: testSDL})

	return &gqlgen.ExecutableSchemaMock{
		SchemaFunc: func() *ast.Schema { return schema },
		ComplexityFunc: func(context.Context, string, string, int, map[string]any) (int, bool) {
			return 0, false
		},
		ExecFunc: func(ctx context.Context) gqlgen.ResponseHandler {
			opCtx := gqlgen.GetOperationContext(ctx)

			sent := false
			return func(ctx context.Context) *gqlgen.Response {
				if sent {
					return nil
				}
				sent = true

				return &gqlgen.Response{Data: resolveRoot(ctx, opCtx, resolvers)}
			}
		},
	}
}

// resolveRoot resolves the root fields of the operation concurrently, and
// returns the data they produced.
func resolveRoot(ctx context.Context, opCtx *gqlgen.OperationContext, resolvers map[string]resolverFunc) jsontext.Value {
	object := "Query"
	if opCtx.Operation.Operation == ast.Mutation {
		object = "Mutation"
	}

	ctx = gqlgen.WithFieldContext(ctx, &gqlgen.FieldContext{Object: object})
	fields := gqlgen.CollectFields(opCtx, opCtx.Operation.SelectionSet, []string{object})

	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		data = make(map[string]any, len(fields))
	)

	for _, field := range fields {
		resolve := resolvers[field.Name]
		if resolve == nil {
			data[field.Alias] = nil
			continue
		}

		wg.Go(func() {
			ctx := gqlgen.WithFieldContext(ctx, &gqlgen.FieldContext{
				Object:     object,
				Field:      field,
				IsMethod:   true,
				IsResolver: true,
			})

			defer func() {
				if r := recover(); r != nil {
					gqlgen.AddError(ctx, gqlgen.Recover(ctx, r))
				}
			}()

			res, err := opCtx.ResolverMiddleware(ctx, gqlgen.Resolver(resolve))
			if err != nil {
				gqlgen.AddError(ctx, err)
				res = nil
			}

			mu.Lock()
			data[field.Alias] = res
			mu.Unlock()
		})
	}

	wg.Wait()

	b, _ := json.Marshal(data)
	return b
}

// constant resolves to v.
func constant(v any) resolverFunc {
	return func(context.Context) (any, error) { return v, nil }
}

// testConfig returns a Config serving testSchema with resolvers.
func testConfig(resolvers map[string]resolverFunc) Config {
	return Config{Schema: testSchema(resolvers)}
}

// newTestHandler builds the integration for cfg, as New does, and returns the
// handler its HTTP server would serve, along with the recorder of the Service it
// is built on.
func newTestHandler(t *testing.T, cfg Config) (http.Handler, *servicetest.Recorder) {
	t.Helper()

	svc, rec := servicetest.New(t)
	return newTestHandlerFor(t, svc, cfg), rec
}

// newTestHandlerFor is newTestHandler on a Service of the test's own.
func newTestHandlerFor(t *testing.T, svc *service.Service, cfg Config) http.Handler {
	t.Helper()

	require.NoError(t, cfg.sanitize())

	g, err := build(svc, &cfg)
	require.NoError(t, err)

	return g.handler()
}

// serve sends req to h and returns what h answered.
func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	return rw
}

// postGraphQL POSTs body to the GraphQL endpoint of h as JSON.
func postGraphQL(h http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	return serve(h, req)
}

// query encodes a GraphQL request body for document.
func query(document string) string {
	b, _ := json.Marshal(map[string]string{"query": document})
	return string(b)
}

// response is the decoded body of a GraphQL response.
type response struct {
	Data       map[string]any `json:"data"`
	Errors     []responseErr  `json:"errors"`
	Extensions map[string]any `json:"extensions"`
}

// responseErr is one element of a GraphQL response's errors array.
type responseErr struct {
	Message    string         `json:"message"`
	Path       []any          `json:"path"`
	Extensions map[string]any `json:"extensions"`
}

// decode decodes the GraphQL response rw recorded.
func decode(t *testing.T, rw *httptest.ResponseRecorder) response {
	t.Helper()

	var resp response
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &resp), "body: %s", rw.Body.String())

	return resp
}

func TestGraphQL_Post_ReturnsData(t *testing.T) {
	h, _ := newTestHandler(t, testConfig(map[string]resolverFunc{"name": constant("helix")}))

	rw := postGraphQL(h, query("{ name }"))

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.JSONEq(t, `{"data":{"name":"helix"}}`, rw.Body.String())
}

// TestGraphQL_Options_ReturnsAllowedMethods pins the route the mux has always
// declared: only the POST transport used to be installed, so gqlgen answered an
// OPTIONS request with a 400 "transport not supported".
func TestGraphQL_Options_ReturnsAllowedMethods(t *testing.T) {
	h, _ := newTestHandler(t, testConfig(nil))

	rw := serve(h, httptest.NewRequest(http.MethodOptions, "/graphql", nil))

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "OPTIONS, POST", rw.Header().Get("Allow"))
}

func TestGraphQL_NonAllowedMethod_ReturnsMethodNotAllowed(t *testing.T) {
	h, _ := newTestHandler(t, testConfig(nil))

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method+" /graphql", func(t *testing.T) {
			rw := serve(h, httptest.NewRequest(method, "/graphql", nil))

			assert.Equal(t, http.StatusMethodNotAllowed, rw.Code)
			assert.Equal(t, "OPTIONS, POST", rw.Header().Get("Allow"))
			assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
			if method != http.MethodHead {
				assert.JSONEq(t, `{"errors":[{"message":"Method is not allowed for this resource","extensions":{"code":"METHOD_NOT_ALLOWED"}}]}`, rw.Body.String())
			}
		})
	}
}

func TestGraphQL_UnknownRoute_ReturnsNotFound(t *testing.T) {
	h, _ := newTestHandler(t, testConfig(nil))

	for _, route := range []string{"/", "/unknown", "/api/graphql", "/graphql/"} {
		t.Run("GET "+route, func(t *testing.T) {
			rw := serve(h, httptest.NewRequest(http.MethodGet, route, nil))

			assert.Equal(t, http.StatusNotFound, rw.Code)
			assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
			assert.JSONEq(t, `{"errors":[{"message":"Resource does not exist","extensions":{"code":"NOT_FOUND"}}]}`, rw.Body.String())
		})
	}
}

func TestGraphQL_GraphiQL(t *testing.T) {
	cfg := testConfig(nil)
	cfg.GraphiQL.Enabled = true
	h, _ := newTestHandler(t, cfg)

	rw := serve(h, httptest.NewRequest(http.MethodGet, "/graphiql", nil))

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Contains(t, rw.Body.String(), "GraphiQL")
}

func TestGraphQL_Liveness_ReturnsOK(t *testing.T) {
	h, _ := newTestHandler(t, testConfig(nil))

	rw := serve(h, httptest.NewRequest(http.MethodGet, "/health", nil))

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"data":null}`, rw.Body.String())
}

func TestGraphQL_Readiness(t *testing.T) {
	t.Run("custom ready", func(t *testing.T) {
		cfg := testConfig(nil)
		cfg.Readiness = func(*http.Request) int { return http.StatusOK }
		h, _ := newTestHandler(t, cfg)

		rw := serve(h, httptest.NewRequest(http.MethodGet, "/ready", nil))

		assert.Equal(t, http.StatusOK, rw.Code)
		assert.JSONEq(t, `{"data":null}`, rw.Body.String())
	})

	t.Run("custom unready", func(t *testing.T) {
		cfg := testConfig(nil)
		cfg.Readiness = func(*http.Request) int { return http.StatusServiceUnavailable }
		h, _ := newTestHandler(t, cfg)

		rw := serve(h, httptest.NewRequest(http.MethodGet, "/ready", nil))

		assert.Equal(t, http.StatusServiceUnavailable, rw.Code)
		assert.JSONEq(t, `{"errors":[{"message":"Service is temporarily unavailable","extensions":{"code":"SERVICE_UNAVAILABLE"}}]}`, rw.Body.String())
	})

	// The probe is served on the same listener as the API, to whoever asks. What
	// made a dependency unhealthy — a driver error naming a host — must not be in
	// the body.
	t.Run("an unhealthy dependency is reported generically", func(t *testing.T) {
		svc, _ := servicetest.New(t)
		require.NoError(t, service.Attach(svc, "database", unhealthy{
			err: errors.New("dial tcp 10.0.0.7:5432: connection refused"),
		}))

		h := newTestHandlerFor(t, svc, testConfig(nil))

		rw := serve(h, httptest.NewRequest(http.MethodGet, "/ready", nil))

		assert.Equal(t, http.StatusServiceUnavailable, rw.Code)
		assert.JSONEq(t, `{"errors":[{"message":"Service is temporarily unavailable","extensions":{"code":"SERVICE_UNAVAILABLE"}}]}`, rw.Body.String())
		assert.NotContains(t, rw.Body.String(), "10.0.0.7")
		assert.NotContains(t, rw.Body.String(), "database")
	})
}

// unhealthy is a dependency whose health check fails with err.
type unhealthy struct {
	err error
}

func (u unhealthy) CheckHealth(context.Context) error { return u.err }
func (u unhealthy) Close(context.Context) error       { return nil }

var _ integration.Dependency = unhealthy{}

// TestGraphQL_BodyLimit pins the P0: gqlgen's POST transport reads the whole
// body into memory, whatever its size.
func TestGraphQL_BodyLimit(t *testing.T) {
	cfg := testConfig(map[string]resolverFunc{"name": constant("helix")})
	cfg.MaxRequestBodyBytes = 64
	h, _ := newTestHandler(t, cfg)

	body := query("{ name " + strings.Repeat("other ", 20) + "}")
	require.Greater(t, len(body), 64)

	t.Run("a declared length over the limit", func(t *testing.T) {
		rw := postGraphQL(h, body)

		assert.Equal(t, http.StatusRequestEntityTooLarge, rw.Code)
		assert.JSONEq(t, `{"errors":[{"message":"Payload exceeds size limit","extensions":{"code":"PAYLOAD_TOO_LARGE"}}]}`, rw.Body.String())
	})

	// A chunked request declares no length, and is caught while it is read.
	t.Run("an undeclared length over the limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/graphql", io.MultiReader(strings.NewReader(body)))
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = -1

		rw := serve(h, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, rw.Code)
		assert.Contains(t, rw.Body.String(), `"PAYLOAD_TOO_LARGE"`)
	})

	t.Run("a body within the limit", func(t *testing.T) {
		rw := postGraphQL(h, query("{ name }"))

		assert.Equal(t, http.StatusOK, rw.Code)
	})
}

// TestGraphQL_UndecodableBody_IsNotEchoed pins the P0: gqlgen answers a body it
// cannot decode with an error message embedding the whole body.
//
// "member in another case" pins that the body is decoded the way the transport
// decodes it: the v1 encoding/json reads "Query" into the query and refuses the
// number, where json/v2 would skip the member and hand the body on to be echoed.
func TestGraphQL_UndecodableBody_IsNotEchoed(t *testing.T) {
	h, _ := newTestHandler(t, testConfig(nil))

	for name, body := range map[string]string{
		"invalid JSON":           `{"query": "{ name }", "secret": "s3cr3t-value"`,
		"wrong field type":       `{"query": 42, "secret": "s3cr3t-value"}`,
		"member in another case": `{"Query": 42, "secret": "s3cr3t-value"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rw := postGraphQL(h, body)

			assert.Equal(t, http.StatusBadRequest, rw.Code)
			assert.JSONEq(t, `{"errors":[{"message":"Request is invalid","extensions":{"code":"BAD_REQUEST"}}]}`, rw.Body.String())
			assert.NotContains(t, rw.Body.String(), "s3cr3t-value")
		})
	}
}

// TestGraphQL_UnsupportedContentType_CarriesCode pins the gqlgen error that came
// without an extensions.code.
func TestGraphQL_UnsupportedContentType_CarriesCode(t *testing.T) {
	h, _ := newTestHandler(t, testConfig(nil))

	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(query("{ name }")))
	req.Header.Set("Content-Type", "text/plain")

	rw := serve(h, req)

	assert.Equal(t, http.StatusBadRequest, rw.Code)
	assert.JSONEq(t, `{"errors":[{"message":"Request is invalid","extensions":{"code":"BAD_REQUEST"}}]}`, rw.Body.String())
}

func TestGraphQL_DepthLimit(t *testing.T) {
	var called bool
	cfg := testConfig(map[string]resolverFunc{"thing": func(context.Context) (any, error) {
		called = true
		return nil, nil
	}})
	cfg.DepthLimit = 3
	h, _ := newTestHandler(t, cfg)

	t.Run("an operation at the limit runs", func(t *testing.T) {
		rw := postGraphQL(h, query("{ thing { child { name } } }"))

		assert.Empty(t, decode(t, rw).Errors)
		assert.True(t, called)
	})

	t.Run("an operation past the limit is refused before it runs", func(t *testing.T) {
		called = false
		rw := postGraphQL(h, query("{ thing { child { child { name } } } }"))

		resp := decode(t, rw)
		require.Len(t, resp.Errors, 1)
		assert.Equal(t, "Operation exceeds the maximum depth of 3", resp.Errors[0].Message)
		assert.Equal(t, codeDepthLimitExceeded, resp.Errors[0].Extensions["code"])
		assert.False(t, called, "a refused operation must not execute")
	})

	t.Run("fragments count towards the depth", func(t *testing.T) {
		called = false
		rw := postGraphQL(h, query("{ thing { ...F } } fragment F on Thing { child { child { name } } }"))

		resp := decode(t, rw)
		require.Len(t, resp.Errors, 1)
		assert.Equal(t, codeDepthLimitExceeded, resp.Errors[0].Extensions["code"])
		assert.False(t, called)
	})
}

func TestGraphQL_ComplexityLimit(t *testing.T) {
	var called bool
	cfg := testConfig(map[string]resolverFunc{"name": func(context.Context) (any, error) {
		called = true
		return "helix", nil
	}})
	cfg.ComplexityLimit = 3
	h, _ := newTestHandler(t, cfg)

	rw := postGraphQL(h, query("{ a: name b: name c: name d: name }"))

	resp := decode(t, rw)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "COMPLEXITY_LIMIT_EXCEEDED", resp.Errors[0].Extensions["code"])
	assert.False(t, called, "a refused operation must not execute")
}

func TestGraphQL_ParserTokenLimit(t *testing.T) {
	cfg := testConfig(nil)
	cfg.ParserTokenLimit = 10
	h, _ := newTestHandler(t, cfg)

	rw := postGraphQL(h, query("{ "+strings.Repeat("name ", 20)+"}"))

	resp := decode(t, rw)
	require.Len(t, resp.Errors, 1)
	assert.Contains(t, resp.Errors[0].Message, "token limit")
	assert.Equal(t, "GRAPHQL_PARSE_FAILED", resp.Errors[0].Extensions["code"])
}

// TestGraphQL_Suggestions pins that a validation error does not reveal what an
// introspection query would have, when introspection is off.
func TestGraphQL_Suggestions(t *testing.T) {
	for _, introspection := range []bool{false, true} {
		t.Run(fmt.Sprintf("introspection %t", introspection), func(t *testing.T) {
			cfg := testConfig(nil)
			cfg.Introspection.Enabled = introspection
			h, _ := newTestHandler(t, cfg)

			resp := decode(t, postGraphQL(h, query("{ nme }")))

			require.Len(t, resp.Errors, 1)
			assert.Equal(t, introspection, strings.Contains(resp.Errors[0].Message, "Did you mean"), resp.Errors[0].Message)
		})
	}
}

// TestGraphQL_ResolverError_IsMasked pins the P1 end to end: an error a resolver
// returns reaches the client only when it was written for one.
func TestGraphQL_ResolverError_IsMasked(t *testing.T) {
	h, _ := newTestHandler(t, testConfig(map[string]resolverFunc{
		"name": func(context.Context) (any, error) {
			return nil, fmt.Errorf("query things: %w", errors.New("dial tcp 10.0.0.7:5432: connection refused"))
		},
		"other": func(context.Context) (any, error) {
			return nil, errorstack.New("Thing does not exist", errorstack.WithCode(errorstack.CodeNotFound))
		},
	}))

	rw := postGraphQL(h, query("{ name other }"))

	assert.NotContains(t, rw.Body.String(), "10.0.0.7")

	resp := decode(t, rw)
	require.Len(t, resp.Errors, 2)

	byPath := map[string]responseErr{}
	for _, e := range resp.Errors {
		byPath[fmt.Sprint(e.Path...)] = e
	}

	assert.Equal(t, "Internal server error", byPath["name"].Message)
	assert.Equal(t, errorstack.CodeInternalError, byPath["name"].Extensions["code"])
	assert.Equal(t, "Thing does not exist", byPath["other"].Message)
	assert.Equal(t, errorstack.CodeNotFound, byPath["other"].Extensions["code"])
}

func TestGraphQL_ResolverPanic(t *testing.T) {
	h, rec := newTestHandler(t, testConfig(map[string]resolverFunc{
		"name": func(context.Context) (any, error) { panic("resolver exploded") },
	}))

	rw := postGraphQL(h, query("{ name }"))

	assert.NotContains(t, rw.Body.String(), "exploded")

	resp := decode(t, rw)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "Internal server error", resp.Errors[0].Message)
	assert.Equal(t, errorstack.CodeInternalError, resp.Errors[0].Extensions["code"])

	// The panic is logged through the Service's logger, rather than written to
	// stderr the way gqlgen's default recover function does.
	var logged bool
	for _, record := range rec.Records() {
		if record.Body().AsString() == "Recovered from a panic while serving a GraphQL operation" {
			logged = true
		}
	}
	assert.True(t, logged, "the panic must be logged")
}

// TestGraphQL_ConcurrentTopLevelExtensions pins the P1 race: the presenter read
// gqlgen's response extensions map, unlocked, while concurrent resolvers' errors
// wrote it under gqlgen's lock. Run under -race, the old code fails here — or
// dies with a concurrent map access.
func TestGraphQL_ConcurrentTopLevelExtensions(t *testing.T) {
	const fields = 32

	h, _ := newTestHandler(t, testConfig(map[string]resolverFunc{
		"name": func(ctx context.Context) (any, error) {
			alias := gqlgen.GetFieldContext(ctx).Field.Alias

			return nil, errorstack.New("Thing does not exist", errorstack.WithCode(errorstack.CodeNotFound)).
				SetExtension("shared", "any").
				SetExtension("key_"+alias, alias)
		},
	}))

	var document strings.Builder
	document.WriteString("{")
	for i := range fields {
		fmt.Fprintf(&document, " f%d: name", i)
	}
	document.WriteString(" }")

	resp := decode(t, postGraphQL(h, query(document.String())))

	assert.Len(t, resp.Errors, fields)
	assert.Equal(t, "any", resp.Extensions["shared"], "a key several errors register keeps one value")
	for i := range fields {
		alias := fmt.Sprintf("f%d", i)
		assert.Equal(t, alias, resp.Extensions["key_"+alias])
	}
}

func TestGraphQL_RequestTimeout(t *testing.T) {
	var deadline bool
	cfg := testConfig(map[string]resolverFunc{"name": func(ctx context.Context) (any, error) {
		_, deadline = ctx.Deadline()
		return "helix", nil
	}})

	t.Run("unset leaves requests unbounded", func(t *testing.T) {
		h, _ := newTestHandler(t, cfg)
		postGraphQL(h, query("{ name }"))

		assert.False(t, deadline)
	})

	t.Run("set bounds the resolvers' context", func(t *testing.T) {
		cfg := cfg
		cfg.RequestTimeout = time.Hour
		h, _ := newTestHandler(t, cfg)
		postGraphQL(h, query("{ name }"))

		assert.True(t, deadline)
	})
}

func TestGraphQL_Middleware(t *testing.T) {
	cfg := testConfig(map[string]resolverFunc{"name": constant("helix")})
	cfg.Middleware = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			integration.WriteError(rw, req, http.StatusUnauthorized)
		})
	}
	h, _ := newTestHandler(t, cfg)

	assert.Equal(t, http.StatusUnauthorized, postGraphQL(h, query("{ name }")).Code, "the GraphQL endpoint is behind the middleware")
	assert.Equal(t, http.StatusOK, serve(h, httptest.NewRequest(http.MethodGet, "/health", nil)).Code, "the probes are not")
}

func TestGraphQL_Configure_AppliedLast(t *testing.T) {
	cfg := testConfig(map[string]resolverFunc{"name": constant("helix")})
	cfg.Configure = func(srv *handler.Server) {
		srv.AroundResponses(func(ctx context.Context, next gqlgen.ResponseHandler) *gqlgen.Response {
			resp := next(ctx)
			resp.Extensions = map[string]any{"configured": true}
			return resp
		})
	}
	h, _ := newTestHandler(t, cfg)

	resp := decode(t, postGraphQL(h, query("{ name }")))

	assert.Equal(t, true, resp.Extensions["configured"])
}

func TestNew_Refusals(t *testing.T) {
	t.Run("nil service", func(t *testing.T) {
		err := New(t.Context(), nil, testConfig(nil))

		assert.Equal(t, errorstack.NewValidation(errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"service"},
		}), err)
	})

	// A path the serve mux cannot parse used to panic in New, and a probe path
	// would have put the GraphQL endpoint outside Middleware.
	for _, path := range []string{"graphql", "/", "/health", "/ready", "/graphql/", "/a b", "/{x}"} {
		t.Run("path "+path, func(t *testing.T) {
			svc, _ := servicetest.New(t)
			cfg := testConfig(nil)
			cfg.Path = path

			var err error
			require.NotPanics(t, func() { err = New(t.Context(), svc, cfg) })

			var stack *errorstack.Error
			require.ErrorAs(t, err, &stack)
			assert.Equal(t, []any{"config", "path"}, stack.Entries[0].Path)
		})
	}

	t.Run("TLS without a certificate", func(t *testing.T) {
		svc, _ := servicetest.New(t)
		cfg := testConfig(nil)
		cfg.TLS.Enabled = true

		err := New(t.Context(), svc, cfg)

		assert.Equal(t, errorstack.NewValidation(errorstack.Entry{
			Message: "Must be set; a server requires cert_pem and key_pem",
			Path:    []any{"config", "tls"},
		}), err)
	})
}

// TestNew_GraphiQLWithoutIntrospection_Warns pins the documented behaviour:
// enabling GraphiQL does not enable introspection, without which GraphiQL cannot
// load the schema.
func TestNew_GraphiQLWithoutIntrospection_Warns(t *testing.T) {
	warned := func(t *testing.T, introspection bool) bool {
		svc, rec := servicetest.New(t)
		cfg := testConfig(nil)
		cfg.GraphiQL.Enabled = true
		cfg.Introspection.Enabled = introspection
		require.NoError(t, New(t.Context(), svc, cfg))

		for _, record := range rec.Records() {
			if strings.HasPrefix(record.Body().AsString(), "GraphiQL is enabled but introspection is not") {
				return true
			}
		}

		return false
	}

	t.Run("without introspection", func(t *testing.T) {
		assert.True(t, warned(t, false))
	})

	t.Run("with introspection", func(t *testing.T) {
		assert.False(t, warned(t, true))
	})
}
