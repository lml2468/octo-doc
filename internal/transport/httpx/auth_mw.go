package httpx

import (
	"net/http"

	"github.com/lml2468/octo-doc/internal/platform/apperr"
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
