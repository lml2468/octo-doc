package httpx

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

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
}

// Deps bundles the constructor arguments for a Server.
type Deps struct {
	Config    *config.Config
	Logger    *slog.Logger
	Docs      *service.DocService
	Comments  *service.CommentService
	Auth      *service.AuthService
	OverlayJS string
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
	}
}

// Handler builds the HTTP handler with all routes and middleware wired.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()

	writeLimiter := newRateLimiter(s.cfg.RateLimitWindow, s.cfg.RateLimitMax)

	// Health + identity.
	r.Get("/api/ping", s.handlePing)
	r.Get("/healthz", s.handleHealthz)

	// Admin / auth.
	r.Get("/api/admin/bootstrap", s.wrap(s.handleBootstrap))
	r.Get("/api/auth/me", s.wrap(s.handleAuthMe))
	r.Post("/api/auth/device/start", s.cors(s.wrap(s.handleDeviceStart)))
	r.Post("/api/auth/device/poll", s.cors(s.wrap(s.handleDevicePoll)))
	r.Post("/api/auth/logout", s.cors(s.wrap(s.handleLogout)))

	// Documents.
	r.With(s.requireWriteAuth).Method(http.MethodPost, "/api/docs", s.cors(s.limit(writeLimiter, false, s.wrap(s.handlePublish))))
	r.With(s.requireWriteAuth).Method(http.MethodPost, "/api/upload", s.cors(s.limit(writeLimiter, false, s.wrap(s.handlePublish))))
	r.Get("/api/docs/{slug}/versions", s.cors(s.wrap(s.handleVersions)))
	r.With(s.requireWriteAuth).Delete("/api/doc", s.cors(s.wrap(s.handleDeleteDoc)))

	// Rendered docs + export/fork.
	r.Get("/d/{slug}/v/{version}", s.maybeRequireReadAuth(s.secHeaders(s.wrap(s.handleRender))))
	r.Head("/d/{slug}/v/{version}", s.maybeRequireReadAuth(s.secHeaders(s.wrap(s.handleRender))))
	r.Get("/d/{slug}/v/{version}/{kind}", s.maybeRequireReadAuth(s.secHeaders(s.wrap(s.handleForkExport))))

	// Comments + reactions.
	r.Get("/api/comments", s.cors(s.limit(writeLimiter, true, s.wrap(s.handleListComments))))
	r.Post("/api/comments", s.cors(s.limit(writeLimiter, true, s.wrap(s.handleCreateComment))))
	r.Patch("/api/comments", s.cors(s.limit(writeLimiter, true, s.wrap(s.handlePatchComment))))
	r.Delete("/api/comments", s.cors(s.limit(writeLimiter, true, s.wrap(s.handleDeleteComment))))
	r.Post("/api/reactions", s.cors(s.limit(writeLimiter, false, s.wrap(s.handleReact))))
	r.With(s.requireWriteAuth).Post("/api/agent/reply", s.cors(s.limit(writeLimiter, false, s.wrap(s.handleAgentReply))))

	// Pages.
	r.Get("/", s.handleLanding)
	r.Get("/me", s.wrap(s.handleCatalog))

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Not found", http.StatusNotFound)
	})
	return r
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

// cors adds permissive CORS headers for the API (CLI/agents are cross-origin).
func (s *Server) cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
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
