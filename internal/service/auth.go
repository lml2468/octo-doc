package service

import (
	"context"
	"crypto/subtle"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-doc/internal/config"
	"github.com/Mininglamp-OSS/octo-doc/internal/platform/apperr"
	"github.com/Mininglamp-OSS/octo-doc/internal/platform/sluglock"
	"github.com/Mininglamp-OSS/octo-doc/internal/storage"
)

// sessionTTLSeconds is the viewer session lifetime (30 days).
const sessionTTLSeconds = 60 * 60 * 24 * 30

// AuthService handles write-token validation, admin bootstrap, and viewer
// sessions.
//
// Viewer identity is intentionally minimal right now: there is no built-in login
// provider, so comments are anonymous. The session machinery (GetSession,
// CreateSession, Logout, the sessions table) is kept as the seam a future Octo
// unified login will plug into — it only needs to mint a session and the rest of
// the system already consumes session.Login generically.
type AuthService struct {
	meta storage.MetadataStore
	cfg  *config.Config
	lock sluglock.Locker
}

// NewAuthService constructs an AuthService. The locker serializes the one-shot
// bootstrap check-and-set; pass the shared (distributed) locker so bootstrap is
// atomic across app instances, not just within one process.
func NewAuthService(meta storage.MetadataStore, cfg *config.Config, lock sluglock.Locker) *AuthService {
	return &AuthService{meta: meta, cfg: cfg, lock: lock}
}

// IsValidWriteToken does a constant-time check that token is the static or a
// provisioned write token.
func (s *AuthService) IsValidWriteToken(ctx context.Context, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	if s.cfg.WriteToken != "" && constantTimeEqual(token, s.cfg.WriteToken) {
		return true, nil
	}
	rec, err := s.meta.GetToken(ctx, token)
	if err != nil {
		return false, err
	}
	return rec != nil, nil
}

// Bootstrap mints the first write token. One-shot: errors once any token exists
// or a static token is configured. The check-and-set runs under a lock so two
// concurrent bootstraps can't both mint a "first" token (single-instance
// guarantee; a multi-instance deployment should disable ALLOW_BOOTSTRAP and
// provision a token out of band).
func (s *AuthService) Bootstrap(ctx context.Context) (string, error) {
	if !s.cfg.AllowBootstrap {
		return "", apperr.Forbidden("bootstrap disabled", "bootstrap_disabled")
	}
	if s.cfg.WriteToken != "" {
		return "", apperr.Conflict("a static WRITE_TOKEN is configured", "static_token_configured")
	}
	var token string
	err := s.lock.With(ctx, "__bootstrap__", func() error {
		exists, aerr := s.meta.AnyToken(ctx)
		if aerr != nil {
			return aerr
		}
		if exists {
			return apperr.Conflict("already bootstrapped", "already_bootstrapped")
		}
		token = NewToken()
		return s.meta.PutToken(ctx, token, storage.TokenRecord{
			Token: token, Created: time.Now().UTC().Format(time.RFC3339), Label: "bootstrap",
		})
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// GetSession resolves a session from its id, or nil.
func (s *AuthService) GetSession(ctx context.Context, sid string) (*storage.Session, error) {
	if sid == "" {
		return nil, nil
	}
	return s.meta.GetSession(ctx, sid)
}

// CreateSession persists a viewer session and returns its id. This is the seam a
// future login provider (e.g. Octo unified login) calls once it has an
// authenticated identity; nothing GitHub-specific lives here.
func (s *AuthService) CreateSession(ctx context.Context, login, name string, avatarURL *string) (string, error) {
	sid := NewSessionID()
	if name == "" {
		name = login
	}
	session := storage.Session{
		Login: login, Name: name, AvatarURL: avatarURL,
		Created: time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.meta.PutSession(ctx, sid, session, sessionTTLSeconds); err != nil {
		return "", err
	}
	return sid, nil
}

// IsOwner reports whether a session belongs to the configured owner.
func (s *AuthService) IsOwner(session *storage.Session) bool {
	owner := strings.ToLower(s.cfg.Owner)
	return owner != "" && session != nil && strings.ToLower(session.Login) == owner
}

// Logout destroys a session.
func (s *AuthService) Logout(ctx context.Context, sid string) error {
	if sid == "" {
		return nil
	}
	return s.meta.DeleteSession(ctx, sid)
}

// LoginEnabled reports whether a login provider is configured. It is the single
// source of truth for the overlay's authConfigured flag. There is no built-in
// provider yet, so it is always false (commenting is anonymous); a future Octo
// unified login flips this on in one place.
func (s *AuthService) LoginEnabled() bool { return false }

// SessionTTLSeconds exposes the cookie max-age.
func (s *AuthService) SessionTTLSeconds() int { return sessionTTLSeconds }

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
