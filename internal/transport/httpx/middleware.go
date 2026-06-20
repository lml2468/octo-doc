package httpx

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Mininglamp-OSS/octo-doc/internal/platform/apperr"
)

// rateLimiter is a fixed-window limiter keyed by token+IP, in-memory (correct for
// a single instance).
type rateLimiter struct {
	mu     sync.Mutex
	hits   map[string]*window
	window time.Duration
	max    int
}

type window struct {
	count   int
	resetAt time.Time
}

func newRateLimiter(win time.Duration, max int) *rateLimiter {
	return &rateLimiter{hits: map[string]*window{}, window: win, max: max}
}

// allow reports whether the request may proceed; if not, it returns the seconds
// until reset.
func (rl *rateLimiter) allow(token, ip string, now time.Time) (bool, int) {
	if rl.max <= 0 {
		return true, 0
	}
	key := truncate(token, 16) + "|" + ip
	rl.mu.Lock()
	defer rl.mu.Unlock()
	w := rl.hits[key]
	if w == nil || w.resetAt.Before(now) {
		w = &window{count: 0, resetAt: now.Add(rl.window)}
		rl.hits[key] = w
	}
	w.count++
	if w.count > rl.max {
		return false, int(w.resetAt.Sub(now).Seconds()) + 1
	}
	if len(rl.hits) > 10_000 {
		for k, v := range rl.hits {
			if v.resetAt.Before(now) {
				delete(rl.hits, k)
			}
		}
	}
	return true, 0
}

// limit wraps a handler, enforcing the limit. When skipGET is true, GET requests
// pass through unmetered (for mixed read/write paths).
func (s *Server) limit(rl *rateLimiter, skipGET bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if skipGET && r.Method == http.MethodGet {
			next(w, r)
			return
		}
		ok, retry := rl.allow(bearerToken(r), clientIP(r), time.Now())
		if !ok {
			writeErr(w, s.logger, apperr.RateLimited(retry))
			return
		}
		next(w, r)
	}
}

// clientIP extracts the client IP, honoring reverse-proxy headers.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	if xr := r.Header.Get("X-Real-IP"); xr != "" {
		return xr
	}
	return "unknown"
}

// bearerToken extracts a Bearer token from the Authorization header, or "".
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(h) > len(p) && strings.EqualFold(h[:len(p)], p) {
		return strings.TrimSpace(h[len(p):])
	}
	return ""
}
