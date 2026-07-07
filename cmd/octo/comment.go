package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/lml2468/octo-doc/internal/core"
)

// cmdComment posts a human comment (or a reply, with --parent) to a doc on the
// configured server. A --anchor binds a top-level comment to specific text.
// Requires at least a reader credential (the doc's share code via OCTO_CODE, or
// the write token). Prints the new comment/reply id.
//
//	octo comment --slug <slug> --text <s> [--version N] [--anchor <text>] [--parent <id>]
func cmdComment(args []string) error {
	fs := flag.NewFlagSet("comment", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		slug    = fs.String("slug", "", "slug of the doc (required)")
		text    = fs.String("text", "", "comment text (required)")
		version = fs.Int("version", 0, "document version the comment targets")
		anchor  = fs.String("anchor", "", "exact text to anchor a top-level comment to")
		parent  = fs.String("parent", "", "parent comment id (makes this a reply)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *slug == "" || *text == "" {
		return fmt.Errorf("--slug and --text are required")
	}
	cfg := loadConfig()
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

// cmdReact toggles an emoji reaction on a comment on the configured server.
//
//	octo react --slug <slug> --comment <id> --emoji <e> [--version N]
func cmdReact(args []string) error {
	fs := flag.NewFlagSet("react", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		slug    = fs.String("slug", "", "slug of the doc (required)")
		comment = fs.String("comment", "", "comment id to react on (required)")
		emoji   = fs.String("emoji", "", "the emoji (required)")
		version = fs.Int("version", 0, "document version")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *slug == "" || *comment == "" || *emoji == "" {
		return fmt.Errorf("--slug, --comment, and --emoji are required")
	}
	cfg := loadConfig()
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
