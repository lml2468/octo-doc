package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigEnvPrecedence(t *testing.T) {
	t.Setenv("OCTO_BASE_URL", "https://octo.example.com/")
	t.Setenv("OCTO_TOKEN", "octo-tok")
	t.Setenv("OCTO_CODE", "sharecode")
	t.Setenv("OCTO_DIR", t.TempDir())
	cfg := loadConfig()
	if cfg.BaseURL != "https://octo.example.com" {
		t.Errorf("BaseURL not trimmed: %q", cfg.BaseURL)
	}
	if cfg.Token != "octo-tok" {
		t.Errorf("Token = %q", cfg.Token)
	}
	if cfg.Code != "sharecode" {
		t.Errorf("Code = %q", cfg.Code)
	}
}

func TestConfigNoLegacyFallback(t *testing.T) {
	// Legacy names are gone: with no OCTO_* set (and a legacy-style var present),
	// config resolves empty — OCTO_* is the only accepted surface.
	os.Unsetenv("OCTO_BASE_URL")
	os.Unsetenv("OCTO_TOKEN")
	t.Setenv("TDOC_BASE_URL", "https://legacy.example.com")
	t.Setenv("TDOC_TOKEN", "legacy-tok")
	t.Setenv("OCTO_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir()) // no ~/.octo/config.json to fall back to
	cfg := loadConfig()
	if cfg.BaseURL != "" {
		t.Errorf("BaseURL = %q; legacy env must not resolve", cfg.BaseURL)
	}
	if cfg.Token != "" {
		t.Errorf("Token = %q; legacy env must not resolve", cfg.Token)
	}
}

func TestSafeSlug(t *testing.T) {
	valid := []string{"hello", "a", "my-doc_1", "ABC-123"}
	for _, s := range valid {
		if safeSlug(s) != s {
			t.Errorf("safeSlug(%q) rejected a valid slug", s)
		}
	}
	invalid := []string{"", "../etc", "a/b", "a b", "a.b", "toolong" + string(make([]byte, 100))}
	for _, s := range invalid {
		if safeSlug(s) != "" {
			t.Errorf("safeSlug(%q) accepted an invalid slug", s)
		}
	}
}

func TestKebabValidation(t *testing.T) {
	good := []string{"a", "hello", "compound-interest", "a1-b2"}
	for _, s := range good {
		if !kebabRE.MatchString(s) {
			t.Errorf("kebab %q should be valid", s)
		}
	}
	bad := []string{"", "Hello", "-leading", "trailing-", "under_score", "sp ace"}
	for _, s := range bad {
		if kebabRE.MatchString(s) {
			t.Errorf("kebab %q should be invalid", s)
		}
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := newStore(dir)
	slug := "roundtrip"
	if err := os.MkdirAll(st.slugDir(slug), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := &docMeta{Title: "T", Slug: slug, Created: "2026-01-01T00:00:00Z", Versions: []versionRef{
		{N: 2, Created: "b"}, {N: 1, Created: "a"}, {N: 3, Created: "c"},
	}}
	if err := st.writeMeta(slug, meta); err != nil {
		t.Fatal(err)
	}
	got, err := st.readMeta(slug)
	if err != nil {
		t.Fatal(err)
	}
	if got.latestVersion() != 3 {
		t.Errorf("latestVersion = %d, want 3", got.latestVersion())
	}
	sorted := got.sortedVersions()
	if sorted[0].N != 1 || sorted[2].N != 3 {
		t.Errorf("sortedVersions not ascending: %+v", sorted)
	}
}

func TestReadCommentsCorruptIsEmpty(t *testing.T) {
	dir := t.TempDir()
	st := newStore(dir)
	slug := "corrupt"
	if err := os.MkdirAll(st.slugDir(slug), 0o755); err != nil {
		t.Fatal(err)
	}
	// A non-array comments.json (hand-edited to {}) must read as empty, not error.
	if err := os.WriteFile(st.commentsPath(slug), []byte(`{"oops":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := st.readComments(slug)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("corrupt file should read as empty, got %d", len(list))
	}
}

func TestWireDiskMapping(t *testing.T) {
	// created ↔ created_at must map both directions, including nested replies.
	c := comment{
		ID: "c1", Text: "hi", Created: "2026-01-01T00:00:00Z", Status: "open",
		Replies: []reply{{ID: "r1", ParentID: "c1", Text: "re", Created: "2026-01-02T00:00:00Z"}},
	}
	w := c.toWire()
	if w.CreatedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("comment created_at = %q", w.CreatedAt)
	}
	if w.Replies[0].CreatedAt != "2026-01-02T00:00:00Z" {
		t.Errorf("reply created_at = %q", w.Replies[0].CreatedAt)
	}
	back := w.toComment()
	if back.Created != c.Created || back.Replies[0].Created != c.Replies[0].Created {
		t.Errorf("round-trip lost created: %+v", back)
	}
}

func TestExistingCommentsRejectsNonArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "comments.json")
	// An error-object body must not be treated as a valid local array.
	os.WriteFile(path, []byte(`{"error":"x"}`), 0o644)
	if _, ok := existingComments(path); ok {
		t.Error("non-array should be rejected")
	}
	os.WriteFile(path, []byte(`[{"id":"c1","text":"t","created":"z"}]`), 0o644)
	list, ok := existingComments(path)
	if !ok || len(list) != 1 {
		t.Errorf("valid array rejected: ok=%v len=%d", ok, len(list))
	}
}
