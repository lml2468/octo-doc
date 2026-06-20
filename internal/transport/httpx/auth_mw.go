package httpx

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-doc/internal/platform/apperr"
)

// requireWriteAuth is chi middleware enforcing a valid write token.
func (s *Server) requireWriteAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		ok, err := s.auth.IsValidWriteToken(r.Context(), token)
		if err != nil {
			writeErr(w, s.logger, err)
			return
		}
		if token == "" || !ok {
			writeErr(w, s.logger, apperr.Unauthorized("", ""))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// maybeRequireReadAuth gates public GET routes behind the write token when
// PRIVATE=1. A failed check returns 404 (not 401) so a private server never
// confirms a doc exists.
func (s *Server) maybeRequireReadAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.Private {
			next(w, r)
			return
		}
		token := bearerToken(r)
		if token != "" {
			ok, err := s.auth.IsValidWriteToken(r.Context(), token)
			if err != nil {
				writeErr(w, s.logger, err)
				return
			}
			if ok {
				next(w, r)
				return
			}
		}
		writeErr(w, s.logger, apperr.NotFound("Not found"))
	}
}
