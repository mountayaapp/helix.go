package rest

import (
	"net/http"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/service"

	"github.com/getkin/kin-openapi/routers"
	"github.com/uptrace/bunrouter"
	"github.com/uptrace/bunrouter/extra/bunrouterotel"
	"github.com/uptrace/bunrouter/extra/reqlog"
)

/*
REST exposes the HTTP REST API functions.
*/
type REST interface {
	GET(path string, handler http.HandlerFunc)
	DELETE(path string, handler http.HandlerFunc)
	PATCH(path string, handler http.HandlerFunc)
	POST(path string, handler http.HandlerFunc)
	PUT(path string, handler http.HandlerFunc)
}

/*
rest represents the rest integration. It respects the integration.Server and
REST interfaces.
*/
type rest struct {

	// svc is the Service this integration belongs to.
	svc *service.Service

	// config holds the Config initially passed when creating a new REST API.
	config *Config

	// bun is the underlying router. This package has been designed to easily
	// switch from one underlying router to another if necessary, in case one goes
	// unmaintained or doesn't meet our requirements anymore.
	bun *bunrouter.CompatRouter

	// server is the standard http.Server used to serve HTTP requests.
	server *http.Server

	// oapirouter is the OpenAPI router used to validate requests and responses
	// against the OpenAPI description passed in Config.
	oapirouter routers.Router
}

/*
New tries to build a new HTTP API server for Config. Returns an error if Config
or OpenAPI description are not valid.
*/
func New(svc *service.Service, cfg Config) (REST, error) {

	// No need to continue if Config is not valid.
	err := cfg.sanitize()
	if err != nil {
		return nil, err
	}

	// Start to build an error stack, so we can add validations as we go.
	stack := errorstack.New("Failed to initialize integration", errorstack.WithIntegration(identifier))
	r := &rest{
		svc:    svc,
		config: &cfg,
	}

	var validations []errorstack.Validation
	r.bun, validations = r.buildRouter()
	if validations != nil {
		stack.WithValidations(validations...)
	}

	// Only try to build the OpenAPI router if enabled in Config.
	if cfg.OpenAPI.Enabled {
		r.oapirouter, validations = r.buildRouterOpenAPI()
		if validations != nil {
			stack.WithValidations(validations...)
		}
	}

	// Stop here if error validations were encountered.
	if stack.HasValidations() {
		return nil, stack
	}

	// Otherwise, try to register the server integration to the service.
	if err := service.Serve(svc, r); err != nil {
		return nil, err
	}

	return r.bun, nil
}

/*
buildRouter tries to build the HTTP router. It comes with opinionated handlers
for 404 and 405 HTTP errors, as well as for the health endpoint.
*/
func (r *rest) buildRouter() (*bunrouter.CompatRouter, []errorstack.Validation) {
	opts := []bunrouter.Option{
		bunrouter.Use(reqlog.NewMiddleware(reqlog.WithEnabled(false))),
		bunrouter.Use(bunrouterotel.NewMiddleware(bunrouterotel.WithClientIP())),
		bunrouter.WithNotFoundHandler(r.handlerNotFound),
		bunrouter.WithMethodNotAllowedHandler(r.handlerMethodNotAllowed),
	}

	if r.config.OpenAPI.Enabled {
		opts = append(opts, bunrouter.WithMiddleware(r.middlewareValidation))
	}

	router := bunrouter.New(opts...).Compat()
	router.Router.GET("/health", r.handlerLiveness)
	router.Router.GET("/ready", r.handlerReadiness)

	return router, nil
}
