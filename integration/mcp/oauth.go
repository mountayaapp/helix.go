package mcp

import (
	"context"
	"net/http"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

/*
wellKnownProtectedResource is the path, relative to the server root, where OAuth
2.0 Protected Resource Metadata is served, as defined by RFC 9728.
*/
const wellKnownProtectedResource = "/.well-known/oauth-protected-resource"

/*
metadataURL returns the absolute URL of the Protected Resource Metadata endpoint,
derived from ResourceId. It is advertised to clients in the WWW-Authenticate
header so they can discover the authorization servers.
*/
func (a *OAuthResourceServer) metadataURL() string {
	return a.ResourceId + wellKnownProtectedResource
}

/*
BearerMiddleware returns the HTTP middleware enforcing bearer token
authentication on the MCP transport. The SDK middleware handles the 401 +
WWW-Authenticate response (pointing clients at the Protected Resource Metadata
endpoint); the claims returned by ValidateToken are carried through
TokenInfo.Extra so tool handlers can read them.

When Compose is false, the integration applies this middleware automatically. It
is exported so that, when Compose is true, the consumer can place it inside its
own Middleware chain.
*/
func (a *OAuthResourceServer) BearerMiddleware() func(next http.Handler) http.Handler {

	// Adapt the consumer's ValidateToken into the SDK's TokenVerifier shape. The
	// SDK middleware handles the 401 + WWW-Authenticate response; the claims are
	// carried through TokenInfo.Extra so tool handlers can read them.
	verifier := func(ctx context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
		claims, expiration, err := a.ValidateToken(ctx, token)
		if err != nil {
			return nil, err
		}

		return &mcpauth.TokenInfo{
			Scopes:     a.Scopes,
			Expiration: expiration,
			Extra:      map[string]any{"claims": claims},
		}, nil
	}

	return mcpauth.RequireBearerToken(verifier, &mcpauth.RequireBearerTokenOptions{
		ResourceMetadataURL: a.metadataURL(),
		Scopes:              a.Scopes,
	})
}

/*
metadataHandler returns the path and handler serving the OAuth 2.0 Protected
Resource Metadata document (RFC 9728).
*/
func (a *OAuthResourceServer) metadataHandler() (string, http.Handler) {
	handler := mcpauth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:             a.ResourceId,
		AuthorizationServers: a.AuthorizationServers,
		ScopesSupported:      a.Scopes,
	})

	return wellKnownProtectedResource, handler
}
