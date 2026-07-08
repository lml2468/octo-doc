package httpx_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lml2468/octo-doc/internal/config"
	"github.com/lml2468/octo-doc/internal/platform/log"
	"github.com/lml2468/octo-doc/internal/platform/sluglock"
	"github.com/lml2468/octo-doc/internal/service"
	"github.com/lml2468/octo-doc/internal/storage/memory"
	"github.com/lml2468/octo-doc/internal/transport/httpx"
)

// newTestServer builds a full server backed by the in-memory store.
func newTestServer(t *testing.T, cfg *config.Config) http.Handler {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{
			WriteToken: "test-token", MaxHTMLBytes: 5 << 20, RepoURL: "https://example.com/repo",
			RateLimitMax:   0, // disable rate limiting in tests
			MaxAssetBytes:  25 << 20,
			AssetMIMEAllow: []string{"image/png", "image/gif", "image/jpeg"},
		}
	}
	store := memory.New()
	locker := sluglock.NewMemory()
	comments := service.NewCommentService(store, locker)
	docs := service.NewDocService(store, store, comments, locker, cfg.BaseURL, cfg.MaxHTMLBytes)
	assets := service.NewAssetService(store, store, locker, cfg.MaxAssetBytes, cfg.AssetMIMEAllow)
	auth := service.NewAuthService(store, cfg, locker)
	srv := httpx.New(httpx.Deps{
		Config: cfg, Logger: log.New("silent"), Docs: docs, Comments: comments, Assets: assets, Auth: auth,
		OverlayJS: "/* overlay */",
	})
	return srv.Handler()
}

func do(t *testing.T, h http.Handler, method, target string, headers map[string]string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, target, r)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPingIdentity(t *testing.T) {
	h := newTestServer(t, nil)
	rec := do(t, h, http.MethodGet, "/v1/ping", nil, "")
	if rec.Code != 200 {
		t.Fatalf("ping status = %d", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	data, _ := body["data"].(map[string]any)
	if data == nil || data["service"] != "octo-doc" {
		t.Fatalf("ping data = %v; want data.service=octo-doc", body)
	}
}

func TestPublishRequiresAuth(t *testing.T) {
	h := newTestServer(t, nil)
	rec := do(t, h, http.MethodPost, "/v1/docs", map[string]string{"Content-Type": "application/json"},
		`{"slug":"x","html":"<html></html>"}`)
	if rec.Code != 401 {
		t.Fatalf("unauthenticated publish = %d; want 401", rec.Code)
	}
}

func TestPublishTitleFromMeta(t *testing.T) {
	// The CLI sends the doc's meta.json under `meta` ({slug,version,html,meta,
	// comments}); the server must read meta.title when no top-level title is given.
	h := newTestServer(t, nil)
	auth := map[string]string{"Authorization": "Bearer test-token", "Content-Type": "application/json"}
	rec := do(t, h, http.MethodPost, "/v1/docs", auth,
		`{"slug":"titled","version":1,"html":"<html><body><h1>x</h1></body></html>","meta":{"title":"From Meta","slug":"titled"}}`)
	if rec.Code != 200 {
		t.Fatalf("publish = %d: %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, http.MethodGet, "/v1/docs/titled/versions", map[string]string{"Authorization": "Bearer test-token"}, "")
	if !strings.Contains(rec.Body.String(), `"title":"From Meta"`) {
		t.Fatalf("title from meta not applied: %s", rec.Body.String())
	}
}

func TestRenderAlwaysPublishedMode(t *testing.T) {
	// A doc served by this server is published — the overlay must run in
	// "published" mode (Share/Fork), never "local" (which would show a dead
	// Publish button). Commenting is anonymous, so authConfigured is false.
	h := newTestServer(t, nil)
	auth := map[string]string{"Authorization": "Bearer test-token", "Content-Type": "application/json"}
	_ = do(t, h, http.MethodPost, "/v1/docs", auth,
		`{"slug":"m","version":1,"html":"<html><body><h1>x</h1></body></html>","meta":{"title":"M"}}`)
	body := do(t, h, http.MethodGet, "/d/m/v/1", map[string]string{"Authorization": "Bearer test-token"}, "").Body.String()
	if !strings.Contains(body, `"mode":"published"`) {
		t.Errorf("expected published mode in: %s", body[strings.Index(body, "__ODOC__"):min(strings.Index(body, "__ODOC__")+120, len(body))])
	}
	if !strings.Contains(body, `"authConfigured":false`) {
		t.Error("expected authConfigured=false (anonymous commenting)")
	}
}

func TestCommentRequiresCapability(t *testing.T) {
	// Default-private: a comment with no credential is rejected (404, existence
	// hidden). A share code (or the write token) is required to comment.
	h := newTestServer(t, nil)
	auth := map[string]string{"Authorization": "Bearer test-token", "Content-Type": "application/json"}
	_ = do(t, h, http.MethodPost, "/v1/docs", auth,
		`{"slug":"anon","version":1,"html":"<html><body><p>hello world</p></body></html>"}`)

	// No credential → rejected.
	rec := do(t, h, http.MethodPost, "/v1/comments", map[string]string{"Content-Type": "application/json"},
		`{"slug":"anon","text":"nice","version":1,"anchor":{"kind":"text","text":"hello"}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("anonymous comment = %d; want 404 (needs a capability)", rec.Code)
	}

	// The author (write token) can comment.
	rec = do(t, h, http.MethodPost, "/v1/comments", auth,
		`{"slug":"anon","text":"nice","version":1,"anchor":{"kind":"text","text":"hello"}}`)
	if rec.Code != 200 {
		t.Fatalf("author comment = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPublishRenderLifecycle(t *testing.T) {
	h := newTestServer(t, nil)
	auth := map[string]string{"Authorization": "Bearer test-token", "Content-Type": "application/json"}

	// Publish v1.
	rec := do(t, h, http.MethodPost, "/v1/docs", auth,
		`{"slug":"hello","html":"<html><body><h1>Hi</h1><img src=\"a.png\"></body></html>","title":"Hello"}`)
	if rec.Code != 200 {
		t.Fatalf("publish = %d: %s", rec.Code, rec.Body.String())
	}
	var pub map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &pub)
	pubData, _ := pub["data"].(map[string]any)
	if pubData == nil || pubData["version"].(float64) != 1 {
		t.Fatalf("publish body = %v", pub)
	}

	// Render injects overlay + stamps aids (author reads it).
	rec = do(t, h, http.MethodGet, "/d/hello/v/1", map[string]string{"Authorization": "Bearer test-token"}, "")
	if rec.Code != 200 {
		t.Fatalf("render = %d", rec.Code)
	}
	html := rec.Body.String()
	if !strings.Contains(html, "window.__ODOC__") {
		t.Error("overlay config not injected")
	}
	if !strings.Contains(html, "data-odoc-aid=") {
		t.Error("aids not stamped")
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors") {
		t.Error("security headers missing")
	}
	// Rich inline media (video/audio, iframe embeds, self-hosted objects) must be
	// governed by explicit CSP directives, not left to default-src fallback.
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"media-src ", "frame-src ", "object-src "} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q directive: %s", want, csp)
		}
	}

	// Publish v2 auto-increments.
	rec = do(t, h, http.MethodPost, "/v1/docs", auth,
		`{"slug":"hello","html":"<html><body><h1>Hi v2</h1></body></html>"}`)
	_ = json.Unmarshal(rec.Body.Bytes(), &pub)
	pubData, _ = pub["data"].(map[string]any)
	if pubData == nil || pubData["version"].(float64) != 2 {
		t.Fatalf("v2 version = %v", pub)
	}

	// Versions endpoint lists both (author reads).
	rec = do(t, h, http.MethodGet, "/v1/docs/hello/versions", map[string]string{"Authorization": "Bearer test-token"}, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"n":2`) {
		t.Fatalf("versions = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDraftLifecycle(t *testing.T) {
	h := newTestServer(t, nil)
	auth := map[string]string{"Authorization": "Bearer test-token", "Content-Type": "application/json"}

	// Draft save is author-only; no credential → 404 (existence hidden).
	rec := do(t, h, http.MethodPut, "/v1/docs/dr/draft",
		map[string]string{"Content-Type": "application/json"},
		`{"html":"<html><body><h1>draft</h1></body></html>"}`)
	if rec.Code != 401 && rec.Code != 404 {
		t.Fatalf("unauthenticated draft save = %d; want 401/404", rec.Code)
	}

	// Save a draft (overwrite twice to prove it's mutable).
	for _, body := range []string{
		`{"html":"<html><body><h1>draft one</h1></body></html>","title":"Draft Doc"}`,
		`{"html":"<html><body><h1>draft two</h1></body></html>","title":"Draft Doc"}`,
	} {
		rec = do(t, h, http.MethodPut, "/v1/docs/dr/draft", auth, body)
		if rec.Code != 200 {
			t.Fatalf("draft save = %d: %s", rec.Code, rec.Body.String())
		}
	}

	// The draft is NOT a version — versions endpoint has none yet (author reads).
	rec = do(t, h, http.MethodGet, "/v1/docs/dr/versions", map[string]string{"Authorization": "Bearer test-token"}, "")
	if strings.Contains(rec.Body.String(), `"n":1`) {
		t.Fatalf("draft leaked into versions: %s", rec.Body.String())
	}

	// Draft render is author-only. No credential → 404 (existence hidden).
	rec = do(t, h, http.MethodGet, "/d/dr/draft", nil, "")
	if rec.Code != 401 && rec.Code != 404 {
		t.Fatalf("unauthenticated draft render = %d; want 401/404", rec.Code)
	}
	rec = do(t, h, http.MethodGet, "/d/dr/draft", map[string]string{"Authorization": "Bearer test-token"}, "")
	if rec.Code != 200 {
		t.Fatalf("draft render = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"mode":"draft"`) {
		t.Error("draft not rendered in draft mode")
	}
	if !strings.Contains(rec.Body.String(), "draft two") {
		t.Error("draft render shows stale content")
	}

	// Promote → the draft becomes immutable v1.
	rec = do(t, h, http.MethodPost, "/v1/docs/dr/draft/promote", auth, "")
	if rec.Code != 200 {
		t.Fatalf("promote = %d: %s", rec.Code, rec.Body.String())
	}
	var pub map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &pub)
	if d, _ := pub["data"].(map[string]any); d == nil || d["version"].(float64) != 1 {
		t.Fatalf("promote body = %v; want version 1", pub)
	}

	// v1 is now committed; the author reads it, and the draft slot is cleared.
	if rec = do(t, h, http.MethodGet, "/d/dr/v/1", map[string]string{"Authorization": "Bearer test-token"}, ""); rec.Code != 200 {
		t.Fatalf("published v1 render = %d", rec.Code)
	}
	rec = do(t, h, http.MethodGet, "/d/dr/draft", map[string]string{"Authorization": "Bearer test-token"}, "")
	if rec.Code != 404 {
		t.Fatalf("draft after promote = %d; want 404 (cleared)", rec.Code)
	}

	// Promoting again with no draft is a clean 404, not a 500.
	rec = do(t, h, http.MethodPost, "/v1/docs/dr/draft/promote", auth, "")
	if rec.Code != 404 {
		t.Fatalf("promote with no draft = %d; want 404", rec.Code)
	}
}

func TestCommentLifecycle(t *testing.T) {
	h := newTestServer(t, nil)
	auth := map[string]string{"Authorization": "Bearer test-token", "Content-Type": "application/json"}
	_ = do(t, h, http.MethodPost, "/v1/docs", auth,
		`{"slug":"doc","html":"<html><body><p>hello world</p></body></html>"}`)

	// Create a comment (author credential).
	rec := do(t, h, http.MethodPost, "/v1/comments", auth,
		`{"slug":"doc","text":"nice","version":1,"anchor":{"kind":"text","text":"hello"}}`)
	if rec.Code != 200 {
		t.Fatalf("create comment = %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	createdData, _ := created["data"].(map[string]any)
	id, _ := createdData["id"].(string)
	if id == "" {
		t.Fatalf("no comment id in %v", created)
	}

	// List shows it, wrapped in the data/pagination envelope.
	rec = do(t, h, http.MethodGet, "/v1/comments?slug=doc&version=1", map[string]string{"Authorization": "Bearer test-token"}, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "nice") {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"pagination"`) || !strings.Contains(rec.Body.String(), `"created_at"`) {
		t.Fatalf("list envelope missing pagination/created_at: %s", rec.Body.String())
	}

	// React.
	rec = do(t, h, http.MethodPost, "/v1/reactions", auth,
		`{"slug":"doc","comment_id":"`+id+`","emoji":"👍","version":1}`)
	if rec.Code != 200 {
		t.Fatalf("react = %d: %s", rec.Code, rec.Body.String())
	}

	// Agent reply (write-token gated) flips status.
	rec = do(t, h, http.MethodPost, "/v1/agent/replies", auth,
		`{"slug":"doc","parent_id":"`+id+`","text":"done","status":"applied","applied_in":1}`)
	if rec.Code != 200 {
		t.Fatalf("agent reply = %d: %s", rec.Code, rec.Body.String())
	}

	// Delete.
	rec = do(t, h, http.MethodDelete, "/v1/comments?slug=doc&id="+id+"&version=1", map[string]string{"Authorization": "Bearer test-token"}, "")
	if rec.Code != 200 {
		t.Fatalf("delete = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestForkExport(t *testing.T) {
	h := newTestServer(t, nil)
	auth := map[string]string{"Authorization": "Bearer test-token", "Content-Type": "application/json"}
	_ = do(t, h, http.MethodPost, "/v1/docs", auth,
		`{"slug":"f","html":"<html><body><p>content here</p></body></html>"}`)
	_ = do(t, h, http.MethodPost, "/v1/comments", auth,
		`{"slug":"f","text":"note","version":1,"anchor":{"kind":"text","text":"content"}}`)

	rd := map[string]string{"Authorization": "Bearer test-token"}
	rec := do(t, h, http.MethodGet, "/d/f/v/1/export", rd, "")
	if rec.Code != 200 {
		t.Fatalf("export = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "octo-doc fork export") {
		t.Error("export banner missing")
	}
	if !strings.Contains(rec.Body.String(), "odoc-fork-comments") {
		t.Error("fork comments JSON missing")
	}

	rec = do(t, h, http.MethodGet, "/d/f/v/1/fork", rd, "")
	if !strings.Contains(rec.Body.String(), "window.__ODOC__") {
		t.Error("fork should boot overlay")
	}
}

func TestBootstrapOnce(t *testing.T) {
	cfg := &config.Config{AllowBootstrap: true, MaxHTMLBytes: 1 << 20, RepoURL: "https://x", RateLimitMax: 0}
	h := newTestServer(t, cfg)
	rec := do(t, h, http.MethodPost, "/v1/admin/bootstrap", nil, "")
	if rec.Code != 200 {
		t.Fatalf("bootstrap = %d: %s", rec.Code, rec.Body.String())
	}
	// Second call conflicts.
	rec = do(t, h, http.MethodPost, "/v1/admin/bootstrap", nil, "")
	if rec.Code != 409 {
		t.Fatalf("second bootstrap = %d; want 409", rec.Code)
	}
}

func TestInvalidSlugRejected(t *testing.T) {
	h := newTestServer(t, nil)
	rec := do(t, h, http.MethodGet, "/v1/comments?slug=../etc", nil, "")
	if rec.Code != 400 {
		t.Fatalf("bad slug = %d; want 400", rec.Code)
	}
}
