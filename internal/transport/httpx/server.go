package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Mininglamp-OSS/octo-doc/internal/config"
	"github.com/Mininglamp-OSS/octo-doc/internal/service"
)

// Server holds the dependencies shared by all handlers.
type Server struct {
	cfg       *config.Config
	logger    *slog.Logger
	docs      *service.DocService
	comments  *service.CommentService
	auth      *service.AuthService
	overlayJS string
	health    func(context.Context) error
}

// Deps bundles the constructor arguments for a Server.
type Deps struct {
	Config    *config.Config
	Logger    *slog.Logger
	Docs      *service.DocService
	Comments  *service.CommentService
	Auth      *service.AuthService
	OverlayJS string
	// Health verifies backing stores are reachable (readiness probe). Optional; a
	// nil Health means /healthz reports liveness only.
	Health func(context.Context) error
}

// New constructs a Server.
func New(d Deps) *Server {
	return &Server{
		cfg:       d.Config,
		logger:    d.Logger,
		docs:      d.Docs,
		comments:  d.Comments,
		auth:      d.Auth,
		overlayJS: d.OverlayJS,
		health:    d.Health,
	}
}

// Handler builds the HTTP handler with all routes and middleware wired.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()

	writeLimiter := newRateLimiter(s.cfg.RateLimitWindow, s.cfg.RateLimitMax)

	// Container liveness probe — not a versioned REST resource, stays at root.
	r.Get("/healthz", s.handleHealthz)

	// All JSON APIs live under /v1 (the single current API version). Handlers
	// emit the {data}/{error} envelope; the chi mount provides the /v1 prefix.
	r.Route("/v1", func(r chi.Router) {
		// Health + identity.
		r.Get("/ping", s.handlePing)

		// Admin / auth. Viewer identity is anonymous for now (no built-in login
		// provider); /auth/me reports it and logout clears any future session.
		r.Post("/admin/bootstrap", s.cors(s.wrap(s.handleBootstrap)))
		r.Get("/auth/me", s.wrap(s.handleAuthMe))
		r.Post("/auth/logout", s.cors(s.wrap(s.handleLogout)))

		// Documents.
		r.With(s.requireWriteAuth).Method(http.MethodPost, "/docs", s.cors(s.limit(writeLimiter, false, s.wrap(s.handlePublish))))
		r.Get("/docs/{slug}/versions", s.cors(s.maybeRequireReadAuth(s.wrap(s.handleVersions))))
		r.With(s.requireWriteAuth).Delete("/docs/{slug}", s.cors(s.wrap(s.handleDeleteDoc)))

		// Comments + reactions.
		r.Get("/comments", s.cors(s.maybeRequireReadAuth(s.limit(writeLimiter, true, s.wrap(s.handleListComments)))))
		r.Post("/comments", s.cors(s.limit(writeLimiter, true, s.wrap(s.handleCreateComment))))
		r.Patch("/comments", s.cors(s.limit(writeLimiter, true, s.wrap(s.handlePatchComment))))
		r.Delete("/comments", s.cors(s.limit(writeLimiter, true, s.wrap(s.handleDeleteComment))))
		r.Post("/reactions", s.cors(s.limit(writeLimiter, false, s.wrap(s.handleReact))))
		r.With(s.requireWriteAuth).Post("/agent/replies", s.cors(s.limit(writeLimiter, false, s.wrap(s.handleAgentReply))))
	})

	// Rendered docs + export/fork. These return browser HTML (overlay injected),
	// not the JSON envelope, so they keep the /d/ document-URL scheme.
	r.Get("/d/{slug}/v/{version}", s.maybeRequireReadAuth(s.secHeaders(s.wrap(s.handleRender))))
	r.Head("/d/{slug}/v/{version}", s.maybeRequireReadAuth(s.secHeaders(s.wrap(s.handleRender))))
	r.Get("/d/{slug}/v/{version}/{kind}", s.maybeRequireReadAuth(s.secHeaders(s.wrap(s.handleForkExport))))

	// Pages (browser HTML).
	r.Get("/", s.handleLanding)
	r.Get("/me", s.wrap(s.handleCatalog))

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Not found", http.StatusNotFound)
	})
	return middleware.RequestID(s.accessLog(r))
}

// handlerFunc is a handler that may return an error, mapped centrally.
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

// wrap adapts a handlerFunc into an http.HandlerFunc, routing errors to writeErr.
func (s *Server) wrap(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			writeErr(w, s.logger, err)
		}
	}
}

// cors adds CORS headers for the API. Reads (GET, and OPTIONS preflights for a
// GET) are allowed from any origin, since reads are safe/idempotent and used by
// CLIs and agents. Mutating requests — and preflights for them — echo the request
// origin only if it is in the configured CORSOrigins allowlist; with no allowlist,
// no ACAO is sent on writes, so a browser blocks cross-origin mutations
// (same-origin still works).
func (s *Server) cors(next http.HandlerFunc) http.HandlerFunc {
	allowed := map[string]struct{}{}
	for _, o := range s.cfg.CORSOrigins {
		allowed[o] = struct{}{}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		// Classify by the effective method: for a preflight, that is the method the
		// real request will use (Access-Control-Request-Method), not OPTIONS itself —
		// otherwise a write preflight would be treated as a read and wrongly get *.
		effective := r.Method
		if r.Method == http.MethodOptions {
			if reqMethod := r.Header.Get("Access-Control-Request-Method"); reqMethod != "" {
				effective = reqMethod
			}
		}
		isRead := effective == http.MethodGet || effective == http.MethodHead
		switch {
		case isRead:
			w.Header().Set("Access-Control-Allow-Origin", "*")
		case origin != "":
			if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

// secHeaders attaches the document security headers to /d/* responses.
func (s *Server) secHeaders(next http.HandlerFunc) http.HandlerFunc {
	headers := docSecurityHeaders(s.cfg.FrameAncestors)
	return func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		next(w, r)
	}
}

// docSecurityHeaders builds the CSP + framing headers for rendered documents.
func docSecurityHeaders(frameAncestors string) map[string]string {
	csp := strings.Join([]string{
		"default-src 'self' data: blob: https:",
		"script-src 'self' 'unsafe-inline' 'unsafe-eval' data: blob: https:",
		"style-src 'self' 'unsafe-inline' https:",
		"img-src 'self' data: blob: https:",
		"font-src 'self' data: https:",
		"connect-src 'self' https:",
		"base-uri 'self'",
		"frame-ancestors " + frameAncestors,
	}, "; ")
	xfo := "SAMEORIGIN"
	if frameAncestors == "'none'" {
		xfo = "DENY"
	}
	return map[string]string{
		"Content-Security-Policy": csp,
		"X-Frame-Options":         xfo,
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
	}
}
