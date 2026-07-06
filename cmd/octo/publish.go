package main

import (
	"context"
	"errors"
	"fmt"
)

// requireServer resolves config and returns a client, requiring a base URL (and,
// when needWrite is set, a write token). It persists the resolved config so the
// next run needs no env.
func requireServer(cfg config, needWrite bool) (*client, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New(`no octo-doc server configured.

Set the server to author against:
    export OCTO_BASE_URL="https://your-host"   # or http://localhost:8080
    export OCTO_TOKEN="<write token>"          # from: octo-doc bootstrap

To mint a token from a fresh server (only when it has no static WRITE_TOKEN):
    curl -sS -X POST "$OCTO_BASE_URL/v1/admin/bootstrap" | jq -r .data.token`)
	}
	if needWrite && cfg.Token == "" {
		return nil, fmt.Errorf("no write token. Set OCTO_TOKEN or add it to ~/.octo/config.json\n"+
			"       Get one with: curl -sS -X POST %q/v1/admin/bootstrap | jq -r .data.token", cfg.BaseURL)
	}
	if needWrite {
		_ = saveConfig(cfg.BaseURL, cfg.Token) // best-effort
	}
	// Credential sent as Bearer: the write token for author ops; for reader ops
	// (pull/comment on a private doc) fall back to the doc share code.
	cred := cfg.Token
	if cred == "" {
		cred = cfg.Code
	}
	return newClient(cfg.BaseURL, cred), nil
}

// cmdPublish promotes the doc's current server-side draft to a new immutable
// published version. Authoring iterates on the mutable draft (via `octo new` /
// `octo version-add`); publish is the explicit "freeze this" step.
//
//	octo publish <slug>
func cmdPublish(args []string) error {
	if len(args) < 1 || args[0] == "" {
		return errors.New("usage: octo publish <slug>")
	}
	slug := args[0]
	cfg := loadConfig()
	cl, err := requireServer(cfg, true)
	if err != nil {
		return err
	}
	res, err := cl.promote(context.Background(), slug)
	if err != nil {
		return err
	}
	fmt.Printf("Published: %s\n", res.URL)
	return nil
}
