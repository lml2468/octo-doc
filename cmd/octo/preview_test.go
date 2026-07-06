package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-doc/assets"
	"github.com/Mininglamp-OSS/octo-doc/internal/core"
)

// newTestServer builds a preview server over a temp doc store.
func newTestServer(t *testing.T) *previewServer {
	t.Helper()
	return &previewServer{store: newStore(t.TempDir()), port: defaultPort}
}

// seedDoc writes a minimal doc (meta + one HTML version) into the store.
func seedDoc(t *testing.T, st *store, htmlBody string) {
	t.Helper()
	const slug = "doc"
	if err := os.MkdirAll(filepath.Dir(st.htmlPath(slug, 1)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.htmlPath(slug, 1), []byte(htmlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := &docMeta{Title: slug, Slug: slug, Created: "2026-01-01T00:00:00.000Z",
		Versions: []versionRef{{N: 1, Created: "2026-01-01T00:00:00.000Z"}}}
	if err := st.writeMeta(slug, meta); err != nil {
		t.Fatal(err)
	}
}

// postJSON sends a JSON POST (satisfying the CSRF content-type guard) to the
// preview handler and returns the recorder. Shared by the mutation tests so the
// Content-Type header can't drift between them.
func postJSON(h http.Handler, path, body string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	return rr
}

// TestRenderInjectsCanonicalOverlay proves the preview render uses the exact same
// core.InjectOverlayCfg + assets.OverlayJS the server uses — the whole reason the
// mirror can be retired. We reproduce the server's call for the same doc in
// "local" mode and require byte-equality.
func TestRenderInjectsCanonicalOverlay(t *testing.T) {
	ps := newTestServer(t)
	body := "<html><body><h1>Hi</h1></body></html>"
	seedDoc(t, ps.store, body)

	got, err := ps.renderDoc("doc", 1)
	if err != nil {
		t.Fatal(err)
	}
	created := "2026-01-01T00:00:00.000Z"
	want, err := core.InjectOverlayCfg(body, assets.OverlayJS, core.OverlayConfig{
		Slug: "doc", Version: 1, Identity: nil, Mode: "local", AuthConfigured: false,
		Versions: []core.VersionRef{{N: 1, Created: &created}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("preview render is not byte-identical to core.InjectOverlayCfg output")
	}
	if !strings.Contains(got, assets.OverlayJS) {
		t.Error("rendered doc does not embed the canonical overlay bytes")
	}
	if !strings.Contains(got, `"mode":"local"`) {
		t.Error("rendered doc missing local mode marker")
	}
}

func TestPingMarker(t *testing.T) {
	ps := newTestServer(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/ping", nil)
	ps.routes().ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), `"service":"octo"`) {
		t.Errorf("ping marker missing: %s", rr.Body.String())
	}
}

func TestCommentCRUDFlow(t *testing.T) {
	ps := newTestServer(t)
	seedDoc(t, ps.store, "<html><body>x</body></html>")
	h := ps.routes()

	// Create requires the JSON content-type CSRF guard.
	rr := postJSON(h, "/v1/comments", `{"slug":"doc","version":1,"text":"hello"}`)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"created_at"`) {
		t.Fatalf("create failed: %d %s", rr.Code, rr.Body.String())
	}
	// List returns it in wire shape.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/comments?slug=doc", nil))
	if !strings.Contains(rr.Body.String(), `"text":"hello"`) {
		t.Errorf("list missing comment: %s", rr.Body.String())
	}
}

func TestCSRFGuardRejectsNonJSON(t *testing.T) {
	ps := newTestServer(t)
	seedDoc(t, ps.store, "<html><body>x</body></html>")
	rr := httptest.NewRecorder()
	// No application/json content-type → must be rejected as a CSRF-simple POST.
	req := httptest.NewRequest(http.MethodPost, "/v1/comments", strings.NewReader(`{"slug":"doc","text":"x"}`))
	req.Header.Set("Content-Type", "text/plain")
	ps.routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("non-JSON POST should be 403, got %d", rr.Code)
	}
}

func TestCSRFGuardRejectsForeignOrigin(t *testing.T) {
	ps := newTestServer(t)
	seedDoc(t, ps.store, "<html><body>x</body></html>")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/comments", strings.NewReader(`{"slug":"doc","text":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example.com")
	ps.routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("foreign-origin POST should be 403, got %d", rr.Code)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	ps := newTestServer(t)
	rr := httptest.NewRecorder()
	// A traversal slug must not escape the store; parseDocPath+safeSlug reject it.
	req := httptest.NewRequest(http.MethodGet, "/d/..%2f..%2fetc/v/1", nil)
	ps.routes().ServeHTTP(rr, req)
	if rr.Code == 200 {
		t.Error("path traversal should not render")
	}
}

func TestAgentReplySetsStatusEmoji(t *testing.T) {
	ps := newTestServer(t)
	seedDoc(t, ps.store, "<html><body>x</body></html>")
	h := ps.routes()
	// Seed a comment.
	if rr := postJSON(h, "/v1/comments", `{"slug":"doc","version":1,"text":"q"}`); rr.Code != 200 {
		t.Fatalf("seed comment failed: %d", rr.Code)
	}
	list, _ := ps.store.readComments("doc")
	cid := list[0].ID

	// Agent applies it → parent status applied, ✅ reaction from tdoc-agent.
	rr := postJSON(h, "/v1/agent/replies",
		`{"slug":"doc","parent_id":"`+cid+`","text":"done","status":"applied"}`)
	if rr.Code != 200 {
		t.Fatalf("agent reply failed: %d %s", rr.Code, rr.Body.String())
	}
	list, _ = ps.store.readComments("doc")
	if list[0].Status != "applied" {
		t.Errorf("parent status = %q, want applied", list[0].Status)
	}
	if !slices.Contains(list[0].Reactions["✅"], agentLogin) {
		t.Errorf("missing ✅ agent reaction: %+v", list[0].Reactions)
	}
}

func TestSetAgentReactionReplacesStale(t *testing.T) {
	reactions := map[string][]string{}
	setAgentReaction(&reactions, "applied")
	setAgentReaction(&reactions, "question")
	if slices.Contains(reactions["✅"], agentLogin) {
		t.Error("stale ✅ should have been cleared")
	}
	if !slices.Contains(reactions["❓"], agentLogin) {
		t.Error("❓ should be set")
	}
}

// TestCommentIDsUniqueSameInstant guards the collision fix: two comments created
// back-to-back (same millisecond) must get distinct ids, or the second becomes
// unaddressable by findComment/delete/react.
func TestCommentIDsUniqueSameInstant(t *testing.T) {
	ps := newTestServer(t)
	seedDoc(t, ps.store, "<html><body>x</body></html>")
	h := ps.routes()
	seen := map[string]bool{}
	for range 50 {
		if rr := postJSON(h, "/v1/comments", `{"slug":"doc","version":1,"text":"x"}`); rr.Code != 200 {
			t.Fatalf("create failed: %d", rr.Code)
		}
	}
	list, _ := ps.store.readComments("doc")
	for _, c := range list {
		if seen[c.ID] {
			t.Fatalf("duplicate comment id: %s", c.ID)
		}
		seen[c.ID] = true
	}
	if len(seen) != 50 {
		t.Errorf("expected 50 unique ids, got %d", len(seen))
	}
}

// TestPublishRouteWired confirms /v1/publish reaches the publish handler (not a
// 404 fall-through), so the overlay's local-mode Publish button works. With no
// server configured it should return the 503 the handler emits — never 404.
func TestPublishRouteWired(t *testing.T) {
	ps := newTestServer(t)
	ps.cfg = config{Dir: ps.store.dir} // no BaseURL → publish must fail cleanly
	seedDoc(t, ps.store, "<html><body>x</body></html>")
	rr := postJSON(ps.routes(), "/v1/publish", `{"slug":"doc"}`)
	if rr.Code == http.StatusNotFound {
		t.Fatal("/v1/publish fell through to 404 — route not wired")
	}
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("no-server publish should be 503, got %d: %s", rr.Code, rr.Body.String())
	}
}
