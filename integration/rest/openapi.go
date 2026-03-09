package rest

import (
	"bytes"
	"io"
	"net/http"
	"net/url"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/uptrace/bunrouter"
)

const (
	spanOpenAPIReq = humanized + ": OpenAPI / Request validation"
	spanOpenAPIRes = humanized + ": OpenAPI / Response validation"
)

/*
responseWriter wraps the standard http.ResponseWriter so we can store additional
values during the request/response lifecycle, such as the status code and the
the response body.
*/
type responseWriter struct {
	http.ResponseWriter

	// status code is the HTTP status code sets in the response header. This allows
	// to ensure if the status code respects the one defined in the OpenAPI
	// description.
	status int

	// buf is the HTTP response body sets by a handler function. This allows to
	// ensure if the body respects the one defined in the OpenAPI description.
	buf *bytes.Buffer
}

/*
Write writes the data to the connection as part of an HTTP reply.
*/
func (rw *responseWriter) Write(b []byte) (int, error) {
	rw.ResponseWriter.Write(b)
	return rw.buf.Write(b)
}

/*
WriteHeader sends an HTTP response header with the provided status code.
*/
func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

/*
middlewareValidation is the HTTP middleware to validate a request/response against
the OpenAPI description passed in the integration's config.
*/
func (r *rest) middlewareValidation(next bunrouter.HandlerFunc) bunrouter.HandlerFunc {
	return func(w http.ResponseWriter, req bunrouter.Request) error {

		// Create a new trace for the OpenAPI middleware. Since there's already a
		// trace in the request's context, spans will be part of the parent trace.
		ctx, spanReq := trace.Start(req.Context(), trace.SpanKindServer, spanOpenAPIReq)

		// Wrap the standard http.ResponseWriter so we can store additional values
		// during the request/response lifecycle, such as the status code and the
		// the response body.
		rw := &responseWriter{
			status:         200,
			ResponseWriter: w,
			buf:            &bytes.Buffer{},
		}

		// Try to find the route in the OpenAPI description. If the path is not found
		// or if the method is not allowed, it's already catched by the router itself
		// so there's no need to handle this here.
		r, params, err := r.oapirouter.FindRoute(req.Request)
		if err != nil {
			spanReq.RecordError("failed to find route", err)
			spanReq.End()
			return next(rw, req)
		}

		// Build the request input for OpenAPI validation. Skip security validation
		// since authentication is handled by the application's own middleware, not
		// by the OpenAPI layer.
		in := &openapi3filter.RequestValidationInput{
			Request:     req.Request,
			PathParams:  params,
			QueryParams: req.URL.Query(),
			Route:       r,
			Options: &openapi3filter.Options{
				MultiError:         true,
				AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
			},
		}

		// Override the request validation error's string to only return the reason
		// of the error, with no additional details.
		in.Options.WithCustomSchemaErrorFunc(func(err *openapi3.SchemaError) string {
			return err.Reason
		})

		// Whatever happens next, make sure to validate the response returned, just
		// like we did for the request. If the response is not valid, an error is
		// recorded but the response is still returned to the client.
		defer func() {
			ctx, spanRes := trace.Start(req.Context(), trace.SpanKindServer, spanOpenAPIRes)

			out := &openapi3filter.ResponseValidationInput{
				RequestValidationInput: in,
				Status:                 rw.status,
				Header:                 rw.Header(),
				Body:                   io.NopCloser(rw.buf),
				Options: &openapi3filter.Options{
					MultiError:            true,
					IncludeResponseStatus: true,
				},
			}

			out.Options.WithCustomSchemaErrorFunc(func(err *openapi3.SchemaError) string {
				return err.Reason
			})

			err = openapi3filter.ValidateResponse(ctx, out)
			if err != nil {
				spanRes.RecordError("failed to validate response", err)
			}

			spanRes.End()
		}()

		// Validate the request against the OpenAPI description. If the request does
		// not respect the description, an error is recorded but the request is still
		// forwarded to the handler. This makes validation observational, matching the
		// behavior of response validation.
		err = openapi3filter.ValidateRequest(ctx, in)
		if err != nil {
			spanReq.RecordError("failed to validate request", err)
		}

		// If we made it here it means the request is valid. We can close the span
		// and move to the next HTTP handler function.
		spanReq.End()
		next(rw, req)

		return nil
	}
}

/*
buildRouterOpenAPI tries to build the router for validating requests and responses
against the OpenAPI description. It returns validation errors in case the
description can not be loaded or if it's not valid.
*/
func (r *rest) buildRouterOpenAPI() (routers.Router, []errorstack.Validation) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	// Load the description from file or from a URL, depending on the path defined
	// in the Config.
	var doc *openapi3.T
	var err error
	u, ok := isValidUrl(r.config.OpenAPI.Description)
	if ok {
		doc, err = loader.LoadFromURI(u)
	} else {
		doc, err = loader.LoadFromFile(r.config.OpenAPI.Description)
	}

	if err != nil {
		return nil, []errorstack.Validation{
			{
				Message: err.Error(),
			},
		}
	}

	// Clear server URLs so the gorillamux router matches any host. Without this,
	// FindRoute fails in local development because the request host (e.g.
	// localhost:8080) doesn't match the host declared in the spec.
	doc.Servers = openapi3.Servers{
		&openapi3.Server{URL: "/"},
	}

	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		return nil, []errorstack.Validation{
			{
				Message: err.Error(),
			},
		}
	}

	return router, nil
}

/*
isValidUrl tests a string to determine if it is a well-structured URL or not.
*/
func isValidUrl(link string) (*url.URL, bool) {
	u, err := url.Parse(link)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, false
	}

	return u, true
}
