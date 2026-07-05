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
// confirms a doc exists. It works for both the HTML render routes and the /v1
// JSON read routes — writeErr renders the 404 as the JSON envelope, which is the
// correct shape for the JSON endpoints and an acceptable body for a blocked HTML
// read (the doc is hidden regardless).
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
