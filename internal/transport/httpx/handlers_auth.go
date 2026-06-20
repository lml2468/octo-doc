package httpx

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-doc/internal/platform/apperr"
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
	session, err := s.auth.GetSession(r.Context(), cookie(r, "tdoc_sid"))
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
		"authConfigured": s.cfg.GitHubClientID != "",
	})
	return nil
}

func (s *Server) handleDeviceStart(w http.ResponseWriter, r *http.Request) error {
	start, err := s.auth.StartDeviceFlow(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, 200, start)
	return nil
}

func (s *Server) handleDevicePoll(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		DeviceCode string `json:"device_code"`
	}
	_ = decodeJSON(r, &body)
	if body.DeviceCode == "" {
		return apperr.Validation("device_code required", "device_code_required")
	}
	res, err := s.auth.PollDeviceFlow(r.Context(), body.DeviceCode)
	if err != nil {
		return err
	}
	if res.Pending {
		writeJSON(w, 200, map[string]any{"pending": true})
		return nil
	}
	setSessionCookie(w, "tdoc_sid", res.SID, s.auth.SessionTTLSeconds(), s.cfg.CookieSecure)
	writeJSON(w, 200, map[string]any{"ok": true, "identity": res.Identity})
	return nil
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) error {
	if err := s.auth.Logout(r.Context(), cookie(r, "tdoc_sid")); err != nil {
		return err
	}
	clearCookie(w, "tdoc_sid", s.cfg.CookieSecure)
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}
