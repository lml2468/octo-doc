package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// cmdFork copies a local doc under a new slug, resetting its comment thread and
// marking the title as a fork — a fresh doc seeded from an existing one.
//
//	octo fork <slug> [new-slug]
//
// If new-slug is omitted, it defaults to "<slug>-fork".
func cmdFork(args []string) error {
	if len(args) < 1 || args[0] == "" {
		return fmt.Errorf("usage: octo fork <slug> [new-slug]")
	}
	src := args[0]
	dst := src + "-fork"
	if len(args) > 1 && args[1] != "" {
		dst = args[1]
	}
	if !kebabRE.MatchString(dst) {
		return fmt.Errorf("new slug must be lowercase kebab-case; got %q", dst)
	}
	cfg := loadConfig()
	st := newStore(cfg.Dir)
	if !st.exists(src) {
		return fmt.Errorf("no local doc at %s", st.slugDir(src))
	}
	if st.exists(dst) {
		return fmt.Errorf("slug %q already exists; pick another", dst)
	}
	if err := copyTree(st.slugDir(src), st.slugDir(dst)); err != nil {
		return err
	}
	// Reset the fork's comment thread and retitle.
	if err := st.writeComments(dst, []comment{}); err != nil {
		return err
	}
	if meta, err := st.readMeta(dst); err == nil {
		meta.Slug = dst
		meta.Title = meta.Title + " (fork)"
		if err := st.writeMeta(dst, meta); err != nil {
			return err
		}
	}
	fmt.Printf("Forked %s -> %s\n", src, dst)
	return nil
}

// copyTree recursively copies a directory tree.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}
