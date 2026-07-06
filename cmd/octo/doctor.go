package main

import (
	"fmt"
	"os"
)

// ANSI status markers, matching the bash doctor's ✓/!/✗ output.
func ok(msg string)   { fmt.Printf("  \033[32m✓\033[0m %s\n", msg) }
func warn(msg string) { fmt.Printf("  \033[33m!\033[0m %s\n", msg) }
func bad(msg string)  { fmt.Printf("  \033[31m✗\033[0m %s\n", msg) }

// cmdDoctor is a read-only health check: it confirms the CLI's own version, the
// local preview server, and (if configured) reachability + auth of the remote
// octo-doc server. Unlike the bash doctor it needs no node/jq/curl — the CLI is
// self-contained — so it only reports on what actually matters now.
func cmdDoctor(_ []string) error {
	cfg := loadConfig()
	fmt.Println("octo doctor")
	fmt.Println()

	fmt.Println("CLI:")
	ok(fmt.Sprintf("octo %s", version))
	ok(fmt.Sprintf("doc store: %s", cfg.Dir))
	if _, err := os.Stat(cfg.Dir); err != nil {
		warn("doc store does not exist yet (created on first `octo new`)")
	}
	fmt.Println()

	fmt.Printf("Local preview server (:%d):\n", cfg.Port)
	switch {
	case pingIsOurs(cfg.Port):
		ok(fmt.Sprintf("preview up at http://localhost:%d", cfg.Port))
	case portAnswers(cfg.Port):
		bad(fmt.Sprintf("port %d answers HTTP but is NOT the octo preview (foreign service)", cfg.Port))
	default:
		warn("preview not running (start: octo preview start)")
	}
	fmt.Println()

	fmt.Println("Remote octo-doc server:")
	if cfg.BaseURL == "" {
		warn("no server configured. Set OCTO_BASE_URL (+ OCTO_TOKEN) or run `octo publish <slug>`.")
	} else {
		if pingService(cfg.BaseURL+"/v1/ping") == "octo-doc" {
			ok("server reachable: " + cfg.BaseURL)
		} else {
			bad("server " + cfg.BaseURL + " did not answer /v1/ping — check the URL / that it's running")
		}
		if cfg.Token != "" {
			ok("write token configured")
		} else {
			warn(fmt.Sprintf("no write token. Mint one: curl -sS -X POST %q/v1/admin/bootstrap | jq -r .data.token", cfg.BaseURL))
		}
	}
	fmt.Println()
	fmt.Println("Done.")
	return nil
}
