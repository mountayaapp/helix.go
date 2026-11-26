package rest

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"
	"github.com/mountaya/helix.go/telemetry/trace"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// operationResponseValidation is the operation response validation is traced and
// measured as.
const operationResponseValidation = "OpenAPI / Response Validation"

// attrKeyOpenAPIValidated records on a request's span whether a description
// declares its operation. A request no description declares is passed through
// unvalidated, and this is what tells the two apart in a trace.
var attrKeyOpenAPIValidated = integration.Key(identifier, "openapi.validated")

// maxValidatedResponseBytes is how much of a response body is kept for
// validation. A body that grows past it is passed through unvalidated rather than
// held in memory: the copy is only ever for the validator, and the response has
// been reaching the client as it was written all along.
const maxValidatedResponseBytes = 1 << 20

// responseValidationOptions are the options response validation runs with, built
// once rather than per request: the validator only reads them. A status the
// operation does not declare is refused, which is the one thing a description
// that fell behind its handler gets wrong first.
var responseValidationOptions = validationOptions(openapi3filter.Options{
	MultiError:            true,
	IncludeResponseStatus: true,
})

// describedKey is the context key middlewareResponseValidation stores the
// operation it found for a request under, for middlewareRequestValidation to read.
type describedKey struct{}

// described is the operation a description declares for a request, found once
// per request.
type described struct {
	route *routers.Route

	// pathParams are the request's path parameters, percent-decoded, and
	// undecodable the entries refusing those that do not decode. See
	// decodePathParams.
	pathParams  map[string]string
	undecodable []errorstack.Entry

	// response is the response being kept for validation, and nil when none is:
	// the response to a HEAD.
	response *responseWriter
}

// middlewareResponseValidation is the outer half of a route's OpenAPI validation.
// It finds the operation a description declares for the request, once, for
// middlewareRequestValidation further in to validate the request against, and
// validates the response against it once the route has answered.
//
// It wraps everything the route runs once matched: the WithPreValidation
// middlewares, the request validation and the handler. A response any of them
// writes is validated, a refusal from authentication as much as the handler's own
// answer; the request validation's refusal alone is not, being this package's own
// envelope rather than the route's.
//
// A request no description declares is passed through, untouched and unvalidated.
// Whether its path exists or its method is allowed is the router's to answer, and
// it answers with the same 404 and 405 a server without a description would;
// neither is a failure of validation.
func (r *rest) middlewareResponseValidation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		entry := oteltrace.SpanFromContext(req.Context())

		route, matched, ok := r.findOperation(req)
		if !ok {
			entry.SetAttributes(attrKeyOpenAPIValidated.Bool(false))
			next.ServeHTTP(w, req)
			return
		}

		entry.SetAttributes(attrKeyOpenAPIValidated.Bool(true))
		pathParams, undecodable := decodePathParams(matched)
		operation := &described{
			route:       route,
			pathParams:  pathParams,
			undecodable: undecodable,
		}

		// A response to HEAD has no body, and the validator has nothing to say about
		// one.
		if req.Method == http.MethodHead {
			next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), describedKey{}, operation)))
			return
		}

		rw, state := newResponseWriter(w, route.Operation.Responses)
		operation.response = state

		req = req.WithContext(context.WithValue(req.Context(), describedKey{}, operation))
		next.ServeHTTP(rw, req)

		r.validateResponse(req.Context(), &openapi3filter.RequestValidationInput{
			Request:     req,
			PathParams:  pathParams,
			QueryParams: req.URL.Query(),
			Route:       route,
			Options:     requestValidationOptions,
		}, state)
	})
}

// findOperation returns the route a description declares for req, with its path
// parameters as the request escaped them, and false when no description declares
// one.
//
// A HEAD no description declares is found as the GET it mirrors. A GET route
// answers HEAD with its own handler, which relies on its request having been
// validated as the GET's; leaving the HEAD unvalidated would hand that handler
// parameters the description refuses.
func (r *rest) findOperation(req *http.Request) (*routers.Route, map[string]string, bool) {
	route, pathParams, err := r.oapirouter.FindRoute(req)
	if err == nil {
		return route, pathParams, true
	}

	if req.Method != http.MethodHead {
		return nil, nil, false
	}

	get := req.WithContext(req.Context())
	get.Method = http.MethodGet

	route, pathParams, err = r.oapirouter.FindRoute(get)
	if err != nil {
		return nil, nil, false
	}

	return route, pathParams, true
}

// validateResponse validates the response a handler wrote against the operation in
// describes. A response that does not conform is recorded as an error on its own
// span, and has reached the client regardless.
func (r *rest) validateResponse(ctx context.Context, in *openapi3filter.RequestValidationInput, rw *responseWriter) {
	if rw.skipped {
		return
	}

	// A handler that wrote nothing at all answered 200 with no body, which is what
	// net/http sent on its behalf.
	status := rw.status
	if status == 0 {
		status = http.StatusOK
	}

	// A body that was not kept is one the description declares no schema for at
	// its media type, and an empty one is exactly what the validator needs to say
	// so: it compares the media type before it reads anything.
	var body []byte
	if rw.body != nil {
		body = rw.body.Bytes()
	}

	ctx, op := r.inst.StartOperation(ctx, trace.SpanKindInternal, operationResponseValidation)
	defer op.End(ctx)

	err := openapi3filter.ValidateResponse(ctx, &openapi3filter.ResponseValidationInput{
		RequestValidationInput: in,
		Status:                 status,
		Header:                 rw.Header(),
		Body:                   io.NopCloser(bytes.NewReader(body)),
		Options:                responseValidationOptions,
	})

	op.RecordError("failed to validate response", err)
}

// responseWriter wraps the standard http.ResponseWriter so the response can be
// validated once the handler returns: it records the status code, and keeps a copy
// of the body when the description declares a schema for it.
//
// Everything written goes to the client as it is written. The copy is never what
// the client receives, so validating a response never delays or withholds it.
type responseWriter struct {
	http.ResponseWriter

	// responses are the responses the operation declares, which decide whether a
	// body is worth keeping.
	responses *openapi3.Responses

	// status is the first final status code written, or zero before one is. That is
	// the one the client receives: net/http ignores any later one.
	status int

	// body is the copy of the response body kept for validation. It is nil when
	// the body is not kept — its media type has no schema in the description — and
	// once the response is skipped.
	body *bytes.Buffer

	// written counts the body bytes that reached the client, for a flush to tell
	// a complete body from a stream (Flush).
	written int64

	// skipped reports that the response is not validated at all: it is streamed,
	// was flushed before its body was complete, or hijacked, switched protocols, or
	// outgrew maxValidatedResponseBytes. A description only describes finite bodies,
	// and a partial one would fail validation for being partial.
	skipped bool
}

// newResponseWriter wraps w for validation against responses, and returns the
// writer to hand to the handler along with the state to validate afterwards.
// The writer can be hijacked exactly when w can.
func newResponseWriter(w http.ResponseWriter, responses *openapi3.Responses) (http.ResponseWriter, *responseWriter) {
	rw := &responseWriter{
		ResponseWriter: w,
		responses:      responses,
	}

	if hijacker, ok := w.(http.Hijacker); ok {
		return &hijackableResponseWriter{responseWriter: rw, Hijacker: hijacker}, rw
	}

	return rw, rw
}

// Unwrap returns the underlying http.ResponseWriter so http.ResponseController can
// reach what this writer does not implement itself, such as the deadline setters.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// WriteHeader sends an HTTP response header with the provided status code, and
// records it when it is the first final one.
func (rw *responseWriter) WriteHeader(status int) {
	if rw.status == 0 && (status >= http.StatusOK || status == http.StatusSwitchingProtocols) {
		rw.begin(status)
	}

	rw.ResponseWriter.WriteHeader(status)
}

// Write writes the data to the connection as part of an HTTP reply, and keeps a
// copy of what reached it when the body is kept for validation. The error is the
// connection's.
func (rw *responseWriter) Write(b []byte) (int, error) {
	if rw.status == 0 {
		rw.begin(http.StatusOK)
	}

	n, err := rw.ResponseWriter.Write(b)
	rw.written += int64(n)
	rw.keep(b[:n])

	return n, err
}

// Flush sends what was written so far to the client. A response flushed before
// its body is complete is a stream: it is no longer kept, and not validated. One
// whose declared Content-Length is all written is complete, a finite body sent
// at once rather than when the handler returns, and is validated as it would be
// without the flush.
func (rw *responseWriter) Flush() {
	if !rw.complete() {
		rw.skip()
	}

	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// ReadFrom copies src into the response. A body that is not kept goes through the
// underlying writer's own ReadFrom, which keeps net/http's zero-copy path; a body
// that is kept goes through Write, which copies it.
func (rw *responseWriter) ReadFrom(src io.Reader) (int64, error) {
	if rw.status == 0 {
		rw.begin(http.StatusOK)
	}

	if rw.body == nil {
		if readerFrom, ok := rw.ResponseWriter.(io.ReaderFrom); ok {
			n, err := readerFrom.ReadFrom(src)
			rw.written += n

			return n, err
		}
	}

	// io.Copy would find this method and recurse into it; writeOnly hides it.
	return io.Copy(writeOnly{rw}, src)
}

// complete reports whether the response declared its length and all of its body
// has been written.
func (rw *responseWriter) complete() bool {
	declared, err := strconv.ParseInt(rw.Header().Get("Content-Length"), 10, 64)

	return err == nil && rw.written == declared
}

// begin records the response's status, and decides from it and from the headers
// — final by now — whether the body is kept for validation.
func (rw *responseWriter) begin(status int) {
	rw.status = status

	if status == http.StatusSwitchingProtocols || isEventStream(rw.Header().Get("Content-Type")) {
		rw.skip()
		return
	}

	if !rw.skipped && rw.declaresSchema(status) {
		rw.body = &bytes.Buffer{}
	}
}

// declaresSchema reports whether the operation declares a schema for a response
// with status and the response's media type. Any other body is one the validator
// never reads, so keeping a copy of it would buy nothing.
func (rw *responseWriter) declaresSchema(status int) bool {
	if rw.responses == nil {
		return false
	}

	ref := rw.responses.Status(status)
	if ref == nil {
		ref = rw.responses.Default()
	}

	if ref == nil || ref.Value == nil {
		return false
	}

	media := ref.Value.Content.Get(rw.Header().Get("Content-Type"))
	return media != nil && media.Schema != nil
}

// keep appends b to the copy kept for validation, giving the copy up once it
// would outgrow maxValidatedResponseBytes.
func (rw *responseWriter) keep(b []byte) {
	if rw.body == nil {
		return
	}

	if rw.body.Len()+len(b) > maxValidatedResponseBytes {
		rw.skip()
		return
	}

	rw.body.Write(b)
}

// skip gives up on validating the response, and releases the copy kept so far.
func (rw *responseWriter) skip() {
	rw.skipped = true
	rw.body = nil
}

// isEventStream reports whether contentType is that of server-sent events, which
// are a stream by definition.
func isEventStream(contentType string) bool {
	return strings.HasPrefix(contentType, "text/event-stream")
}

// hijackableResponseWriter is a responseWriter over a writer that can be
// hijacked.
//
// It is a second type rather than a Hijack method on responseWriter because
// hijacking is feature-detected: a WebSocket library asks whether the writer is
// an http.Hijacker, and a wrapper that always answers yes turns "this connection
// cannot be hijacked" into a failure at hijack time. Under HTTP/2 the real writer
// is not hijackable, and neither is this.
type hijackableResponseWriter struct {
	*responseWriter
	http.Hijacker
}

// Hijack hands the connection over. What happens on it from then on is no longer
// an HTTP response, and is not validated.
func (rw *hijackableResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	rw.skip()

	return rw.Hijacker.Hijack()
}

// writeOnly exposes nothing but Write, so that io.Copy cannot take a shortcut
// back into the method that called it.
type writeOnly struct {
	io.Writer
}
