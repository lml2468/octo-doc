package main

import (
	"context"
	"errors"
	"fmt"
)

// cmdUnpublish deletes a published doc (all versions + comments) from the server.
// Local files are left untouched.
func cmdUnpublish(args []string) error {
	if len(args) < 1 || args[0] == "" {
		return errors.New("usage: octo unpublish <slug>")
	}
	slug := args[0]
	cfg := loadConfig()
	cl, err := requireServer(cfg, true)
	if err != nil {
		return err
	}
	if err := cl.unpublish(context.Background(), slug); err != nil {
		return err
	}
	fmt.Printf("Unpublished %s.\n", slug)
	return nil
}
