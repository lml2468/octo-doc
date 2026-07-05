package httpx

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) handlePing(w http.ResponseWriter, _ *http.Request) {
	writeData(w, 200, map[string]any{"ok": true, "service": "octo-doc"})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if s.health != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.health(ctx); err != nil {
			if s.logger != nil {
				s.logger.Error("healthz check failed", "err", err.Error())
			}
			writeData(w, http.StatusServiceUnavailable, map[string]any{"ok": false})
			return
		}
	}
	writeData(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) error {
	token, err := s.auth.Bootstrap(r.Context())
	if err != nil {
		return err
	}
	writeData(w, 200, map[string]any{"token": token})
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
	writeData(w, 200, map[string]any{
		"identity":       identity,
		"isOwner":        s.auth.IsOwner(session),
		"authConfigured": s.auth.LoginEnabled(),
	})
	return nil
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) error {
	if err := s.auth.Logout(r.Context(), sessionCookie(r)); err != nil {
		return err
	}
	clearCookie(w, sessionCookieName, s.cfg.CookieSecure)
	writeData(w, 200, map[string]any{"ok": true})
	return nil
}
