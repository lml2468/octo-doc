package httpx

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Mininglamp-OSS/octo-doc/internal/config"
	"github.com/Mininglamp-OSS/octo-doc/internal/platform/apperr"
)

// decodeJSON decodes the request body into v, tolerating an empty/invalid body.
func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	return json.NewDecoder(r.Body).Decode(v)
}

// sessionCookieName is the cookie that carries the viewer session id.
const sessionCookieName = "tdoc_sid"

// sessionCookie reads the session cookie value, or "".
func sessionCookie(r *http.Request) string {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// clearCookie expires a cookie.
func clearCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: 0, Secure: secure})
}

// requireSlug validates a slug from a string, returning a typed 400 on failure.
func requireSlug(value string) (string, error) {
	slug := config.SafeSlug(value)
	if slug == "" {
		return "", apperr.Validation("invalid or missing slug", "invalid_slug")
	}
	return slug, nil
}

// parseVersionParam parses a numeric version path segment.
func parseVersionParam(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// compactDigits keeps only the digits of a string (for compact ids).
func compactDigits(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			out = append(out, s[i])
		}
	}
	return string(out)
}

// randHex4 returns 4 random bytes as 8 hex chars.
func randHex4() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b)
}
