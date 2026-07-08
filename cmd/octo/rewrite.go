package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Asset reference rewriting (P2.1). Before a draft is saved, scan its HTML for
// LOCAL media references — src=/poster= attributes, srcset= candidate lists, and
// CSS url(...) — upload each referenced file as a per-doc asset, and rewrite the
// reference to the minted asset URL. Remote (http/https/protocol-relative),
// data:/blob:, absolute (/…), and anchor (#…) references are left untouched.
//
// This is a pure CLI-side transform on the bytes BEFORE they reach the server:
// the server still receives final HTML and stamps it unchanged, so core byte
// parity is unaffected. See docs/ASSETS.md (P2.1).

// assetUploader uploads one file's bytes and returns its referenceable URL. It is
// injected so the rewrite logic is testable without a server.
type assetUploader func(filename string, data []byte) (url string, err error)

// rewriteFlag is the --rewrite-assets value. It works as a bare boolean flag
// (--rewrite-assets → on) and accepts --rewrite-assets=dry to only report.
type rewriteFlag struct {
	set bool
	dry bool
}

func (f *rewriteFlag) String() string {
	switch {
	case f == nil || !f.set:
		return "off"
	case f.dry:
		return "dry"
	default:
		return "on"
	}
}

// Set parses the optional value. "" (bare flag) and truthy values enable rewrite;
// "dry" enables report-only; falsey values disable.
func (f *rewriteFlag) Set(v string) error {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "true", "1", "on", "yes":
		f.set, f.dry = true, false
	case "dry", "dry-run":
		f.set, f.dry = true, true
	case "false", "0", "off", "no":
		f.set, f.dry = false, false
	default:
		return fmt.Errorf("invalid --rewrite-assets value %q (use: on, dry, off)", v)
	}
	return nil
}

// IsBoolFlag lets the flag be used without a value (--rewrite-assets).
func (f *rewriteFlag) IsBoolFlag() bool { return true }

// applyRewrite runs asset rewriting on htmlBytes if the flag is enabled, reporting
// to stderr. baseDir is where local references resolve from (the HTML file's
// directory, or cwd for --html-stdin). It returns the possibly-rewritten HTML.
func applyRewrite(f *rewriteFlag, htmlBytes []byte, baseDir string, cl *client, slug string, info func(string, ...any)) ([]byte, error) {
	if f == nil || !f.set {
		return htmlBytes, nil
	}
	up := func(filename string, data []byte) (string, error) {
		res, err := cl.uploadAsset(context.Background(), slug, filename, data)
		if err != nil {
			return "", err
		}
		return res.URL, nil
	}
	out, uploads, err := rewriteAssets(string(htmlBytes), baseDir, f.dry, up)
	if err != nil {
		return nil, err
	}
	if len(uploads) == 0 {
		info("rewrite-assets: no local references found")
		return htmlBytes, nil
	}
	for _, u := range uploads {
		if f.dry {
			info("rewrite-assets (dry): would upload %s (%d bytes) ← %s", u.Path, u.Size, u.Ref)
		} else {
			info("rewrite-assets: %s → %s", u.Ref, u.URL)
		}
	}
	if f.dry {
		info("rewrite-assets: dry run — %d file(s) would be uploaded; HTML left unchanged", len(uploads))
		return htmlBytes, nil
	}
	info("rewrite-assets: uploaded %d file(s) and rewrote references", len(uploads))
	return []byte(out), nil
}

// rewriteUpload records one referenced local file and where it went.
type rewriteUpload struct {
	Ref  string // the reference as written in the HTML (e.g. "./img/cat.png")
	Path string // the resolved absolute path on disk
	URL  string // the minted asset URL ("" in dry-run)
	Size int64  // file size in bytes
}

// schemeRe matches an absolute URL scheme (http:, https:, mailto:, …).
var schemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// Attribute/CSS patterns. RE2 has no backreferences, so each quote style is a
// separate pattern with three capture groups: (prefix)(value)(suffix). The
// attribute set covers media refs: src (img/video/audio/source), poster (video),
// and data (object, e.g. embedded PDFs).
var (
	attrDoubleRe = regexp.MustCompile(`(?i)(\b(?:src|poster|data)\s*=\s*")([^"]*)(")`)
	attrSingleRe = regexp.MustCompile(`(?i)(\b(?:src|poster|data)\s*=\s*')([^']*)(')`)
	srcsetDblRe  = regexp.MustCompile(`(?i)(\bsrcset\s*=\s*")([^"]*)(")`)
	srcsetSglRe  = regexp.MustCompile(`(?i)(\bsrcset\s*=\s*')([^']*)(')`)
	cssURLDblRe  = regexp.MustCompile(`(?i)(url\(\s*")([^"]*)("\s*\))`)
	cssURLSglRe  = regexp.MustCompile(`(?i)(url\(\s*')([^']*)('\s*\))`)
	cssURLBareRe = regexp.MustCompile(`(?i)(url\(\s*)([^)"'\s]+)(\s*\))`)
)

// isLocalRef reports whether ref is a local, rewritable file reference.
func isLocalRef(ref string) bool {
	ref = strings.TrimSpace(ref)
	switch {
	case ref == "":
		return false
	case strings.HasPrefix(ref, "#"): // in-page anchor
		return false
	case strings.HasPrefix(ref, "//"): // protocol-relative → remote
		return false
	case strings.HasPrefix(ref, "/"): // site-absolute — not ours to resolve
		return false
	case schemeRe.MatchString(ref): // http:, https:, data:, blob:, mailto:, …
		return false
	}
	return true
}

// resolveInside resolves ref (minus any ?query/#fragment) against baseDir and
// refuses to escape it — a path-traversal guard so a doc can only pull in files
// under the directory it was authored in.
func resolveInside(baseDir, ref string) (string, error) {
	clean := ref
	if i := strings.IndexAny(clean, "?#"); i >= 0 {
		clean = clean[:i]
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	absP, err := filepath.Abs(filepath.Join(absBase, filepath.FromSlash(clean)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absBase, absP)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("reference %q escapes the doc directory", ref)
	}
	return absP, nil
}

// rewriteAssets rewrites local references in html to asset URLs, uploading each
// via up (skipped when dryRun). It returns the new HTML (unchanged in dry-run),
// the list of referenced local files, and the first error encountered.
func rewriteAssets(html, baseDir string, dryRun bool, up assetUploader) (string, []rewriteUpload, error) {
	cache := map[string]string{} // resolved abs path -> minted URL ("" if dry-run/failed)
	var uploads []rewriteUpload
	var firstErr error

	// process resolves+uploads one reference (memoized by resolved path) and returns
	// the replacement value and whether it changed. On any error it records firstErr
	// and leaves the reference unchanged.
	process := func(ref string) (string, bool) {
		if firstErr != nil || !isLocalRef(ref) {
			return ref, false
		}
		abs, err := resolveInside(baseDir, ref)
		if err != nil {
			firstErr = err
			return ref, false
		}
		if url, ok := cache[abs]; ok {
			if url == "" {
				return ref, false
			}
			return url, true
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			firstErr = fmt.Errorf("read %q (referenced as %q): %w", abs, ref, err)
			return ref, false
		}
		url := ""
		if !dryRun {
			url, err = up(filepath.Base(abs), data)
			if err != nil {
				firstErr = fmt.Errorf("upload %q: %w", abs, err)
				return ref, false
			}
		}
		cache[abs] = url
		uploads = append(uploads, rewriteUpload{Ref: ref, Path: abs, URL: url, Size: int64(len(data))})
		if url == "" {
			return ref, false // dry-run: recorded, but don't touch the HTML
		}
		return url, true
	}

	// replaceGrouped rewrites a 3-group (prefix,value,suffix) attribute/url pattern.
	replaceGrouped := func(s string, re *regexp.Regexp) string {
		return re.ReplaceAllStringFunc(s, func(m string) string {
			g := re.FindStringSubmatch(m)
			if g == nil {
				return m
			}
			nv, changed := process(g[2])
			if !changed {
				return m
			}
			return g[1] + nv + g[3]
		})
	}

	out := html
	// src / poster.
	out = replaceGrouped(out, attrDoubleRe)
	out = replaceGrouped(out, attrSingleRe)
	// srcset: a comma-separated list of "url [descriptor]" entries.
	out = rewriteSrcset(out, srcsetDblRe, process)
	out = rewriteSrcset(out, srcsetSglRe, process)
	// CSS url(...) in <style> blocks and style="" attributes.
	out = replaceGrouped(out, cssURLDblRe)
	out = replaceGrouped(out, cssURLSglRe)
	out = replaceGrouped(out, cssURLBareRe)

	return out, uploads, firstErr
}

// rewriteSrcset rewrites each URL in a srcset attribute value, preserving the
// per-entry descriptors (e.g. "1x", "640w") and comma separators.
func rewriteSrcset(s string, re *regexp.Regexp, process func(string) (string, bool)) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		g := re.FindStringSubmatch(m)
		if g == nil {
			return m
		}
		entries := strings.Split(g[2], ",")
		for i, e := range entries {
			lead := e[:len(e)-len(strings.TrimLeft(e, " \t\n"))]
			fields := strings.Fields(e)
			if len(fields) == 0 {
				continue
			}
			nv, changed := process(fields[0])
			if !changed {
				continue
			}
			fields[0] = nv
			entries[i] = lead + strings.Join(fields, " ")
		}
		return g[1] + strings.Join(entries, ",") + g[3]
	})
}
