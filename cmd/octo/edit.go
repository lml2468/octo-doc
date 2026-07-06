package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// cmdVersionAdd appends a new version to a local doc: it writes
// v<n+1>/index.html from the given HTML and records the version in meta.json.
// This is the mechanical half of the /octo edit workflow — the agent generates
// the new HTML, this stamps it into the store.
//
//	octo version-add --slug <slug> (--html-file <path> | --html-stdin) [--prompt <s>]
func cmdVersionAdd(args []string) error {
	fs := flag.NewFlagSet("version-add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		slug      = fs.String("slug", "", "slug of the doc (required)")
		htmlFile  = fs.String("html-file", "", "path to the new version's HTML")
		htmlStdin = fs.Bool("html-stdin", false, "read the new version's HTML from stdin")
		prompt    = fs.String("prompt", "", "prompt-of-record for this version")
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
	cfg := loadConfig()
	st := newStore(cfg.Dir)
	if !st.exists(*slug) {
		return fmt.Errorf("no local doc at %s", st.slugDir(*slug))
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
	meta, err := st.readMeta(*slug)
	if err != nil {
		return err
	}
	next := meta.latestVersion() + 1
	vdir := filepath.Join(st.slugDir(*slug), fmt.Sprintf("v%d", next))
	if err := os.MkdirAll(vdir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(vdir, "index.html"), htmlBytes, 0o644); err != nil {
		return err
	}
	created := nowISO()
	meta.Versions = append(meta.Versions, versionRef{N: next, Created: created, Prompt: *prompt})
	if err := st.writeMeta(*slug, meta); err != nil {
		return err
	}
	fmt.Printf("Added v%d to %s\n", next, *slug)
	fmt.Printf("http://localhost:%d/d/%s/v/%d\n", cfg.Port, *slug, next)
	return nil
}

// cmdReply posts an agent reply to a comment. It targets the local preview server
// by default; with --remote it posts to the configured octo-doc server. This is
// the mechanical half of the edit workflow's mandatory per-comment reply.
//
//	octo reply --slug <slug> --parent <comment-id> --text <s> [--status applied|partial|question] [--applied-in N] [--remote]
func cmdReply(args []string) error {
	fs := flag.NewFlagSet("reply", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		slug      = fs.String("slug", "", "slug of the doc (required)")
		parent    = fs.String("parent", "", "parent comment id (required)")
		text      = fs.String("text", "", "reply text (required)")
		status    = fs.String("status", "", "agent verdict: applied|partial|question")
		appliedIn = fs.Int("applied-in", 0, "version the change was applied in")
		remote    = fs.Bool("remote", false, "post to the configured server instead of the local preview")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *slug == "" || *parent == "" || *text == "" {
		return fmt.Errorf("--slug, --parent, and --text are required")
	}
	cfg := loadConfig()
	var appliedPtr *int
	if *appliedIn > 0 {
		appliedPtr = appliedIn
	}
	if *remote {
		cl, err := requireServer(cfg, true)
		if err != nil {
			return err
		}
		err = cl.agentReply(context.Background(), agentReplyReq{
			Slug: *slug, ParentID: *parent, Text: *text, Status: *status, AppliedIn: appliedPtr,
		})
		if err != nil {
			return err
		}
		fmt.Printf("Replied to %s on %s (remote)\n", *parent, *slug)
		return nil
	}
	// Local preview: POST to the loopback server (JSON content-type satisfies the
	// CSRF guard; no auth on local).
	payload := map[string]any{"slug": *slug, "parent_id": *parent, "text": *text}
	if *status != "" {
		payload["status"] = *status
	}
	if appliedPtr != nil {
		payload["applied_in"] = *appliedPtr
	}
	b, _ := json.Marshal(payload)
	resp, err := http.Post(localURL(cfg.Port, "/v1/agent/replies"), "application/json", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("local preview not reachable (start it with `octo preview start`): %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("reply failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	fmt.Printf("Replied to %s on %s (local)\n", *parent, *slug)
	return nil
}
