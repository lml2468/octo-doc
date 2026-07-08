// Command octo-doc is the single entrypoint: it runs the HTTP server (default),
// applies database migrations, or prints a bootstrap write token.
//
//	octo-doc [serve]     run the server
//	octo-doc migrate     ensure/apply the database schema
//	octo-doc bootstrap   mint and print the first write token
//	octo-doc gc-assets   delete unreferenced media assets past a grace window
package main

import (
	"fmt"
	"os"

	"github.com/lml2468/octo-doc/internal/config"
	"github.com/lml2468/octo-doc/internal/platform/log"
)

func main() {
	cmd := "serve"
	var args []string
	if len(os.Args) > 1 {
		cmd = os.Args[1]
		args = os.Args[2:]
	}
	if err := run(cmd, args); err != nil {
		fmt.Fprintln(os.Stderr, "octo-doc:", err)
		os.Exit(1)
	}
}

func run(cmd string, args []string) error {
	// health is a dependency-free check usable as a container healthcheck; it
	// does not load the full config (only PORT).
	if cmd == "health" {
		return healthCheck()
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := log.New(cfg.LogLevel)

	switch cmd {
	case "serve":
		return serve(cfg, logger)
	case "migrate":
		return migrate(cfg, logger)
	case "bootstrap":
		return bootstrap(cfg)
	case "gc-assets":
		return gcAssets(cfg, logger, args)
	default:
		return fmt.Errorf("unknown command %q\nusage: octo-doc [serve|migrate|bootstrap|gc-assets|health]", cmd)
	}
}
