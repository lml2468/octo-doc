package main

import (
	"context"
	"errors"
	"fmt"
)

// cmdShare mints (or rotates) the per-doc share code and prints a link that grants
// read + comment access to anyone who opens it. Author-only (uses the write
// token). Re-running rotates the code, invalidating older links.
//
//	octo share <slug>            # mint or rotate the share code, print the URL
//	octo share <slug> --revoke   # clear the code (existing links stop working)
func cmdShare(args []string) error {
	revoke := false
	var slug string
	for _, a := range args {
		switch a {
		case "--revoke":
			revoke = true
		default:
			if slug == "" {
				slug = a
			}
		}
	}
	if slug == "" {
		return errors.New("usage: octo share <slug> [--revoke]")
	}
	cfg := loadConfig()
	cl, err := requireServer(cfg, true) // author op → write token required
	if err != nil {
		return err
	}
	ctx := context.Background()

	if revoke {
		if err := cl.revokeShare(ctx, slug); err != nil {
			return err
		}
		fmt.Printf("Revoked the share code for %s — existing links no longer work.\n", slug)
		return nil
	}

	res, err := cl.share(ctx, slug)
	if err != nil {
		return err
	}
	fmt.Printf("Share link for %s (read + comment):\n\n  %s\n\n", slug, res.URL)
	fmt.Println("Anyone with this link can read and comment. Re-run `octo share` to rotate it.")
	return nil
}
