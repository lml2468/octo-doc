// Command octo is the agent-side client for octo-doc: it scaffolds prompt-native
// interactive HTML docs on disk, serves them locally with the canonical overlay
// injected, and publishes them to a self-hosted octo-doc server.
//
// It is a separate binary from the server (cmd/octo-doc): it links no database or
// blob store, only the pure core kernel and the embedded overlay. This is what
// lets the local preview render byte-identically to the published server without
// a mirrored copy of overlay.js.
//
//	octo new        scaffold a doc from finished HTML (+ optional publish)
//	octo preview    run the local preview server (serve|stop|status)
//	octo publish    upload a local doc's versions to an octo-doc server
//	octo pull       merge server comments into the local comments.json
//	octo unpublish  delete a published doc from the server
//	octo list       list local docs
//	octo fork       copy a local doc under a new slug
//	octo version-add append a new version to a local doc
//	octo reply      post an agent reply to a comment (local or remote)
//	octo doctor     health-check local deps + the configured server
//	octo update     self-update from GitHub Releases
//	octo version    print the CLI version
package main

import (
	"fmt"
	"os"
)

// version is stamped at build time via -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "octo:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := ""
	var rest []string
	if len(args) > 0 {
		cmd = args[0]
		rest = args[1:]
	}

	switch cmd {
	case "new":
		return cmdNew(rest)
	case "preview":
		return cmdPreview(rest)
	case "publish":
		return cmdPublish(rest)
	case "pull":
		return cmdPull(rest)
	case "unpublish":
		return cmdUnpublish(rest)
	case "share":
		return cmdShare(rest)
	case "list":
		return cmdList(rest)
	case "fork":
		return cmdFork(rest)
	case "version-add":
		return cmdVersionAdd(rest)
	case "comment":
		return cmdComment(rest)
	case "react":
		return cmdReact(rest)
	case "reply":
		return cmdReply(rest)
	case "doctor":
		return cmdDoctor(rest)
	case "update":
		return cmdUpdate(rest)
	case "version", "--version", "-v":
		fmt.Println("octo", version)
		return nil
	case "", "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `octo — the octo-doc agent CLI

usage: octo <command> [args]

commands:
  new          scaffold a doc from finished HTML (+ optional publish)
  preview      run the local preview server (serve|stop|status)
  publish      upload a local doc's versions to an octo-doc server
  pull         merge server comments into the local comments.json
  unpublish    delete a published doc from the server
  share        mint/rotate a read+comment share link for a doc
  list         list local docs
  fork         copy a local doc under a new slug
  version-add  append a new version to a local doc
  comment      post a human comment or reply to a doc
  react        toggle an emoji reaction on a comment
  reply        post an agent reply to a comment (local or remote)
  doctor       health-check local deps + the configured server
  update       self-update from GitHub Releases
  version      print the CLI version

config (env wins, then ~/.octo/config.json, then ~/.tdoc fallbacks):
  OCTO_BASE_URL  server to publish to (e.g. https://docs.example.com)
  OCTO_TOKEN     write token (Authorization: Bearer)
  OCTO_DIR       local doc store (default ~/octo-docs, else existing ~/tdocs)
  OCTO_PORT      local preview port (default 7878)
`)
}
