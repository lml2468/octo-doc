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
	"strings"

	"github.com/Mininglamp-OSS/octo-doc/internal/core"
)

// cmdComment posts a human comment (or a reply, with --parent) to a doc. It targets
// the configured server by default, or the local preview with --local. A --anchor
// binds a top-level comment to specific text. Comments are public, so no token is
// needed for the remote path.
//
//	octo comment --slug <slug> --text <s> [--version N] [--anchor <text>] [--parent <id>] [--local]
//
// It prints the new comment/reply id on stdout so scripts can capture it.
func cmdComment(args []string) error {
	fs := flag.NewFlagSet("comment", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		slug    = fs.String("slug", "", "slug of the doc (required)")
		text    = fs.String("text", "", "comment text (required)")
		version = fs.Int("version", 0, "document version the comment targets")
		anchor  = fs.String("anchor", "", "exact text to anchor a top-level comment to")
		parent  = fs.String("parent", "", "parent comment id (makes this a reply)")
		local   = fs.Bool("local", false, "post to the local preview instead of the configured server")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *slug == "" || *text == "" {
		return fmt.Errorf("--slug and --text are required")
	}
	cfg := loadConfig()

	if *local {
		payload := map[string]any{"slug": *slug, "text": *text}
		if *version > 0 {
			payload["version"] = *version
		}
		if *parent != "" {
			payload["parent_id"] = *parent
		}
		if *anchor != "" {
			payload["anchor"] = map[string]string{"kind": "text", "text": *anchor}
		}
		id, err := postLocalComment(cfg, payload)
		if err != nil {
			return err
		}
		fmt.Println(id)
		return nil
	}

	cl, err := requireServer(cfg, false)
	if err != nil {
		return err
	}
	req := commentReq{Slug: *slug, Text: *text, Version: *version, ParentID: *parent}
	if *anchor != "" {
		req.Anchor = &core.Anchor{Kind: "text", Text: *anchor}
	}
	id, err := cl.createComment(context.Background(), req)
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

// cmdReact toggles an emoji reaction on a comment. Remote by default, --local for
// the preview.
//
//	octo react --slug <slug> --comment <id> --emoji <e> [--version N] [--local]
func cmdReact(args []string) error {
	fs := flag.NewFlagSet("react", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		slug    = fs.String("slug", "", "slug of the doc (required)")
		comment = fs.String("comment", "", "comment id to react on (required)")
		emoji   = fs.String("emoji", "", "the emoji (required)")
		version = fs.Int("version", 0, "document version")
		local   = fs.Bool("local", false, "react on the local preview instead of the configured server")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *slug == "" || *comment == "" || *emoji == "" {
		return fmt.Errorf("--slug, --comment, and --emoji are required")
	}
	cfg := loadConfig()

	if *local {
		payload := map[string]any{"slug": *slug, "comment_id": *comment, "emoji": *emoji}
		if *version > 0 {
			payload["version"] = *version
		}
		if err := postLocal(cfg, "/v1/reactions", payload); err != nil {
			return err
		}
		fmt.Printf("Reacted %s on %s (local)\n", *emoji, *comment)
		return nil
	}

	cl, err := requireServer(cfg, false)
	if err != nil {
		return err
	}
	if err := cl.react(context.Background(), *slug, *comment, *emoji, *version); err != nil {
		return err
	}
	fmt.Printf("Reacted %s on %s\n", *emoji, *comment)
	return nil
}

// postLocalComment POSTs to the local preview's /v1/comments and returns the new id.
func postLocalComment(cfg config, payload map[string]any) (string, error) {
	b, _ := json.Marshal(payload)
	resp, err := http.Post(localURL(cfg.Port, "/v1/comments"), "application/json", bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("local preview not reachable (start it with `octo preview start`): %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("comment failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var env struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", err
	}
	return env.Data.ID, nil
}

// postLocal POSTs a JSON payload to a local preview endpoint, erroring on non-2xx.
func postLocal(cfg config, path string, payload map[string]any) error {
	b, _ := json.Marshal(payload)
	resp, err := http.Post(localURL(cfg.Port, path), "application/json", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("local preview not reachable (start it with `octo preview start`): %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("request failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}
