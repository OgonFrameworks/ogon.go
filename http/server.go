// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Server: net/http wrapper with the router, middleware chain, and lifecycle.
// App code never imports the server type — handlers receive *Ctx only.

package http

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// ServerOptions configures a Server. Defaults are sensible for production:
// 30s read/write/idle timeouts, 10 MiB max body, 10s shutdown drain.
type ServerOptions struct {
	// Addr is the listen address (":8080"). Use UnixSocket to bind a UDS.
	Addr string
	// UnixSocket, when set, replaces Addr with a Unix domain socket.
	UnixSocket string
	// Router is the route table. Required.
	Router *Router
	// Codec registry for content negotiation. Nil → DefaultCodecRegistry.
	Codecs *CodecRegistry
	// Middlewares is the middleware chain applied in order (top → bottom).
	// Default chain is used when nil; see DefaultMiddlewareChain.
	Middlewares []Middleware
	// ReadTimeout, WriteTimeout, IdleTimeout cap the corresponding
	// http.Server fields. Defaults 30s/30s/10s.
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
	// MaxHeaderBytes caps the request header size. Default 1 MiB.
	MaxHeaderBytes int
	// MaxBodyBytes caps the request body size before the bind layer sees it.
	// Default 10 MiB; zero disables (use with care).
	MaxBodyBytes int64
	// DrainTimeout caps the shutdown wait. Default 30s.
	DrainTimeout time.Duration
	// Logger receives lifecycle events. Nil → slog.Default().
	Logger *slog.Logger
	// TLSConfig enables in-process TLS (CORE-030). When nil, plain HTTP.
	TLSConfig *tls.Config
	// OnShutdownStart is invoked at the top of Shutdown. Hook for graceful
	// drain notifications (metrics, external LB dereg, ...).
	OnShutdownStart func()
	// MaxConns caps concurrent in-flight requests. Zero → no cap. When the
	// cap is exceeded, requests block up to ConnWaitTimeout, then return 503.
	MaxConns        int
	ConnWaitTimeout time.Duration
}

// DefaultMiddlewareChain returns the canonical OgonGo chain in contract order
// (PROMPT.md Part VI.3):
//
//	recover → request-id → access-log → timeout → security-headers → handler
//
// The route-group middleware (auth, tenant, rate-limit, csrf, cache) is
// inserted at the route-group boundary (between security-headers and
// handler). otel is opt-in (P9 tracing).
func DefaultMiddlewareChain() []Middleware {
	return []Middleware{
		RecoverMiddleware(),
		RequestIDMiddleware(),
		AccessLogMiddleware(nil),
		TimeoutMiddleware(30 * time.Second),
		SecurityHeadersMiddleware(),
	}
}

// Server wraps net/http.Server with the router, middleware chain, and the
// typed Ctx dispatcher.
type Server struct {
	opts     ServerOptions
	router   *Router
	codecs   *CodecRegistry
	log      *slog.Logger
	server   *http.Server
	listener atomic.Pointer[net.Listener]

	// inflight tracks active connections for graceful drain.
	inflight sync.WaitGroup

	// closed guards Shutdown against double-call.
	closed atomic.Bool

	// connSem is the bounded concurrency semaphore (nil when MaxConns==0).
	connSem chan struct{}
}

// NewServer constructs a Server from opts. The returned server is not
// listening; call Start. Construction is cheap.
func NewServer(opts ServerOptions) (*Server, error) {
	if opts.Router == nil {
		return nil, errors.New("ogon/http: Router is required")
	}
	if opts.Addr == "" && opts.UnixSocket == "" {
		opts.Addr = ":8080"
	}
	if opts.ReadTimeout == 0 {
		opts.ReadTimeout = 30 * time.Second
	}
	if opts.WriteTimeout == 0 {
		opts.WriteTimeout = 30 * time.Second
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = 10 * time.Second
	}
	if opts.MaxHeaderBytes == 0 {
		opts.MaxHeaderBytes = 1 << 20 // 1 MiB
	}
	if opts.MaxBodyBytes == 0 {
		opts.MaxBodyBytes = 10 << 20 // 10 MiB
	}
	if opts.DrainTimeout == 0 {
		opts.DrainTimeout = 30 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	codecs := opts.Codecs
	if codecs == nil {
		codecs = DefaultCodecRegistry
	}
	mws := opts.Middlewares
	if mws == nil {
		mws = DefaultMiddlewareChain()
	}

	s := &Server{
		opts:   opts,
		router: opts.Router,
		codecs: codecs,
		log:    opts.Logger,
	}
	s.router.MaxBodyBytes = opts.MaxBodyBytes
	if opts.MaxConns > 0 {
		s.connSem = make(chan struct{}, opts.MaxConns)
	}

	// Build the chain: initCtx (outermost) → routerMatch → middlewares → dispatcher.
	chain := make([]Middleware, 0, len(mws)+2)
	chain = append(chain, InitCtxMiddleware(opts.Router))
	chain = append(chain, RouterMatchMiddleware(opts.Router, codecs))
	chain = append(chain, mws...)
	handler := Chain(chain, http.HandlerFunc(s.dispatch))
	s.server = &http.Server{
		Addr:              opts.Addr,
		Handler:           handler,
		TLSConfig:         opts.TLSConfig,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       opts.ReadTimeout,
		WriteTimeout:      opts.WriteTimeout,
		IdleTimeout:       opts.IdleTimeout,
		MaxHeaderBytes:    opts.MaxHeaderBytes,
	}
	return s, nil
}

// Handler returns the composed http.Handler for httptest or external serving.
// (DX-P4 escape hatch: PROMPT.md VI.1.)
func (s *Server) Handler() http.Handler { return s.server.Handler }

// Router returns the underlying route table.
func (s *Server) Router() *Router { return s.router }

// Start binds the listener and blocks serving until ctx is cancelled or
// Shutdown is called. Start is safe to call exactly once.
func (s *Server) Start(ctx context.Context) error {
	var ln net.Listener
	var err error
	if s.opts.UnixSocket != "" {
		ln, err = net.Listen("unix", s.opts.UnixSocket)
	} else {
		ln, err = net.Listen("tcp", s.opts.Addr)
	}
	if err != nil {
		return fmt.Errorf("ogon/http: listen failed: %w", err)
	}
	s.listener.Store(&ln)

	s.log.Info("ogon/http listening",
		"addr", s.opts.Addr,
		"unix", s.opts.UnixSocket,
		"tls", s.opts.TLSConfig != nil,
	)

	errCh := make(chan error, 1)
	go func() {
		if s.opts.TLSConfig != nil {
			errCh <- s.server.ServeTLS(ln, "", "")
		} else {
			errCh <- s.server.Serve(ln)
		}
	}()

	select {
	case <-ctx.Done():
		return s.Shutdown()
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// Shutdown drains in-flight requests and stops the server. Idempotent and
// safe for concurrent callers. Honors DrainTimeout.
func (s *Server) Shutdown() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	if s.opts.OnShutdownStart != nil {
		s.opts.OnShutdownStart()
	}
	s.log.Info("ogon/http shutdown: draining")
	ctx, cancel := context.WithTimeout(context.Background(), s.opts.DrainTimeout)
	defer cancel()
	err := s.server.Shutdown(ctx)
	s.inflight.Wait()
	s.log.Info("ogon/http shutdown: complete", "err", err)
	return err
}

// dispatch is the core request dispatcher: route-group middleware → handler.
// Called once per request after the middleware chain has run. The *Ctx is
// already stored in the request context by InitCtxMiddleware; the route and
// params are populated by RouterMatchMiddleware. The dispatcher runs the
// per-route typed middleware chain (Route.Use) then calls the handler.
func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	if s.connSem != nil {
		select {
		case s.connSem <- struct{}{}:
			defer func() { <-s.connSem }()
		default:
			if s.opts.ConnWaitTimeout > 0 {
				t := time.NewTimer(s.opts.ConnWaitTimeout)
				defer t.Stop()
				select {
				case s.connSem <- struct{}{}:
					defer func() { <-s.connSem }()
				case <-t.C:
					problem := NewProblem(http.StatusServiceUnavailable,
						"Service Unavailable",
						"server is at max capacity; retry later")
					_ = problem.Write(w, nil)
					return
				}
			} else {
				problem := NewProblem(http.StatusServiceUnavailable,
					"Service Unavailable",
					"server is at max capacity; retry later")
				_ = problem.Write(w, nil)
				return
			}
		}
	}

	s.inflight.Add(1)
	defer s.inflight.Done()

	c := CtxFromRequest(r)
	if c == nil {
		// Fallback path: server is being served without InitCtxMiddleware.
		// Construct a transient Ctx and release at end.
		c = AcquireCtx(w, r, s.router)
		defer ReleaseCtx(c)
	} else {
		// Re-bind w/r in case outer middlewares wrapped them (e.g., gzip).
		c.w = w
		c.r = r
	}

	// If route matching didn't run (e.g., escape-hatch handler chain),
	// match here as a fallback.
	if c.route == nil {
		match, outcome, allow := s.router.Match(r.Method, r.URL.Path)
		switch outcome {
		case MatchNone:
			_ = c.Problem(NotFoundProblem(r.URL.Path))
			return
		case MatchMethodNotAllowed:
			if len(allow) > 0 {
				w.Header().Set("Allow", joinComma(allow))
			}
			_ = c.Problem(MethodNotAllowedProblem(r.Method, r.URL.Path, joinComma(allow)))
			return
		case MatchOK:
			c.route = match.Route
			c.params = match.Params
		}
	}

	// Run route-group middlewares attached via Route.Use(). These see *Ctx,
	// may short-circuit with c.Abort() or by writing the response.
	if len(c.route.groupMws) > 0 {
		for _, mw := range c.route.groupMws {
			if err := mw(c); err != nil {
				if !c.Written() {
					s.writeError(c, err)
				}
				return
			}
			if c.Aborted() {
				return
			}
		}
	}

	// Dispatch to the handler. Errors MUST produce ProblemDetails; helpers
	// (c.JSON, c.Problem, etc.) write directly. Returned errors are coerced.
	err := c.route.Handler(c)
	if err != nil && !c.Written() {
		s.writeError(c, err)
	}
}

// writeError coerces a handler error into a ProblemDetails response when
// the handler has not already written one. *ProblemDetails pass through;
// other errors become a 500 generic Problem.
func (s *Server) writeError(c *Ctx, err error) {
	if err == nil {
		return
	}
	if p, ok := err.(*ProblemDetails); ok {
		_ = c.Problem(p)
		return
	}
	s.log.Error("ogon/http: unhandled error",
		"err", err,
		"route", c.RouteTemplate(),
		"method", c.Request().Method,
	)
	_ = c.Problem(NewProblem(http.StatusInternalServerError,
		"Internal Server Error", ""))
}

// Listener returns the bound listener, or nil if not started.
func (s *Server) Listener() net.Listener {
	if p := s.listener.Load(); p != nil {
		return *p
	}
	return nil
}

// joinComma joins strings with ", " (RFC 7230 §7 list format).
func joinComma(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += ", " + p
	}
	return out
}

// OnShutdownStartHook installs an additional pre-drain hook (for graceful
// LB deregistration, metrics flushes, etc.).
func (s *Server) OnShutdownStartHook(fn func()) {
	prev := s.opts.OnShutdownStart
	s.opts.OnShutdownStart = func() {
		if prev != nil {
			prev()
		}
		fn()
	}
}
