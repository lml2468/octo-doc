package httpx_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-doc/internal/config"
	"github.com/Mininglamp-OSS/octo-doc/internal/platform/log"
	"github.com/Mininglamp-OSS/octo-doc/internal/platform/sluglock"
	"github.com/Mininglamp-OSS/octo-doc/internal/service"
	"github.com/Mininglamp-OSS/octo-doc/internal/storage/memory"
	"github.com/Mininglamp-OSS/octo-doc/internal/transport/httpx"
)

// newTestServer builds a full server backed by the in-memory store.
func newTestServer(t *testing.T, cfg *config.Config) http.Handler {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{
			WriteToken: "test-token", MaxHTMLBytes: 5 << 20, RepoURL: "https://example.com/repo",
			RateLimitMax: 0, // disable rate limiting in tests
		}
	}
	store := memory.New()
	locker := sluglock.NewMemory()
	comments := service.NewCommentService(store, locker)
	docs := service.NewDocService(store, store, comments, cfg.BaseURL, cfg.MaxHTMLBytes)
	auth := service.NewAuthService(store, cfg)
	srv := httpx.New(httpx.Deps{
		Config: cfg, Logger: log.New("silent"), Docs: docs, Comments: comments, Auth: auth,
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
	rec := do(t, h, http.MethodGet, "/api/ping", nil, "")
	if rec.Code != 200 {
		t.Fatalf("ping status = %d", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["service"] != "tdoc" {
		t.Fatalf("ping service = %v; want tdoc", body["service"])
	}
}

func TestPublishRequiresAuth(t *testing.T) {
	h := newTestServer(t, nil)
	rec := do(t, h, http.MethodPost, "/api/docs", map[string]string{"Content-Type": "application/json"},
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
	rec := do(t, h, http.MethodPost, "/api/docs", auth,
		`{"slug":"titled","version":1,"html":"<html><body><h1>x</h1></body></html>","meta":{"title":"From Meta","slug":"titled"}}`)
	if rec.Code != 200 {
		t.Fatalf("publish = %d: %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, http.MethodGet, "/api/docs/titled/versions", nil, "")
	if !strings.Contains(rec.Body.String(), `"title":"From Meta"`) {
		t.Fatalf("title from meta not applied: %s", rec.Body.String())
	}
}

func TestPublishRenderLifecycle(t *testing.T) {
	h := newTestServer(t, nil)
	auth := map[string]string{"Authorization": "Bearer test-token", "Content-Type": "application/json"}

	// Publish v1.
	rec := do(t, h, http.MethodPost, "/api/docs", auth,
		`{"slug":"hello","html":"<html><body><h1>Hi</h1><img src=\"a.png\"></body></html>","title":"Hello"}`)
	if rec.Code != 200 {
		t.Fatalf("publish = %d: %s", rec.Code, rec.Body.String())
	}
	var pub map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &pub)
	if pub["ok"] != true || pub["version"].(float64) != 1 {
		t.Fatalf("publish body = %v", pub)
	}

	// Render injects overlay + stamps aids.
	rec = do(t, h, http.MethodGet, "/d/hello/v/1", nil, "")
	if rec.Code != 200 {
		t.Fatalf("render = %d", rec.Code)
	}
	html := rec.Body.String()
	if !strings.Contains(html, "window.__TDOC__") {
		t.Error("overlay config not injected")
	}
	if !strings.Contains(html, "data-tdoc-aid=") {
		t.Error("aids not stamped")
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors") {
		t.Error("security headers missing")
	}

	// Publish v2 auto-increments.
	rec = do(t, h, http.MethodPost, "/api/docs", auth,
		`{"slug":"hello","html":"<html><body><h1>Hi v2</h1></body></html>"}`)
	_ = json.Unmarshal(rec.Body.Bytes(), &pub)
	if pub["version"].(float64) != 2 {
		t.Fatalf("v2 version = %v", pub["version"])
	}

	// Versions endpoint lists both.
	rec = do(t, h, http.MethodGet, "/api/docs/hello/versions", nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"n":2`) {
		t.Fatalf("versions = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCommentLifecycle(t *testing.T) {
	h := newTestServer(t, nil)
	auth := map[string]string{"Authorization": "Bearer test-token", "Content-Type": "application/json"}
	_ = do(t, h, http.MethodPost, "/api/docs", auth,
		`{"slug":"doc","html":"<html><body><p>hello world</p></body></html>"}`)

	// Create a comment (anonymous/local mode).
	rec := do(t, h, http.MethodPost, "/api/comments", map[string]string{"Content-Type": "application/json"},
		`{"slug":"doc","text":"nice","version":1,"anchor":{"kind":"text","text":"hello"}}`)
	if rec.Code != 200 {
		t.Fatalf("create comment = %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no comment id in %v", created)
	}

	// List shows it.
	rec = do(t, h, http.MethodGet, "/api/comments?slug=doc&version=1", nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "nice") {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}

	// React.
	rec = do(t, h, http.MethodPost, "/api/reactions", map[string]string{"Content-Type": "application/json"},
		`{"slug":"doc","comment_id":"`+id+`","emoji":"👍","version":1}`)
	if rec.Code != 200 {
		t.Fatalf("react = %d: %s", rec.Code, rec.Body.String())
	}

	// Agent reply (write-token gated) flips status.
	rec = do(t, h, http.MethodPost, "/api/agent/reply", auth,
		`{"slug":"doc","parent_id":"`+id+`","text":"done","status":"applied","applied_in":1}`)
	if rec.Code != 200 {
		t.Fatalf("agent reply = %d: %s", rec.Code, rec.Body.String())
	}

	// Delete.
	rec = do(t, h, http.MethodDelete, "/api/comments?slug=doc&id="+id+"&version=1", nil, "")
	if rec.Code != 200 {
		t.Fatalf("delete = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestForkExport(t *testing.T) {
	h := newTestServer(t, nil)
	auth := map[string]string{"Authorization": "Bearer test-token", "Content-Type": "application/json"}
	_ = do(t, h, http.MethodPost, "/api/docs", auth,
		`{"slug":"f","html":"<html><body><p>content here</p></body></html>"}`)
	_ = do(t, h, http.MethodPost, "/api/comments", map[string]string{"Content-Type": "application/json"},
		`{"slug":"f","text":"note","version":1,"anchor":{"kind":"text","text":"content"}}`)

	rec := do(t, h, http.MethodGet, "/d/f/v/1/export", nil, "")
	if rec.Code != 200 {
		t.Fatalf("export = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "octo-doc fork export") {
		t.Error("export banner missing")
	}
	if !strings.Contains(rec.Body.String(), "tdoc-fork-comments") {
		t.Error("fork comments JSON missing")
	}

	rec = do(t, h, http.MethodGet, "/d/f/v/1/fork", nil, "")
	if !strings.Contains(rec.Body.String(), "window.__TDOC__") {
		t.Error("fork should boot overlay")
	}
}

func TestBootstrapOnce(t *testing.T) {
	cfg := &config.Config{AllowBootstrap: true, MaxHTMLBytes: 1 << 20, RepoURL: "https://x", RateLimitMax: 0}
	h := newTestServer(t, cfg)
	rec := do(t, h, http.MethodGet, "/api/admin/bootstrap", nil, "")
	if rec.Code != 200 {
		t.Fatalf("bootstrap = %d: %s", rec.Code, rec.Body.String())
	}
	// Second call conflicts.
	rec = do(t, h, http.MethodGet, "/api/admin/bootstrap", nil, "")
	if rec.Code != 409 {
		t.Fatalf("second bootstrap = %d; want 409", rec.Code)
	}
}

func TestInvalidSlugRejected(t *testing.T) {
	h := newTestServer(t, nil)
	rec := do(t, h, http.MethodGet, "/api/comments?slug=../etc", nil, "")
	if rec.Code != 400 {
		t.Fatalf("bad slug = %d; want 400", rec.Code)
	}
}
