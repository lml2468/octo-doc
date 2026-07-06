package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// cmdPull fetches the full cross-version comment history from the server and
// merges it into the local comments.json non-destructively: remote wins on id
// collisions, local-only comments are kept, and a .bak of the prior file is
// written before overwrite. Reads are public, so no token is required.
func cmdPull(args []string) error {
	if len(args) < 1 || args[0] == "" {
		return errors.New("usage: octo pull <slug>")
	}
	slug := args[0]
	cfg := loadConfig()
	cl, err := requireServer(cfg, false)
	if err != nil {
		return err
	}
	st := newStore(cfg.Dir)
	if err := os.MkdirAll(st.slugDir(slug), 0o755); err != nil {
		return err
	}

	wire, err := cl.listComments(context.Background(), slug)
	if err != nil {
		return fmt.Errorf("%w. Local file untouched", err)
	}
	remote := make([]comment, 0, len(wire))
	for _, w := range wire {
		remote = append(remote, w.toComment())
	}

	out := st.commentsPath(slug)
	local, hadLocal := existingComments(out)
	merged := remote
	if hadLocal {
		if err := backup(out); err != nil {
			return err
		}
		remoteIDs := make(map[string]bool, len(remote))
		for _, c := range remote {
			remoteIDs[c.ID] = true
		}
		localOnly := 0
		for _, c := range local {
			if !remoteIDs[c.ID] {
				merged = append(merged, c)
				localOnly++
			}
		}
		if localOnly > 0 {
			fmt.Fprintf(os.Stderr, "Merged %d local-only comment(s) (backup at %s.bak).\n", localOnly, out)
		}
	}
	if err := st.writeComments(slug, merged); err != nil {
		return err
	}
	fmt.Printf("Pulled %d comments for %s -> %s\n", len(merged), slug, out)
	return nil
}

// existingComments reads a comments.json only if it parses to a valid array,
// mirroring the bash `jq -e 'type == "array"'` guard. The bool reports validity.
func existingComments(path string) ([]comment, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	// Unmarshaling into a slice fails on a non-array (error object, {}, etc.), so
	// this doubles as the array-type check.
	var list []comment
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, false
	}
	return list, true
}

// backup copies path to path.bak.
func backup(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path+".bak", b, 0o644)
}
