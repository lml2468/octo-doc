package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// cmdNew creates a doc remote-first: it validates the finished HTML, keeps a local
// working copy (so `version-add`/`pull` have a source), then saves it as a mutable
// draft on the server and prints the draft URL. This is also the programmatic
// entry other skills call when they already have a doc-shaped artifact.
//
// With --open, it opens the draft in a browser. Because the draft is author-only
// and a browser can't send a Bearer header, the URL carries ?code=<write-token>,
// which the server exchanges for an HttpOnly cookie and strips from the address bar.
func cmdNew(args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		slug      = fs.String("slug", "", "slug for the new doc (kebab-case, required)")
		title     = fs.String("title", "", "human-readable title (required)")
		htmlFile  = fs.String("html-file", "", "path to the full HTML")
		htmlStdin = fs.Bool("html-stdin", false, "read HTML from stdin")
		prompt    = fs.String("prompt", "", "prompt-of-record stored in meta.json")
		open      = fs.Bool("open", false, "open the draft in the default browser")
		quiet     = fs.Bool("quiet", false, "suppress informational output")
		force     = fs.Bool("force", false, "overwrite an existing local slug")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	info := func(format string, a ...any) {
		if !*quiet {
			fmt.Fprintf(os.Stderr, "[octo-new] "+format+"\n", a...)
		}
	}

	if *slug == "" {
		return fmt.Errorf("--slug required")
	}
	if *title == "" {
		return fmt.Errorf("--title required")
	}
	if !kebabRE.MatchString(*slug) {
		return fmt.Errorf("--slug must be lowercase kebab-case (a-z, 0-9, -); got %q", *slug)
	}
	if *htmlFile != "" && *htmlStdin {
		return fmt.Errorf("use --html-file OR --html-stdin, not both")
	}
	if *htmlFile == "" && !*htmlStdin {
		return fmt.Errorf("need either --html-file <path> or --html-stdin")
	}

	cfg := loadConfig()
	cl, err := requireServer(cfg, true) // creating a draft is an author op
	if err != nil {
		return err
	}
	st := newStore(cfg.Dir)
	if st.exists(*slug) && !*force {
		return fmt.Errorf("slug %q already exists at %s; use --force to overwrite, or a different slug", *slug, st.slugDir(*slug))
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return err
	}

	// Read the HTML into memory and validate before touching anything.
	var htmlBytes []byte
	if *htmlStdin {
		htmlBytes, err = io.ReadAll(os.Stdin)
	} else {
		htmlBytes, err = os.ReadFile(*htmlFile)
	}
	if err != nil {
		return fmt.Errorf("could not read HTML: %w", err)
	}
	if len(htmlBytes) == 0 {
		return fmt.Errorf("html content was empty (existing doc, if any, left untouched)")
	}
	if !strings.Contains(strings.ToLower(string(htmlBytes)), "<body") {
		return fmt.Errorf("html does not contain a <body> tag — did you pass markdown by mistake? (existing doc left untouched)")
	}

	// Save the draft on the server first — if that fails, nothing local changes.
	if err := cl.saveDraft(context.Background(), *slug, string(htmlBytes), *title); err != nil {
		return fmt.Errorf("save draft: %w", err)
	}

	// Keep a local working copy (the source for version-add / pull). Stage →
	// validate → swap so a re-run can't corrupt an existing copy.
	if err := st.writeWorkingCopy(*slug, *title, *prompt, htmlBytes); err != nil {
		return err
	}
	info("draft saved for %s", *slug)

	draftURL := cfg.BaseURL + "/d/" + *slug + "/draft"
	// The author's browser needs the write token as ?code= (exchanged for a cookie).
	authorURL := draftURL + "?code=" + url.QueryEscape(cfg.Token)
	if *open {
		openBrowser(authorURL)
	}
	// Print the plain draft URL (last line, capturable). The author opens it with
	// --open, or appends ?code=<token> themselves.
	fmt.Println(draftURL)
	return nil
}

// writeWorkingCopy persists the local working copy: meta.json + comments.json +
// v1/index.html (the draft's source), replacing any existing copy atomically-ish.
func (s *store) writeWorkingCopy(slug, title, prompt string, html []byte) error {
	stage, err := os.MkdirTemp(s.dir, ".stage-"+slug+"-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := os.MkdirAll(filepath.Join(stage, "v1"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "v1", "index.html"), html, 0o644); err != nil {
		return err
	}
	docDir := s.slugDir(slug)
	if s.exists(slug) {
		if err := os.RemoveAll(docDir); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(docDir, 0o755); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(stage, "v1"), filepath.Join(docDir, "v1")); err != nil {
		return err
	}
	created := nowISO()
	caller := envFirst("OCTO_NEW_CALLER", "TDOC_NEW_CALLER", "CLAUDE_SKILL_NAME")
	if caller == "" {
		caller = "unknown"
	}
	recorded := prompt
	if recorded == "" {
		recorded = "Imported via octo new by " + caller
	}
	meta := &docMeta{
		Title:    title,
		Slug:     slug,
		Created:  created,
		Versions: []versionRef{{N: 1, Created: created, Prompt: recorded}},
	}
	if err := s.writeMeta(slug, meta); err != nil {
		return err
	}
	return s.writeComments(slug, []comment{})
}

// openBrowser best-effort opens a URL in the default browser.
func openBrowser(target string) {
	var bin string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		bin, args = "open", []string{target}
	case "windows":
		bin, args = "rundll32", []string{"url.dll,FileProtocolHandler", target}
	default:
		bin, args = "xdg-open", []string{target}
	}
	if _, err := exec.LookPath(bin); err != nil {
		return
	}
	_ = exec.Command(bin, args...).Start()
}
