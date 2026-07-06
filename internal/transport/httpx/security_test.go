package httpx_test

import (
	"context"
	"encoding/json"
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
	auth := service.NewAuthService(store, cfg, locker)
	srv := httpx.New(httpx.Deps{
		Config: cfg, Logger: log.New("silent"), Docs: docs, Comments: comments, Auth: auth,
		OverlayJS: "/* overlay */",
		Health:    func(_ context.Context) error { return check() },
	})
	return srv.Handler()
}

// TestDocsPrivateByDefault verifies every doc is private by default: a caller
// with no credential gets 404 (existence hidden) on reads, the author (write
// token) gets through, and a valid share code grants read + comment.
func TestDocsPrivateByDefault(t *testing.T) {
	h := newTestServer(t, nil) // default cfg
	auth := map[string]string{"Authorization": "Bearer test-token", "Content-Type": "application/json"}

	// Publish a doc (author).
	pub := do(t, h, http.MethodPost, "/v1/docs", auth,
		`{"slug":"doc","html":"<html><body><p>hi</p></body></html>"}`)
	if pub.Code != http.StatusOK {
		t.Fatalf("setup publish = %d: %s", pub.Code, pub.Body.String())
	}

	// No credential → 404 on render, versions, and comments (existence hidden).
	for _, target := range []string{"/d/doc/v/1", "/v1/docs/doc/versions", "/v1/comments?slug=doc"} {
		if rec := do(t, h, http.MethodGet, target, nil, ""); rec.Code != http.StatusNotFound {
			t.Errorf("anonymous GET %s = %d; want 404 (private by default)", target, rec.Code)
		}
	}

	// Author (write token) reads everything.
	for _, target := range []string{"/d/doc/v/1", "/v1/docs/doc/versions", "/v1/comments?slug=doc"} {
		if rec := do(t, h, http.MethodGet, target, map[string]string{"Authorization": "Bearer test-token"}, ""); rec.Code == http.StatusNotFound {
			t.Errorf("author GET %s = 404; write token should grant read", target)
		}
	}

	// Mint a share code.
	sh := do(t, h, http.MethodPost, "/v1/docs/doc/share", map[string]string{"Authorization": "Bearer test-token"}, "")
	if sh.Code != http.StatusOK {
		t.Fatalf("share = %d: %s", sh.Code, sh.Body.String())
	}
	var share map[string]any
	_ = json.Unmarshal(sh.Body.Bytes(), &share)
	code, _ := share["data"].(map[string]any)["code"].(string)
	if code == "" {
		t.Fatalf("share returned no code: %s", sh.Body.String())
	}

	// Reader (code as Bearer) reads published + can comment, but NOT the draft.
	codeAuth := map[string]string{"Authorization": "Bearer " + code}
	if rec := do(t, h, http.MethodGet, "/d/doc/v/1", codeAuth, ""); rec.Code != http.StatusOK {
		t.Errorf("reader render with code = %d; want 200", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/v1/comments?slug=doc", codeAuth, ""); rec.Code != http.StatusOK {
		t.Errorf("reader list-comments with code = %d; want 200", rec.Code)
	}
	cm := do(t, h, http.MethodPost, "/v1/comments",
		map[string]string{"Authorization": "Bearer " + code, "Content-Type": "application/json"},
		`{"slug":"doc","version":1,"text":"nice"}`)
	if cm.Code != http.StatusOK {
		t.Errorf("reader comment with code = %d; want 200: %s", cm.Code, cm.Body.String())
	}

	// A wrong code is rejected (404) on read and comment.
	bad := map[string]string{"Authorization": "Bearer deadbeefdeadbeef"}
	if rec := do(t, h, http.MethodGet, "/d/doc/v/1", bad, ""); rec.Code != http.StatusNotFound {
		t.Errorf("wrong code render = %d; want 404", rec.Code)
	}

	// Rotating the code invalidates the old one.
	sh2 := do(t, h, http.MethodPost, "/v1/docs/doc/share", map[string]string{"Authorization": "Bearer test-token"}, "")
	var share2 map[string]any
	_ = json.Unmarshal(sh2.Body.Bytes(), &share2)
	newCode, _ := share2["data"].(map[string]any)["code"].(string)
	if newCode == code || newCode == "" {
		t.Fatalf("rotate did not mint a new code")
	}
	if rec := do(t, h, http.MethodGet, "/d/doc/v/1", codeAuth, ""); rec.Code != http.StatusNotFound {
		t.Errorf("old code after rotate = %d; want 404", rec.Code)
	}
}

// TestCodeCookieExchange verifies a browser ?code= is exchanged for an HttpOnly
// cookie and redirected to a param-free URL, so the secret leaves the address bar.
func TestCodeCookieExchange(t *testing.T) {
	h := newTestServer(t, nil)
	auth := map[string]string{"Authorization": "Bearer test-token", "Content-Type": "application/json"}
	_ = do(t, h, http.MethodPost, "/v1/docs", auth,
		`{"slug":"doc","html":"<html><body><p>hi</p></body></html>"}`)
	sh := do(t, h, http.MethodPost, "/v1/docs/doc/share", map[string]string{"Authorization": "Bearer test-token"}, "")
	var share map[string]any
	_ = json.Unmarshal(sh.Body.Bytes(), &share)
	code, _ := share["data"].(map[string]any)["code"].(string)

	rec := do(t, h, http.MethodGet, "/d/doc/v/1?code="+code, nil, "")
	if rec.Code != http.StatusFound {
		t.Fatalf("?code= first hit = %d; want 302 redirect", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc == "" || contains(loc, "code=") {
		t.Errorf("redirect Location %q must not contain the code", loc)
	}
	setCookie := rec.Header().Get("Set-Cookie")
	if setCookie == "" || !contains(setCookie, "HttpOnly") {
		t.Errorf("expected an HttpOnly capability cookie, got %q", setCookie)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
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

	// Reactions are capability-gated now; use the write token (author) so the
	// request reaches the rate limiter rather than 404ing at the capability check.
	base := map[string]string{"Authorization": "Bearer test-token", "Content-Type": "application/json"}

	got429 := false
	for i := 0; i < 6; i++ {
		// Each request spoofs a distinct XFF; it must be ignored.
		hdr := map[string]string{"X-Forwarded-For": randIP(i)}
		for k, v := range base {
			hdr[k] = v
		}
		rec := do(t, h, http.MethodPost, "/v1/reactions", hdr, `{"slug":"d","comment_id":"c","emoji":"x"}`)
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
