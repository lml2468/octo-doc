package service

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-doc/internal/config"
	"github.com/Mininglamp-OSS/octo-doc/internal/platform/apperr"
	"github.com/Mininglamp-OSS/octo-doc/internal/storage"
)

// sessionTTLSeconds is the viewer session lifetime (30 days).
const sessionTTLSeconds = 60 * 60 * 24 * 30

// AuthService handles write-token validation, admin bootstrap, viewer sessions,
// and the GitHub Device Flow.
type AuthService struct {
	meta   storage.MetadataStore
	cfg    *config.Config
	github *githubClient
}

// NewAuthService constructs an AuthService.
func NewAuthService(meta storage.MetadataStore, cfg *config.Config) *AuthService {
	return &AuthService{meta: meta, cfg: cfg, github: newGitHubClient(http.DefaultClient)}
}

// Identity is the viewer identity returned to the overlay/API.
type Identity struct {
	Login     string  `json:"login"`
	AvatarURL *string `json:"avatar_url,omitempty"`
	Name      string  `json:"name,omitempty"`
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
// or a static token is configured.
func (s *AuthService) Bootstrap(ctx context.Context) (string, error) {
	if !s.cfg.AllowBootstrap {
		return "", apperr.Forbidden("bootstrap disabled", "bootstrap_disabled")
	}
	if s.cfg.WriteToken != "" {
		return "", apperr.Conflict("a static WRITE_TOKEN is configured", "static_token_configured")
	}
	any, err := s.meta.AnyToken(ctx)
	if err != nil {
		return "", err
	}
	if any {
		return "", apperr.Conflict("already bootstrapped", "already_bootstrapped")
	}
	token := NewToken()
	if err := s.meta.PutToken(ctx, token, storage.TokenRecord{
		Token: token, Created: time.Now().UTC().Format(time.RFC3339), Label: "bootstrap",
	}); err != nil {
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

// IsOwner reports whether a session belongs to the configured owner.
func (s *AuthService) IsOwner(session *storage.Session) bool {
	owner := strings.ToLower(s.cfg.Owner)
	return owner != "" && session != nil && strings.ToLower(session.Login) == owner
}

// StartDeviceFlow starts the GitHub Device Flow.
func (s *AuthService) StartDeviceFlow(ctx context.Context) (*DeviceStart, error) {
	if s.cfg.GitHubClientID == "" {
		return nil, apperr.Validation("auth not configured", "auth_not_configured")
	}
	return s.github.startDeviceFlow(ctx, s.cfg.GitHubClientID)
}

// PollResult is returned by PollDeviceFlow.
type PollResult struct {
	Pending  bool
	SID      string
	Identity Identity
}

// PollDeviceFlow polls the device flow; on success creates a session and returns
// the identity plus new session id.
func (s *AuthService) PollDeviceFlow(ctx context.Context, deviceCode string) (*PollResult, error) {
	if s.cfg.GitHubClientID == "" {
		return nil, apperr.Validation("auth not configured", "auth_not_configured")
	}
	if deviceCode == "" {
		return nil, apperr.Validation("device_code required", "device_code_required")
	}
	poll, err := s.github.pollAccessToken(ctx, s.cfg.GitHubClientID, deviceCode)
	if err != nil {
		return nil, err
	}
	if poll.pending {
		return &PollResult{Pending: true}, nil
	}
	user, err := s.github.fetchUser(ctx, poll.accessToken)
	if err != nil {
		return nil, err
	}
	if user.Login == "" {
		return nil, apperr.Upstream("GitHub returned no login", "no_user", nil)
	}
	sid := NewSessionID()
	name := user.Name
	if name == "" {
		name = user.Login
	}
	var avatar *string
	if user.AvatarURL != "" {
		a := user.AvatarURL
		avatar = &a
	}
	session := storage.Session{Login: user.Login, AvatarURL: avatar, Name: name, Created: time.Now().UTC().Format(time.RFC3339)}
	if err := s.meta.PutSession(ctx, sid, session, sessionTTLSeconds); err != nil {
		return nil, err
	}
	id := Identity{Login: user.Login, Name: name, AvatarURL: avatar}
	return &PollResult{Pending: false, SID: sid, Identity: id}, nil
}

// Logout destroys a session.
func (s *AuthService) Logout(ctx context.Context, sid string) error {
	if sid == "" {
		return nil
	}
	return s.meta.DeleteSession(ctx, sid)
}

// SessionTTLSeconds exposes the cookie max-age.
func (s *AuthService) SessionTTLSeconds() int { return sessionTTLSeconds }

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
