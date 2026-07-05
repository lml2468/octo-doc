package httpx_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Mininglamp-OSS/octo-doc/internal/config"
	"github.com/Mininglamp-OSS/octo-doc/internal/platform/log"
	"github.com/Mininglamp-OSS/octo-doc/internal/platform/sluglock"
	"github.com/Mininglamp-OSS/octo-doc/internal/service"
	"github.com/Mininglamp-OSS/octo-doc/internal/storage/memory"
	"github.com/Mininglamp-OSS/octo-doc/internal/transport/httpx"
)

var errUnhealthy = errors.New("store down")

// newTestServerWithHealth builds a server whose /healthz uses the given check.
func newTestServerWithHealth(t *testing.T, check func() error) http.Handler {
	t.Helper()
	cfg := &config.Config{WriteToken: "t", MaxHTMLBytes: 1 << 20, RepoURL: "https://x", RateLimitMax: 0}
	store := memory.New()
	locker := sluglock.NewMemory()
	comments := service.NewCommentService(store, locker)
	docs := service.NewDocService(store, store, comments, locker, cfg.BaseURL, cfg.MaxHTMLBytes)
	auth := service.NewAuthService(store, cfg)
	srv := httpx.New(httpx.Deps{
		Config: cfg, Logger: log.New("silent"), Docs: docs, Comments: comments, Auth: auth,
		OverlayJS: "/* overlay */",
		Health:    func(_ context.Context) error { return check() },
	})
	return srv.Handler()
}

// TestPrivateGatesJSONReads verifies PRIVATE=1 hides the /v1 JSON read endpoints
// (comments, versions) behind the write token, returning 404 (not 401) to a
// token-less caller so existence is never confirmed.
func TestPrivateGatesJSONReads(t *testing.T) {
	cfg := &config.Config{
		WriteToken: "secret", Private: true, MaxHTMLBytes: 1 << 20,
		RepoURL: "https://x", RateLimitMax: 0,
	}
	h := newTestServer(t, cfg)

	// Publish a doc so the authorized read path has real content to return (a
	// nonexistent doc would 404 regardless of auth, masking the gate).
	pub := do(t, h, http.MethodPost, "/v1/docs",
		map[string]string{"Authorization": "Bearer secret", "Content-Type": "application/json"},
		`{"slug":"doc","html":"<html><body><p>hi</p></body></html>"}`)
	if pub.Code != http.StatusOK {
		t.Fatalf("setup publish = %d: %s", pub.Code, pub.Body.String())
	}

	for _, target := range []string{"/v1/comments?slug=doc", "/v1/docs/doc/versions"} {
		rec := do(t, h, http.MethodGet, target, nil, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("PRIVATE GET %s without token = %d; want 404", target, rec.Code)
		}
		rec = do(t, h, http.MethodGet, target, map[string]string{"Authorization": "Bearer secret"}, "")
		if rec.Code == http.StatusNotFound {
			t.Errorf("PRIVATE GET %s WITH token = 404; token should grant read", target)
		}
	}
}

// TestPublicJSONReadsOpenByDefault confirms the gating is conditional on PRIVATE.
func TestPublicJSONReadsOpenByDefault(t *testing.T) {
	h := newTestServer(t, nil) // default cfg: not private
	rec := do(t, h, http.MethodGet, "/v1/comments?slug=doc", nil, "")
	if rec.Code == http.StatusNotFound {
		t.Errorf("public GET /v1/comments = 404; reads should be open when not PRIVATE")
	}
}

// TestRateLimitIgnoresSpoofedXFF verifies that, without TrustProxyHeaders, a
// client cannot mint a fresh rate-limit bucket by rotating X-Forwarded-For — the
// socket peer (shared in httptest) is used, so the shared limit still applies.
func TestRateLimitIgnoresSpoofedXFF(t *testing.T) {
	cfg := &config.Config{
		WriteToken: "test-token", MaxHTMLBytes: 1 << 20, RepoURL: "https://x",
		RateLimitWindow:   60_000_000_000, // 1m in ns
		RateLimitMax:      2,
		TrustProxyHeaders: false,
	}
	h := newTestServer(t, cfg)

	got429 := false
	for i := 0; i < 6; i++ {
		// Each request spoofs a distinct XFF; it must be ignored.
		rec := do(t, h, http.MethodPost, "/v1/reactions",
			map[string]string{"X-Forwarded-For": randIP(i)}, `{"slug":"d","comment_id":"c","emoji":"x"}`)
		if rec.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Error("spoofed X-Forwarded-For evaded the rate limit (headers trusted when they should not be)")
	}
}

func randIP(i int) string {
	return "10.0.0." + string(rune('1'+i))
}

// TestHealthzReportsUnhealthy verifies /healthz returns 503 when a store health
// check fails.
func TestHealthzReportsUnhealthy(t *testing.T) {
	h := newTestServerWithHealth(t, func() error { return errUnhealthy })
	rec := do(t, h, http.MethodGet, "/healthz", nil, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unhealthy /healthz = %d; want 503", rec.Code)
	}

	ok := newTestServerWithHealth(t, func() error { return nil })
	rec = do(t, ok, http.MethodGet, "/healthz", nil, "")
	if rec.Code != http.StatusOK {
		t.Errorf("healthy /healthz = %d; want 200", rec.Code)
	}
}
