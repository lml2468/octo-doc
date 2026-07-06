package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// cmdNew scaffolds a doc from finished HTML — the programmatic entry other agents
// call when they already have a doc-shaped artifact. It stage-validates the HTML
// in a temp dir and only swaps it into place on success, so a bad payload can
// never destroy an existing doc. It then ensures the local preview is up and
// prints the local URL last (callers capture it); with --publish, the published
// URL is printed on a second line.
func cmdNew(args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		slug      = fs.String("slug", "", "slug for the new doc (kebab-case, required)")
		title     = fs.String("title", "", "human-readable title (required)")
		htmlFile  = fs.String("html-file", "", "path to the full HTML for v1")
		htmlStdin = fs.Bool("html-stdin", false, "read HTML for v1 from stdin")
		prompt    = fs.String("prompt", "", "prompt-of-record stored in meta.json")
		publish   = fs.Bool("publish", false, "also publish after scaffolding")
		open      = fs.Bool("open", false, "open the URL in the default browser")
		quiet     = fs.Bool("quiet", false, "suppress informational output")
		force     = fs.Bool("force", false, "overwrite an existing slug")
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
	st := newStore(cfg.Dir)
	if st.exists(*slug) && !*force {
		return fmt.Errorf("slug %q already exists at %s; use --force to overwrite, or a different slug", *slug, st.slugDir(*slug))
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return err
	}

	// Read the HTML into memory and validate before touching the doc dir.
	var htmlBytes []byte
	var err error
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

	// Stage → validate → swap: write into a temp dir under cfg.Dir, then move.
	stage, err := os.MkdirTemp(cfg.Dir, ".stage-"+*slug+"-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := os.MkdirAll(filepath.Join(stage, "v1"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "v1", "index.html"), htmlBytes, 0o644); err != nil {
		return err
	}

	// Validation passed — now it's safe to replace.
	docDir := st.slugDir(*slug)
	if st.exists(*slug) {
		info("overwriting existing slug %q (--force)", *slug)
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

	// meta.json + empty comments.json.
	created := nowISO()
	caller := envFirst("OCTO_NEW_CALLER", "TDOC_NEW_CALLER", "CLAUDE_SKILL_NAME")
	if caller == "" {
		caller = "unknown"
	}
	recorded := *prompt
	if recorded == "" {
		recorded = "Imported via octo new by " + caller
	}
	meta := &docMeta{
		Title:    *title,
		Slug:     *slug,
		Created:  created,
		Versions: []versionRef{{N: 1, Created: created, Prompt: recorded}},
	}
	if err := st.writeMeta(*slug, meta); err != nil {
		return err
	}
	if err := st.writeComments(*slug, []comment{}); err != nil {
		return err
	}
	info("scaffolded %s (v1)", docDir)

	// Ensure the local preview server is up so the printed URL is live.
	if err := previewStart(cfg); err != nil {
		return fmt.Errorf("doc scaffolded at %s but preview not serving: %w", docDir, err)
	}
	localURLStr := fmt.Sprintf("http://localhost:%d/d/%s/v/1", cfg.Port, *slug)

	// Optional publish (non-fatal on failure — the local doc still stands).
	publishedURL := ""
	if *publish {
		info("publishing to octo-doc server...")
		if err := cmdPublish([]string{*slug}); err != nil {
			fmt.Fprintf(os.Stderr, "octo-new: publish failed; local doc is still available: %v\n", err)
		} else {
			publishedURL = fmt.Sprintf("%s/d/%s/v/1", cfg.BaseURL, *slug)
		}
	}

	if *open {
		target := localURLStr
		if publishedURL != "" {
			target = publishedURL
		}
		openBrowser(target)
	}

	// Output contract: last line(s) are URLs callers can capture.
	fmt.Println(localURLStr)
	if publishedURL != "" {
		fmt.Println(publishedURL)
	}
	return nil
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
