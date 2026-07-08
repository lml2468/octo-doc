package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeUploader returns a deterministic URL per filename and records calls.
func fakeUploader(t *testing.T) (assetUploader, *[]string) {
	t.Helper()
	var calls []string
	up := func(filename string, data []byte) (string, error) {
		calls = append(calls, filename)
		return "https://host/d/s/assets/" + filename + "-sha", nil
	}
	return up, &calls
}

func TestIsLocalRef(t *testing.T) {
	cases := map[string]bool{
		"./img/cat.png":             true,
		"img/cat.png":               true,
		"../shared/logo.svg":        true, // local per se; traversal caught later
		"video.mp4?v=2":             true,
		"https://cdn.example/a.png": false,
		"http://x/a.png":            false,
		"//cdn.example/a.png":       false,
		"/abs/a.png":                false,
		"data:image/png;base64,AA":  false,
		"blob:xyz":                  false,
		"#anchor":                   false,
		"":                          false,
		"mailto:x@y.z":              false,
	}
	for ref, want := range cases {
		if got := isLocalRef(ref); got != want {
			t.Errorf("isLocalRef(%q) = %v; want %v", ref, got, want)
		}
	}
}

func TestRewriteAssetsBasic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cat.png"), []byte("\x89PNG..."), 0o644); err != nil {
		t.Fatal(err)
	}
	up, calls := fakeUploader(t)
	html := `<body><img src="./cat.png" alt="c"><img src="https://cdn/x.png"></body>`
	out, uploads, err := rewriteAssets(html, dir, false, up)
	if err != nil {
		t.Fatal(err)
	}
	if len(uploads) != 1 || len(*calls) != 1 {
		t.Fatalf("uploads=%d calls=%d; want 1,1", len(uploads), len(*calls))
	}
	if !strings.Contains(out, `src="https://host/d/s/assets/cat.png-sha"`) {
		t.Errorf("local ref not rewritten: %s", out)
	}
	if !strings.Contains(out, `src="https://cdn/x.png"`) {
		t.Errorf("remote ref should be untouched: %s", out)
	}
}

func TestRewriteAssetsDedup(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.png"), []byte("A"), 0o644)
	up, calls := fakeUploader(t)
	// Same file referenced twice (once with ./ prefix) → one upload.
	html := `<img src="a.png"><img src="./a.png">`
	out, uploads, err := rewriteAssets(html, dir, false, up)
	if err != nil {
		t.Fatal(err)
	}
	if len(uploads) != 1 || len(*calls) != 1 {
		t.Fatalf("dedup failed: uploads=%d calls=%d; want 1,1", len(uploads), len(*calls))
	}
	if strings.Count(out, "assets/a.png-sha") != 2 {
		t.Errorf("both refs should point at the same asset URL: %s", out)
	}
}

func TestRewriteAssetsDryRun(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.png"), []byte("A"), 0o644)
	up, calls := fakeUploader(t)
	html := `<img src="a.png">`
	out, uploads, err := rewriteAssets(html, dir, true, up)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Errorf("dry-run must not upload; got %d calls", len(*calls))
	}
	if len(uploads) != 1 {
		t.Errorf("dry-run should still report 1 upload; got %d", len(uploads))
	}
	if out != html {
		t.Errorf("dry-run must not modify HTML:\n got: %s\nwant: %s", out, html)
	}
}

func TestRewriteAssetsTraversalGuard(t *testing.T) {
	dir := t.TempDir()
	up, _ := fakeUploader(t)
	// A reference escaping the base dir must error, not read outside the tree.
	html := `<img src="../../../etc/passwd">`
	_, _, err := rewriteAssets(html, dir, false, up)
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("want traversal error, got %v", err)
	}
}

func TestRewriteAssetsMissingFile(t *testing.T) {
	dir := t.TempDir()
	up, _ := fakeUploader(t)
	html := `<img src="nope.png">`
	_, _, err := rewriteAssets(html, dir, false, up)
	if err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("want read error for missing file, got %v", err)
	}
}

func TestRewriteAssetsSrcset(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "s1.png"), []byte("1"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "s2.png"), []byte("2"), 0o644)
	up, calls := fakeUploader(t)
	html := `<img srcset="s1.png 1x, s2.png 2x">`
	out, _, err := rewriteAssets(html, dir, false, up)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 {
		t.Fatalf("srcset should upload 2 files; got %d", len(*calls))
	}
	if !strings.Contains(out, "assets/s1.png-sha 1x") || !strings.Contains(out, "assets/s2.png-sha 2x") {
		t.Errorf("srcset descriptors not preserved: %s", out)
	}
}

func TestRewriteAssetsCSSAndPoster(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "bg.jpg"), []byte("B"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "p.jpg"), []byte("P"), 0o644)
	up, calls := fakeUploader(t)
	html := `<style>.h{background:url('bg.jpg')}</style><video poster="p.jpg"></video>`
	out, _, err := rewriteAssets(html, dir, false, up)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 {
		t.Fatalf("want 2 uploads (css + poster); got %d", len(*calls))
	}
	if !strings.Contains(out, "url('https://host/d/s/assets/bg.jpg-sha')") {
		t.Errorf("css url not rewritten: %s", out)
	}
	if !strings.Contains(out, `poster="https://host/d/s/assets/p.jpg-sha"`) {
		t.Errorf("poster not rewritten: %s", out)
	}
}

func TestRewriteAssetsObjectData(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "report.pdf"), []byte("%PDF-1.7"), 0o644)
	up, calls := fakeUploader(t)
	// <object data> is rewritten; unrelated attributes carrying a path are not.
	html := `<object data="report.pdf"></object><a href="report.pdf">dl</a>`
	out, _, err := rewriteAssets(html, dir, false, up)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("only <object data> should upload; got %d calls", len(*calls))
	}
	if !strings.Contains(out, `data="https://host/d/s/assets/report.pdf-sha"`) {
		t.Errorf("object data not rewritten: %s", out)
	}
	if !strings.Contains(out, `href="report.pdf"`) {
		t.Errorf("href must be untouched (not a media attr): %s", out)
	}
}

func TestRewriteFlagParsing(t *testing.T) {
	cases := []struct {
		in      string
		wantSet bool
		wantDry bool
		wantErr bool
	}{
		{"", true, false, false}, // bare flag
		{"on", true, false, false},
		{"dry", true, true, false},
		{"dry-run", true, true, false},
		{"off", false, false, false},
		{"bogus", false, false, true},
	}
	for _, c := range cases {
		var f rewriteFlag
		err := f.Set(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("Set(%q) err=%v; wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if f.set != c.wantSet || f.dry != c.wantDry {
			t.Errorf("Set(%q) = {set:%v dry:%v}; want {set:%v dry:%v}", c.in, f.set, f.dry, c.wantSet, c.wantDry)
		}
	}
}
