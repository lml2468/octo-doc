// Command octo is the agent-side client for octo-doc: it authors prompt-native
// interactive HTML docs against a remote octo-doc server. `new` saves HTML as a
// mutable server-side draft; `publish` promotes the draft to an immutable version.
// Documents are private by default; `share` mints a per-doc code granting
// read+comment access.
//
// It is a separate binary from the server (cmd/octo-doc): it links no database or
// blob store. Authoring and preview happen against a running server (local docker
// stack or a hosted instance) — there is no local preview server.
//
//	octo new        create a doc: save HTML as a server-side draft, open it
//	octo publish    promote the current draft to an immutable published version
//	octo pull       merge server comments into the local comments.json
//	octo unpublish  delete a published doc from the server
//	octo share      mint/rotate a read+comment share link for a doc
//	octo asset-add  upload a media file and print its doc-referenceable URL
//	octo asset-list list a doc's uploaded media assets
//	octo asset-rm   delete an uploaded media asset by content hash
//	octo list       list local docs
//	octo fork       copy a local doc under a new slug
//	octo version-add save a new draft (the next version's HTML) for a doc
//	octo comment    post a human comment or reply to a doc
//	octo react      toggle an emoji reaction on a comment
//	octo reply      post an agent reply to a comment
//	octo doctor     health-check the CLI + the configured server
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
	case "publish":
		return cmdPublish(rest)
	case "pull":
		return cmdPull(rest)
	case "unpublish":
		return cmdUnpublish(rest)
	case "share":
		return cmdShare(rest)
	case "asset-add":
		return cmdAssetAdd(rest)
	case "asset-list":
		return cmdAssetList(rest)
	case "asset-rm":
		return cmdAssetRm(rest)
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
  new          create a doc: save HTML as a server-side draft, open it
  publish      promote the current draft to an immutable published version
  pull         merge server comments into the local comments.json
  unpublish    delete a published doc from the server
  share        mint/rotate a read+comment share link for a doc
  asset-add    upload a media file (image/video/…) and print its doc URL
  asset-list   list a doc's uploaded media assets
  asset-rm     delete an uploaded media asset by its content hash
  list         list local docs
  fork         copy a local doc under a new slug
  version-add  save a new draft (the next version's HTML) for a doc
  comment      post a human comment or reply to a doc
  react        toggle an emoji reaction on a comment
  reply        post an agent reply to a comment
  doctor       health-check the CLI + the configured server
  update       self-update from GitHub Releases
  version      print the CLI version

config (env wins, then ~/.octo/config.json):
  OCTO_BASE_URL  server to author against (e.g. https://docs.example.com)
  OCTO_TOKEN     write token (Authorization: Bearer) — author credential
  OCTO_CODE      a doc share code (reader credential, for pull/comment)
  OCTO_DIR       local working copy (default ~/octo-docs)
`)
}
