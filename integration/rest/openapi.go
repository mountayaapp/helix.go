package rest

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/mountaya/helix.go/documents"
	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// NewOpenAPIRouter loads the OpenAPI descriptions passed, merges them into one
// document, validates it, and returns the router the request/response validation
// middleware matches against. ctx bounds the loading of a description, or of a
// document one references, over the network.
//
// It is exported because "which routes does this description actually validate"
// is a question worth answering outside a running server. The middleware fails
// OPEN on a route FindRoute does not resolve — a request it cannot place is passed
// through rather than refused, and its span says so with rest.openapi.validated
// set to false — so a route missing from every description is unvalidated, and
// the only way to see that before it ships is to build this router and ask it. A
// test that reproduced the loading instead of calling it would answer for its own
// copy rather than for the server's.
//
// The host of every server URL is dropped and its path kept, so that the router
// matches a request whatever host it was sent to, under the base path the
// description declares. See ConfigOpenAPI.Descriptions.
//
// In an OpenAPI 3.1 description, every empty schema ({}) a JSON body reaches is
// spelled as the list of every JSON type, which is what it means there, so that a
// null where the description admits anything is admitted. See
// admitNullWhereTheSchemaIsEmpty. The document a route's Spec answers is the one
// spelled so.
//
// A failure is returned as an *errorstack.Error whose entries say what is wrong,
// with the index of the description at fault as their path when there is one.
func NewOpenAPIRouter(ctx context.Context, descriptions ...string) (routers.Router, error) {
	doc, err := LoadOpenAPIDescriptions(ctx, descriptions...)
	if err != nil {
		return nil, err
	}

	// Validated before the servers are rewritten, so that what is checked is what
	// the descriptions say rather than what this function made of them. The router
	// assumes a valid document, and an invalid one fails in ways that look like a
	// request being wrong rather than the description.
	//
	// Examples are left out of it. They are documentation: nothing routes or
	// validates a request against them, and the validator reads them with OpenAPI
	// 3.0's rules, so a 3.1 description whose example holds a null where its schema
	// is unconstrained ({}), which 3.1 allows, would refuse to load at all.
	if err := doc.Validate(ctx, openapi3.DisableExamplesValidation()); err != nil {
		return nil, invalidDescription(err)
	}

	doc.Servers, err = hostlessServers(doc.Servers)
	if err != nil {
		return nil, invalidDescription(err)
	}

	for _, item := range doc.Paths.Map() {
		if len(item.Servers) == 0 {
			continue
		}

		item.Servers, err = hostlessServers(item.Servers)
		if err != nil {
			return nil, invalidDescription(err)
		}
	}

	admitNullWhereTheSchemaIsEmpty(doc)

	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		return nil, invalidDescription(err)
	}

	return router, nil
}

// everyJSONType is every type JSON Schema gives a JSON value. A schema listing
// them all constrains a value's type no more than one listing none.
var everyJSONType = openapi3.Types{
	openapi3.TypeArray,
	openapi3.TypeBoolean,
	openapi3.TypeInteger,
	openapi3.TypeNull,
	openapi3.TypeNumber,
	openapi3.TypeObject,
	openapi3.TypeString,
}

// admitNullWhereTheSchemaIsEmpty spells every empty schema a JSON body of doc
// reaches as the list of every JSON type, null included, when doc is an OpenAPI
// 3.1 description or later.
//
// Under JSON Schema 2020-12, the dialect of OpenAPI 3.1, the empty schema ({})
// admits every value, null included: it is how a description says "anything
// here", such as the payload of an envelope that each operation narrows beside it,
// to null for an answer with none. The validator does not always read it so. A
// schema using no keyword new in 2020-12 is validated by the validator written for
// OpenAPI 3.0, which under 3.1 admits null only where `type` lists it, and an empty
// schema lists none. The null would be refused wherever the description admits
// anything: a request carrying one answered with a 400, and every answer with no
// payload recorded as failing validation.
//
// Listing every type is the same schema spelled so that both validators read it
// alike: it constrains nothing, so no value the description admits is refused and
// none it refuses is admitted. Only a JSON body can carry a null, and decoding one
// never reads its schema, so the schemas a JSON body reaches are the ones spelled
// anew. A parameter's, a header's or another media type's schema decides how its
// text is decoded, which a list of types would change, and is left as written
// unless it is the very schema a JSON body reaches too.
//
// An OpenAPI 3.0 description is left as written: there, null is a value only where
// `nullable` admits it, and the empty schema does not.
func admitNullWhereTheSchemaIsEmpty(doc *openapi3.T) {
	if !doc.IsOpenAPI31OrLater() || doc.Paths == nil {
		return
	}

	// Every empty schema is found before any is spelled anew: a schema is empty
	// when all of its subschemas are, so spelling one as it is found would change
	// what the walk finds next.
	empty := map[*openapi3.Schema]bool{}
	collect := func(_ string, ref *openapi3.SchemaRef) error {
		if ref.Value.IsEmpty() {
			empty[ref.Value] = true
		}

		return nil
	}

	for _, item := range doc.Paths.Map() {
		for _, operation := range item.Operations() {
			if body := operation.RequestBody; body != nil && body.Value != nil {
				walkJSONContent(body.Value.Content, collect)
			}

			if operation.Responses == nil {
				continue
			}

			for _, response := range operation.Responses.Map() {
				if response != nil && response.Value != nil {
					walkJSONContent(response.Value.Content, collect)
				}
			}
		}
	}

	for schema := range empty {
		types := slices.Clone(everyJSONType)
		schema.Type = &types
	}
}

// walkJSONContent walks the schema of every JSON media type of content, and every
// schema under it, with fn.
func walkJSONContent(content openapi3.Content, fn openapi3.WalkSchemasFunc) {
	for name, media := range content {
		if media == nil || media.Schema == nil || !isJSONMediaType(name) {
			continue
		}

		// fn never fails, and neither does the walk without it.
		_ = media.Schema.WalkSubtree(fn)
	}
}

// isJSONMediaType reports whether mediaType names JSON: application/json, or a
// type with the +json structured syntax suffix, parameters aside. A range (*/* or
// application/*) names no one type, and is not JSON.
func isJSONMediaType(mediaType string) bool {
	parsed, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return false
	}

	return parsed == "application/json" || strings.HasSuffix(parsed, "+json")
}

// hostlessServers returns servers reduced to their paths, without duplicates, with
// every variable at its default. No servers at all is the single server "/", which
// is what the OpenAPI specification says an absent servers list means.
//
// The host goes because it is where the description says the API is deployed,
// which is not where every instance of it runs: a request to localhost:8080 would
// never match a description naming its production host, and would pass through
// unvalidated. The path stays because it is part of every operation's URL: a
// description whose server is "https://api.example.com/v1" describes "/v1/things"
// under the path "/things", and dropping "/v1" would leave every route unmatched —
// unvalidated again, and silently.
func hostlessServers(servers openapi3.Servers) (openapi3.Servers, error) {
	if len(servers) == 0 {
		return openapi3.Servers{{URL: "/"}}, nil
	}

	out := make(openapi3.Servers, 0, len(servers))
	seen := make(map[string]bool, len(servers))
	for _, server := range servers {
		base, err := server.BasePath()
		if err != nil {
			return nil, fmt.Errorf("server %q has no resolvable path: %w", server.URL, err)
		}

		if seen[base] {
			continue
		}

		seen[base] = true
		out = append(out, &openapi3.Server{URL: base})
	}

	return out, nil
}

// LoadOpenAPIDescriptions loads each description from file or from a URL, depending
// on the path passed, and folds it into the first one. ctx bounds the loading of a
// description, or of a document one references, over the network.
//
// The first is the base because a merged document still has exactly one info, one
// set of servers and one document-level security, and picking the first is the only
// ordering a caller can predict without reading this function.
//
// A failure is returned as an *errorstack.Error whose entry carries the index of
// the description at fault as its path.
func LoadOpenAPIDescriptions(ctx context.Context, descriptions ...string) (*openapi3.T, error) {
	if len(descriptions) == 0 {
		return nil, errorstack.New("Must be set", errorstack.WithCode(errorstack.CodeValidationFailed))
	}

	loader := openapi3.NewLoader()
	loader.Context = ctx
	loader.IsExternalRefsAllowed = true
	loader.ReadFromURIFunc = openapi3.URIMapCache(openapi3.ReadFromURIs(readFromHelixDocuments, readFromHTTP, openapi3.ReadFromFile))

	var doc *openapi3.T
	for i, description := range descriptions {
		var loaded *openapi3.T
		var err error

		u, ok := isValidURL(description)
		if ok {
			loaded, err = loader.LoadFromURI(u)
		} else {
			loaded, err = loader.LoadFromFile(description)
		}

		if err != nil {
			return nil, invalidDescription(err, i)
		}

		if doc == nil {
			doc = loaded
			continue
		}

		if err := mergeOpenAPI(doc, loaded); err != nil {
			return nil, invalidDescription(err, i)
		}
	}

	return doc, nil
}

// invalidDescription is the user-facing error for a description that could not be
// loaded, merged or validated, located by path when the failure belongs to one
// description rather than to the merged document.
func invalidDescription(err error, path ...any) error {
	return errorstack.New(integration.NormalizeErrorMessage(err),
		errorstack.WithCode(errorstack.CodeValidationFailed),
		errorstack.WithPath(path...),
	)
}

// helixDocumentsHost and helixDocumentsRepository locate helix's own documents on
// the web: https://raw.githubusercontent.com/mountaya/helix.go/<ref>/documents/...
const (
	helixDocumentsHost       = "raw.githubusercontent.com"
	helixDocumentsRepository = "/mountaya/helix.go/"
)

// readFromHelixDocuments reads a document of helix's own, one a description
// references by its raw URL at a tag, a branch or a commit, from the copy embedded
// in this module (documents.OpenAPI), and leaves every other location to the next
// reader.
//
// It is what lets a server start with no network access: a description that
// $refs helix's envelopes from GitHub would otherwise fetch them at every start,
// so an outage, a rate limit or an egress policy would stop the server from
// starting, and a tag moved or deleted upstream would change what its responses
// are validated against at the next restart.
//
// The reference's tag is not looked at. What a server's responses must be
// validated against is the envelope it writes, which is the one of the helix it is
// built with, whichever tag a description was written against; the tag is for a
// reader resolving the description over the network. A document this version of
// helix does not ship is an error, never fetched instead: falling back to the
// network would bring back the dependency, for exactly the document that differs.
func readFromHelixDocuments(loader *openapi3.Loader, location *url.URL) ([]byte, error) {
	name, ok := helixDocumentPath(location)
	if !ok {
		return nil, openapi3.ErrURINotSupported
	}

	data, err := fs.ReadFile(documents.OpenAPI, name)
	if err != nil {
		return nil, fmt.Errorf("%s names %s, which this version of helix does not ship", location, path.Join("documents", name))
	}

	return data, nil
}

// helixDocumentPath returns the path in documents.OpenAPI of the document location
// names, and whether location names one of helix's documents at all. The reference
// is a tag or a branch (refs/tags/<tag>, refs/heads/<branch>) or a single segment
// (a tag, a branch or a commit), followed by documents/.
func helixDocumentPath(location *url.URL) (string, bool) {
	if location.Scheme != "https" || location.Host != helixDocumentsHost {
		return "", false
	}

	rest, ok := strings.CutPrefix(location.Path, helixDocumentsRepository)
	if !ok {
		return "", false
	}

	segments := 1
	if strings.HasPrefix(rest, "refs/tags/") || strings.HasPrefix(rest, "refs/heads/") {
		segments = 3
	}

	parts := strings.SplitN(rest, "/", segments+1)
	if len(parts) != segments+1 {
		return "", false
	}

	name, ok := strings.CutPrefix(parts[segments], "documents/")
	if !ok || !fs.ValidPath(name) {
		return "", false
	}

	return name, true
}

// readFromHTTP reads a description, or a document one references, over HTTP
// within the loader's context. kin-openapi's own reader sends its request without
// one, which leaves a description served by an unresponsive host free to hold New
// forever.
func readFromHTTP(loader *openapi3.Loader, location *url.URL) ([]byte, error) {
	if location.Scheme == "" || location.Host == "" {
		return nil, openapi3.ErrURINotSupported
	}

	req, err := http.NewRequestWithContext(loader.Context, http.MethodGet, location.String(), nil)
	if err != nil {
		return nil, err
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("failed to load %s: server answered with status %d", location, res.StatusCode)
	}

	return io.ReadAll(res.Body)
}

// mergeOpenAPI folds the paths and components of one OpenAPI description into
// another, refusing anything both of them declare.
//
// Only paths and components are merged, because those are the two the validation
// router reads: FindRoute matches a path item, and validating a request or a
// response resolves $refs into the components of the document the route came from.
// Everything else — info, servers, tags, document-level security — belongs to the
// base description, which is why the base is the first one configured.
//
// A path present in both is merged operation by operation rather than replaced, so
// two descriptions may each carry a different method on the same path. That is the
// case this exists for: an API whose public and private halves are described
// separately still registers both on one router.
func mergeOpenAPI(doc *openapi3.T, other *openapi3.T) error {
	if other.Components != nil {
		if doc.Components == nil {
			doc.Components = &openapi3.Components{}
		}

		if err := mergeOpenAPIComponents(doc.Components, other.Components); err != nil {
			return err
		}
	}

	if other.Paths == nil {
		return nil
	}

	if doc.Paths == nil {
		doc.Paths = openapi3.NewPaths()
	}

	for path, item := range other.Paths.Map() {
		existing := doc.Paths.Value(path)
		if existing == nil {
			doc.Paths.Set(path, item)
			continue
		}

		for method, operation := range item.Operations() {
			if existing.GetOperation(method) != nil {
				return fmt.Errorf("%s %s is declared in more than one OpenAPI description", method, path)
			}

			existing.SetOperation(method, operation)
		}
	}

	return nil
}

// mergeOpenAPIComponents folds one component set into another, refusing any name
// both of them declare. A name is global to the merged document, so two
// descriptions declaring the same one describe two different things under one
// name — which the validator would resolve to whichever landed last.
func mergeOpenAPIComponents(into *openapi3.Components, from *openapi3.Components) error {
	if into.Schemas == nil {
		into.Schemas = openapi3.Schemas{}
	}
	for name, schema := range from.Schemas {
		if _, ok := into.Schemas[name]; ok {
			return fmt.Errorf("schema %s is declared in more than one OpenAPI description", name)
		}
		into.Schemas[name] = schema
	}

	if into.Parameters == nil {
		into.Parameters = openapi3.ParametersMap{}
	}
	for name, parameter := range from.Parameters {
		if _, ok := into.Parameters[name]; ok {
			return fmt.Errorf("parameter %s is declared in more than one OpenAPI description", name)
		}
		into.Parameters[name] = parameter
	}

	if into.Headers == nil {
		into.Headers = openapi3.Headers{}
	}
	for name, header := range from.Headers {
		if _, ok := into.Headers[name]; ok {
			return fmt.Errorf("header %s is declared in more than one OpenAPI description", name)
		}
		into.Headers[name] = header
	}

	if into.RequestBodies == nil {
		into.RequestBodies = openapi3.RequestBodies{}
	}
	for name, body := range from.RequestBodies {
		if _, ok := into.RequestBodies[name]; ok {
			return fmt.Errorf("request body %s is declared in more than one OpenAPI description", name)
		}
		into.RequestBodies[name] = body
	}

	if into.Responses == nil {
		into.Responses = openapi3.ResponseBodies{}
	}
	for name, response := range from.Responses {
		if _, ok := into.Responses[name]; ok {
			return fmt.Errorf("response %s is declared in more than one OpenAPI description", name)
		}
		into.Responses[name] = response
	}

	if into.SecuritySchemes == nil {
		into.SecuritySchemes = openapi3.SecuritySchemes{}
	}
	for name, scheme := range from.SecuritySchemes {
		if _, ok := into.SecuritySchemes[name]; ok {
			return fmt.Errorf("security scheme %s is declared in more than one OpenAPI description", name)
		}
		into.SecuritySchemes[name] = scheme
	}

	if into.Examples == nil {
		into.Examples = openapi3.Examples{}
	}
	for name, example := range from.Examples {
		if _, ok := into.Examples[name]; ok {
			return fmt.Errorf("example %s is declared in more than one OpenAPI description", name)
		}
		into.Examples[name] = example
	}

	if into.Links == nil {
		into.Links = openapi3.Links{}
	}
	for name, link := range from.Links {
		if _, ok := into.Links[name]; ok {
			return fmt.Errorf("link %s is declared in more than one OpenAPI description", name)
		}
		into.Links[name] = link
	}

	if into.Callbacks == nil {
		into.Callbacks = openapi3.Callbacks{}
	}
	for name, callback := range from.Callbacks {
		if _, ok := into.Callbacks[name]; ok {
			return fmt.Errorf("callback %s is declared in more than one OpenAPI description", name)
		}
		into.Callbacks[name] = callback
	}

	return nil
}

// isValidURL tests a string to determine if it is a well-structured URL or not.
func isValidURL(link string) (*url.URL, bool) {
	u, err := url.Parse(link)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, false
	}

	return u, true
}
