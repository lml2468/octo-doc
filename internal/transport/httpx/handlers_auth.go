package httpx

import (
	"net/http"
)

func (s *Server) handlePing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "service": "tdoc"})
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) error {
	token, err := s.auth.Bootstrap(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true, "token": token})
	return nil
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) error {
	session, err := s.auth.GetSession(r.Context(), sessionCookie(r))
	if err != nil {
		return err
	}
	var identity any
	if session != nil {
		identity = map[string]any{
			"login": session.Login, "avatar_url": session.AvatarURL, "name": session.Name,
		}
	}
	writeJSON(w, 200, map[string]any{
		"identity":       identity,
		"isOwner":        s.auth.IsOwner(session),
		"authConfigured": false,
	})
	return nil
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) error {
	if err := s.auth.Logout(r.Context(), sessionCookie(r)); err != nil {
		return err
	}
	clearCookie(w, sessionCookieName, s.cfg.CookieSecure)
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}
