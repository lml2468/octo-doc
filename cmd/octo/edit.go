package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// cmdVersionAdd saves the next iteration's HTML as the doc's mutable draft on the
// server (overwriting any current draft) and updates the local working copy. This
// is the mechanical half of the edit workflow: the agent generates new HTML, this
// stages it as a draft; `octo publish` later promotes it to an immutable version.
//
//	octo version-add --slug <slug> (--html-file <path> | --html-stdin) [--prompt <s>]
func cmdVersionAdd(args []string) error {
	fs := flag.NewFlagSet("version-add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		slug      = fs.String("slug", "", "slug of the doc (required)")
		htmlFile  = fs.String("html-file", "", "path to the new draft's HTML")
		htmlStdin = fs.Bool("html-stdin", false, "read the new draft's HTML from stdin")
		prompt    = fs.String("prompt", "", "prompt-of-record for this iteration")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *slug == "" {
		return fmt.Errorf("--slug required")
	}
	if *htmlFile != "" && *htmlStdin {
		return fmt.Errorf("use --html-file OR --html-stdin, not both")
	}
	if *htmlFile == "" && !*htmlStdin {
		return fmt.Errorf("need either --html-file <path> or --html-stdin")
	}
	var htmlBytes []byte
	var err error
	if *htmlStdin {
		htmlBytes, err = io.ReadAll(os.Stdin)
	} else {
		htmlBytes, err = os.ReadFile(*htmlFile)
	}
	if err != nil {
		return err
	}
	if len(htmlBytes) == 0 {
		return fmt.Errorf("html content was empty")
	}
	if !strings.Contains(strings.ToLower(string(htmlBytes)), "<body") {
		return fmt.Errorf("html does not contain a <body> tag — did you pass markdown by mistake?")
	}

	cfg := loadConfig()
	cl, err := requireServer(cfg, true) // updating a draft is an author op
	if err != nil {
		return err
	}
	st := newStore(cfg.Dir)
	title := *slug
	if meta, merr := st.readMeta(*slug); merr == nil {
		title = meta.Title
	}
	if err := cl.saveDraft(context.Background(), *slug, string(htmlBytes), title); err != nil {
		return fmt.Errorf("save draft: %w", err)
	}
	// Mirror into the local working copy's draft source (v/draft dir) for reference.
	if err := st.writeDraftCopy(*slug, *prompt, htmlBytes); err != nil {
		return err
	}
	fmt.Printf("Draft updated for %s\n", *slug)
	fmt.Printf("%s/d/%s/draft\n", cfg.BaseURL, *slug)
	return nil
}

// writeDraftCopy records the current draft HTML in the local working copy under a
// "draft" dir (mirrors the server's draft slot; not an immutable version).
func (s *store) writeDraftCopy(slug, prompt string, html []byte) error {
	if !s.exists(slug) {
		if err := os.MkdirAll(s.slugDir(slug), 0o755); err != nil {
			return err
		}
	}
	ddir := filepath.Join(s.slugDir(slug), "draft")
	if err := os.MkdirAll(ddir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ddir, "index.html"), html, 0o644); err != nil {
		return err
	}
	if meta, err := s.readMeta(slug); err == nil {
		meta.DraftPrompt = prompt
		return s.writeMeta(slug, meta)
	}
	return nil
}

// cmdReply posts an agent reply to a comment on the configured server. This is the
// edit workflow's mandatory per-comment reply (applied / partial / question).
//
//	octo reply --slug <slug> --parent <comment-id> --text <s> [--status applied|partial|question] [--applied-in N]
func cmdReply(args []string) error {
	fs := flag.NewFlagSet("reply", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		slug      = fs.String("slug", "", "slug of the doc (required)")
		parent    = fs.String("parent", "", "parent comment id (required)")
		text      = fs.String("text", "", "reply text (required)")
		status    = fs.String("status", "", "agent verdict: applied|partial|question")
		appliedIn = fs.Int("applied-in", 0, "version the change was applied in")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *slug == "" || *parent == "" || *text == "" {
		return fmt.Errorf("--slug, --parent, and --text are required")
	}
	cfg := loadConfig()
	cl, err := requireServer(cfg, true)
	if err != nil {
		return err
	}
	var appliedPtr *int
	if *appliedIn > 0 {
		appliedPtr = appliedIn
	}
	err = cl.agentReply(context.Background(), agentReplyReq{
		Slug: *slug, ParentID: *parent, Text: *text, Status: *status, AppliedIn: appliedPtr,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Replied to %s on %s\n", *parent, *slug)
	return nil
}
