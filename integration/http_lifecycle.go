package integration

import (
	"context"
	"errors"
	stdlog "log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/telemetry/log"
)

// Defaults an HTTPLifecycle applies to the timeouts of the server it runs, when
// the server leaves them unset. net/http's own default for each is no limit at
// all, which lets a client that sends its headers one byte a minute hold a
// connection — and a goroutine — open forever.
const (
	DefaultReadHeaderTimeout = 10 * time.Second
	DefaultIdleTimeout       = 120 * time.Second
)

// Ensure *HTTPLifecycle complies to the Server type.
var _ Server = (*HTTPLifecycle)(nil)

// HTTPLifecycle runs an *http.Server as a Server: it is the Start, Stop and
// CheckHealth that every HTTP server integration needs and that were written
// out once per integration before.
//
// Build it in the integration's constructor, register it with service.Serve, and
// keep it away from the handle returned to the application: it carries the
// lifecycle the Service owns.
type HTTPLifecycle struct {
	server *http.Server

	// cancel cancels the context every request's context derives from. It is how
	// a request that does not end on its own — a stream — is told to end when
	// draining runs out of time.
	cancel context.CancelFunc
}

// NewHTTPLifecycle prepares server to be run by a Service.
//
// base is the context every request's context derives from. Integrations pass
// service.Context(context.Background(), svc), so that a handler's context carries
// the Service's logger and tracer — which is what keeps telemetry/log and
// telemetry/trace on the Service's providers when the Service was built with
// Config.SkipGlobals.
//
// It sets the server's BaseContext, and its ReadHeaderTimeout, IdleTimeout and
// ErrorLog when they are unset. net/http's own error log — TLS handshake failures,
// recovered panics — goes to the helix logger instead of the standard library's.
func NewHTTPLifecycle(base context.Context, server *http.Server) *HTTPLifecycle {
	ctx, cancel := context.WithCancel(base)

	server.BaseContext = func(net.Listener) context.Context { return ctx }

	if server.ReadHeaderTimeout == 0 {
		server.ReadHeaderTimeout = DefaultReadHeaderTimeout
	}
	if server.IdleTimeout == 0 {
		server.IdleTimeout = DefaultIdleTimeout
	}
	if server.ErrorLog == nil {
		server.ErrorLog = stdlog.New(errorLogWriter{ctx: ctx}, "", 0)
	}

	return &HTTPLifecycle{server: server, cancel: cancel}
}

// Start listens and serves until the server is stopped. It serves TLS when the
// server has a TLSConfig.
//
// It is not tied to ctx: a server stops when the Service stops it, which is what
// lets in-flight requests drain rather than be cut off the moment a signal
// arrives.
func (l *HTTPLifecycle) Start(ctx context.Context) error {
	var err error
	if l.server.TLSConfig != nil {
		err = l.server.ListenAndServeTLS("", "")
	} else {
		err = l.server.ListenAndServe()
	}

	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errorstack.Wrap(err, "Failed to start server",
			errorstack.WithExtension("reason", NormalizeErrorMessage(err)),
		)
	}

	return nil
}

// Stop drains the server within ctx's deadline. It is safe to call on a server
// that never started.
//
// Half the remaining budget goes to a graceful drain. A request still running
// after that is one that does not end on its own — a server-sent event stream, a
// long poll — so its context is cancelled to tell it to, and it gets the rest of
// the budget. Whatever is still open when the budget runs out is closed: the
// alternative is a shutdown that never finishes, and the telemetry flushed after
// it never sent.
func (l *HTTPLifecycle) Stop(ctx context.Context) error {
	defer l.cancel()

	drainCtx := ctx
	if deadline, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		drainCtx, cancel = context.WithTimeout(ctx, time.Until(deadline)/2)
		defer cancel()
	}

	if err := l.server.Shutdown(drainCtx); err == nil {
		return nil
	}

	l.cancel()
	if err := l.server.Shutdown(ctx); err == nil {
		return nil
	}

	err := l.server.Close()
	if err == nil {
		err = ctx.Err()
	}

	return errorstack.Wrap(err, "Failed to gracefully stop server",
		errorstack.WithExtension("reason", NormalizeErrorMessage(err)),
	)
}

// CheckHealth reports the server healthy as soon as it is serving; readiness of
// the wider application is the readiness probe's business, not the listener's.
func (l *HTTPLifecycle) CheckHealth(ctx context.Context) error {
	return nil
}

// errorLogWriter writes net/http's error log lines to the helix logger.
type errorLogWriter struct {
	ctx context.Context
}

// Write logs p as a warning. It never fails: a log line that cannot be written is
// not worth failing the server over.
func (w errorLogWriter) Write(p []byte) (int, error) {
	log.Warn(w.ctx, strings.TrimSpace(string(p)))
	return len(p), nil
}
