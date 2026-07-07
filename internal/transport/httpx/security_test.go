package httpx_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/lml2468/octo-doc/internal/config"
	"github.com/lml2468/octo-doc/internal/platform/log"
	"github.com/lml2468/octo-doc/internal/platform/sluglock"
	"github.com/lml2468/octo-doc/internal/service"
	"github.com/lml2468/octo-doc/internal/storage"
	"github.com/lml2468/octo-doc/internal/storage/memory"
	"github.com/lml2468/octo-doc/internal/transport/httpx"
)

// capCookie builds the per-doc capability cookie header value the way the server
// names it (octo_cap_<hashslug>), so tests can present a cookie credential.
func capCookie(slug, value string) string {
	return "octo_cap_" + storage.HashSlug(slug) + "=" + value
}

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

// TestAuthorMutationsViaCookie verifies author-only mutations (share, draft
// promote) accept the write token via the per-doc cookie, not just the Bearer
// header — the path a browser Publish/Share button takes.
func TestAuthorMutationsViaCookie(t *testing.T) {
	h := newTestServer(t, nil)
	auth := map[string]string{"Authorization": "Bearer test-token", "Content-Type": "application/json"}
	_ = do(t, h, http.MethodPost, "/v1/docs", auth,
		`{"slug":"br","html":"<html><body><p>hi</p></body></html>"}`)

	// The author's write token, delivered as a cookie (as a browser would after
	// the ?code=<write-token> exchange), authorizes share.
	cookie := capCookie("br", "test-token")
	rec := do(t, h, http.MethodPost, "/v1/docs/br/share", map[string]string{"Cookie": cookie}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("share via cookie = %d; want 200: %s", rec.Code, rec.Body.String())
	}

	// A save-draft + promote via cookie must also work (browser Publish button).
	rec = do(t, h, http.MethodPut, "/v1/docs/br/draft",
		map[string]string{"Cookie": cookie, "Content-Type": "application/json"},
		`{"html":"<html><body><h1>draft</h1></body></html>"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("draft save via cookie = %d; want 200: %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, http.MethodPost, "/v1/docs/br/draft/promote", map[string]string{"Cookie": cookie}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("promote via cookie = %d; want 200: %s", rec.Code, rec.Body.String())
	}

	// A reader code cookie must NOT authorize author mutations.
	sh := do(t, h, http.MethodPost, "/v1/docs/br/share", map[string]string{"Authorization": "Bearer test-token"}, "")
	var share map[string]any
	_ = json.Unmarshal(sh.Body.Bytes(), &share)
	readerCode, _ := share["data"].(map[string]any)["code"].(string)
	readerCookie := capCookie("br", readerCode)
	rec = do(t, h, http.MethodPost, "/v1/docs/br/draft/promote", map[string]string{"Cookie": readerCookie}, "")
	if rec.Code == http.StatusOK {
		t.Error("a reader code must not authorize promote")
	}
}

// TestFreshCodeBeatsStaleCookie guards the credential-precedence fix: a browser
// holding an old capability cookie that is then handed a fresh valid ?code= link
// must be authorized by the code, not shadowed by the stale cookie. Covers both
// failure modes: (a) a revoked reader code cut off despite a valid new code, and
// (b) an author's ?code=<write-token> blocked by a pre-existing reader cookie.
func TestFreshCodeBeatsStaleCookie(t *testing.T) {
	h := newTestServer(t, nil)
	auth := map[string]string{"Authorization": "Bearer test-token", "Content-Type": "application/json"}
	_ = do(t, h, http.MethodPost, "/v1/docs", auth,
		`{"slug":"rot","html":"<html><body><p>hi</p></body></html>"}`)

	mintCode := func() string {
		sh := do(t, h, http.MethodPost, "/v1/docs/rot/share", map[string]string{"Authorization": "Bearer test-token"}, "")
		var share map[string]any
		_ = json.Unmarshal(sh.Body.Bytes(), &share)
		code, _ := share["data"].(map[string]any)["code"].(string)
		return code
	}

	// (a) Rotate: an old code's cookie must not block a freshly rotated code link.
	oldCode := mintCode()
	newCode := mintCode() // rotation invalidates oldCode's hash
	staleCookie := capCookie("rot", oldCode)
	rec := do(t, h, http.MethodGet, "/d/rot/v/1?code="+newCode, map[string]string{"Cookie": staleCookie}, "")
	if rec.Code != http.StatusFound {
		t.Fatalf("fresh code with stale cookie = %d; want 302 (code honored): %s", rec.Code, rec.Body.String())
	}
	// The exchange must re-issue the cookie with the winning (new) code.
	if sc := rec.Header().Get("Set-Cookie"); !contains(sc, newCode) {
		t.Errorf("exchange should store the new code; Set-Cookie = %q", sc)
	}

	// (b) An author's ?code=<write-token> must win over a stale reader cookie so
	// `octo new --open` reaches the draft even in a browser that read the doc first.
	readerCookie := capCookie("rot", newCode)
	rec = do(t, h, http.MethodGet, "/d/rot/draft?code=test-token", map[string]string{"Cookie": readerCookie}, "")
	if rec.Code != http.StatusFound {
		t.Fatalf("author code with reader cookie = %d; want 302 (author honored): %s", rec.Code, rec.Body.String())
	}
}

// TestRenderCapMarkerReflectsViewer asserts the render injects window.__ODOC_CAP__
// with isAuthor true only for the write-token holder, so the overlay hides the
// author-only Share (mint-code) button from a reader.
func TestRenderCapMarkerReflectsViewer(t *testing.T) {
	h := newTestServer(t, nil)
	auth := map[string]string{"Authorization": "Bearer test-token", "Content-Type": "application/json"}
	_ = do(t, h, http.MethodPost, "/v1/docs", auth,
		`{"slug":"cap","html":"<html><body><p>hi</p></body></html>"}`)
	sh := do(t, h, http.MethodPost, "/v1/docs/cap/share", map[string]string{"Authorization": "Bearer test-token"}, "")
	var share map[string]any
	_ = json.Unmarshal(sh.Body.Bytes(), &share)
	code, _ := share["data"].(map[string]any)["code"].(string)

	// Author (write token as Bearer) → isAuthor: true.
	rec := do(t, h, http.MethodGet, "/d/cap/v/1", map[string]string{"Authorization": "Bearer test-token"}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("author render = %d", rec.Code)
	}
	if !contains(rec.Body.String(), `window.__ODOC_CAP__ = {isAuthor: true}`) {
		t.Error("author render should carry isAuthor: true")
	}

	// Reader (share code cookie) → isAuthor: false.
	rec = do(t, h, http.MethodGet, "/d/cap/v/1", map[string]string{"Cookie": capCookie("cap", code)}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("reader render = %d: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `window.__ODOC_CAP__ = {isAuthor: false}`) {
		t.Error("reader render should carry isAuthor: false (Share button hidden)")
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
