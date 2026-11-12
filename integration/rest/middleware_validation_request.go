package rest

import (
	"errors"
	"net/http"
	"slices"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
)

// operationRequestValidation is the operation request validation is traced and
// measured as.
const operationRequestValidation = "OpenAPI / Request Validation"

// requestValidationOptions are the options request validation runs with, built
// once rather than per request: the validator only reads them. Security is
// skipped, since authentication is the application's own business, not the
// description's: a route does it in its WithPreValidation middlewares, or in
// Config.Middleware.
var requestValidationOptions = validationOptions(openapi3filter.Options{
	MultiError:         true,
	AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
})

// validationOptions returns opts reporting a schema failure by its reason alone,
// with none of the schema and value the validator would otherwise dump into a
// message a client reads. That reason is the message of an OpenAPI 3.0
// description's failures; an OpenAPI 3.1 description's are reported from the JSON
// Schema validator's own errors instead (schemaErrorEntries), since the reason
// kin-openapi gives them is that validator's whole report.
func validationOptions(opts openapi3filter.Options) *openapi3filter.Options {
	opts.WithCustomSchemaErrorFunc(func(err *openapi3.SchemaError) string {
		return err.Reason
	})

	return &opts
}

// middlewareRequestValidation is the inner half of a route's OpenAPI validation:
// it validates the request against the operation middlewareResponseValidation
// found for it, after the route's WithPreValidation middlewares and right before
// its handler. A request no description declares goes on unvalidated.
//
// A request that does not respect the description is refused here and never
// reaches the handler, which is what makes the description a contract rather than
// documentation: every handler may assume its parameters and body are the shape
// the description declares, and a client is told which value was wrong instead of
// meeting whatever the handler made of it.
//
// This is deliberately not symmetric with response validation. A request is the
// caller's to fix, so refusing it is actionable; a response is the service's own,
// and withholding it would turn a description the service has outgrown into an
// outage for a caller who did nothing wrong.
//
// Path parameters are validated percent-decoded, as the description defines them
// (see decodePathParams). One whose segment does not decode is refused without
// asking the validator anything, since there is no value to validate.
//
// With ConfigOpenAPI.RefuseUndeclaredQuery set, a query parameter the operation
// does not declare, and admitted does not name (WithUndeclaredQuery), is refused
// too, in the same answer as every other failure of the request.
func (r *rest) middlewareRequestValidation(next http.Handler, admitted []string) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		operation, ok := req.Context().Value(describedKey{}).(*described)
		if !ok {
			next.ServeHTTP(rw, req)
			return
		}

		in := &openapi3filter.RequestValidationInput{
			Request:     req,
			PathParams:  operation.pathParams,
			QueryParams: req.URL.Query(),
			Route:       operation.route,
			Options:     requestValidationOptions,
		}

		var undeclared []errorstack.Entry
		if r.config.OpenAPI.RefuseUndeclaredQuery {
			undeclared = undeclaredQueryEntries(operation.route, in.QueryParams, admitted)
		}

		// What is refused before the validator is asked: the path parameters that do
		// not decode, then the query parameters the operation does not declare.
		refused := slices.Concat(operation.undecodable, undeclared)

		ctx, validation := r.inst.StartOperation(req.Context(), trace.SpanKindInternal, operationRequestValidation)

		// The validator is not asked about a request whose path does not decode: it
		// would refuse each such parameter a second time, against the segment as
		// written rather than any value the client meant.
		var err error
		if len(operation.undecodable) == 0 {
			err = openapi3filter.ValidateRequest(ctx, in)
		}

		if err != nil || len(refused) > 0 {
			switch {
			case err != nil:
				validation.RecordError("failed to validate request", err)
			case len(operation.undecodable) > 0:
				validation.RecordError("failed to validate request", errUndecodablePath)
			default:
				validation.RecordError("failed to validate request", errUndeclaredQuery)
			}
			validation.End(ctx)

			// The refusal is this package's own envelope, not a response of the
			// route's: there is nothing to validate it against.
			if operation.response != nil {
				operation.response.skip()
			}

			refuseRequest(rw, req, err, refused)
			return
		}

		validation.End(ctx)
		next.ServeHTTP(rw, req)
	})
}

// errUndeclaredQuery is what a request refused for its undeclared query
// parameters alone is recorded with on its trace.
var errUndeclaredQuery = errors.New("query parameters the operation does not declare")

// errUndecodablePath is what a request refused for a path parameter that does
// not percent-decode is recorded with on its trace.
var errUndecodablePath = errors.New("path parameters that do not percent-decode")

// refuseRequest answers a request that failed validation: with 413 when its body
// was over the limit, and otherwise with the entries refused before the validator
// was asked (a path parameter that does not decode, a query parameter the
// operation does not declare), then one per non-conforming value err reports, in
// that order. err is nil when refused holds the request's only failures.
//
// The entries are held to the limits of a refusal on their way out (boundEntries),
// so the answer is bounded and valid UTF-8 whatever the request held.
func refuseRequest(rw http.ResponseWriter, req *http.Request, err error, refused []errorstack.Entry) {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		NewResponseError[NoMetadata](req).
			SetStatus(http.StatusRequestEntityTooLarge).
			Write(rw)
		return
	}

	res := NewResponseError[NoMetadata](req).SetStatus(requestValidationStatus)

	// The seeded fallback entry from SetStatus stands when nothing could be
	// extracted, so a refusal always carries a readable envelope even for an error
	// shape the flattening does not recognise.
	entries := boundEntries(append(slices.Clone(refused), requestValidationEntries(err)...))
	if len(entries) > 0 {
		res.SetValidations(entries...)
	}

	res.Write(rw)
}
