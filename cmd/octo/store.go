package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	// Aliased because the local `config` type would shadow the package name.
	configpkg "github.com/Mininglamp-OSS/octo-doc/internal/config"
)

// The on-disk doc store, rooted at cfg.Dir:
//
//	<dir>/<slug>/meta.json           title, slug, created, versions[]
//	<dir>/<slug>/comments.json       flat JSON array of comments (with nested replies)
//	<dir>/<slug>/v<n>/index.html     the immutable HTML for version n
//
// This layout and the comments.json flat-array shape are the contract shared by
// the preview server, publish, and pull — they round-trip the same bytes the
// server persists, mapping only the created/created_at timestamp field at the
// wire boundary.

// kebabRE is the stricter form required when creating a doc: lowercase kebab.
var kebabRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// safeSlug returns the slug if it is safe to map to a path, else "". It delegates
// to config.SafeSlug so the client's path-traversal guard can never drift from
// the server's slug rule — a security-relevant check kept in one place.
func safeSlug(slug string) string {
	return configpkg.SafeSlug(slug)
}

// versionRef is one entry in a doc's version history (meta.json schema; uses
// "created", not the wire's "created_at").
type versionRef struct {
	N       int    `json:"n"`
	Created string `json:"created,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
}

// docMeta is the meta.json shape.
type docMeta struct {
	Title    string       `json:"title"`
	Slug     string       `json:"slug"`
	Created  string       `json:"created,omitempty"`
	Versions []versionRef `json:"versions"`
}

// store is the filesystem-backed doc store.
type store struct {
	dir string
}

func newStore(dir string) *store { return &store{dir: dir} }

// slugDir returns the directory for a slug.
func (s *store) slugDir(slug string) string { return filepath.Join(s.dir, slug) }

// metaPath / commentsPath / htmlPath resolve the well-known files for a slug.
func (s *store) metaPath(slug string) string {
	return filepath.Join(s.slugDir(slug), "meta.json")
}
func (s *store) commentsPath(slug string) string {
	return filepath.Join(s.slugDir(slug), "comments.json")
}
func (s *store) htmlPath(slug string, v int) string {
	return filepath.Join(s.slugDir(slug), "v"+strconv.Itoa(v), "index.html")
}

// exists reports whether a doc directory is present.
func (s *store) exists(slug string) bool {
	fi, err := os.Stat(s.slugDir(slug))
	return err == nil && fi.IsDir()
}

// readMeta loads a doc's meta.json.
func (s *store) readMeta(slug string) (*docMeta, error) {
	b, err := os.ReadFile(s.metaPath(slug))
	if err != nil {
		return nil, err
	}
	var m docMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.metaPath(slug), err)
	}
	return &m, nil
}

// writeMeta persists a doc's meta.json (pretty-printed, matching the bash tools).
func (s *store) writeMeta(slug string, m *docMeta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.metaPath(slug), append(b, '\n'), 0o644)
}

// latestVersion returns the highest version number recorded in meta.json.
func (m *docMeta) latestVersion() int {
	max := 0
	for _, v := range m.Versions {
		if v.N > max {
			max = v.N
		}
	}
	if max == 0 {
		return 1
	}
	return max
}

// sortedVersions returns the version refs ordered by ascending n.
func (m *docMeta) sortedVersions() []versionRef {
	out := make([]versionRef, len(m.Versions))
	copy(out, m.Versions)
	sort.Slice(out, func(i, j int) bool { return out[i].N < out[j].N })
	return out
}

// readComments loads a doc's comments.json as a flat array. A missing or corrupt
// file (non-array) yields an empty slice rather than an error, matching the
// preview server's readCommentFile hardening.
func (s *store) readComments(slug string) ([]comment, error) {
	b, err := os.ReadFile(s.commentsPath(slug))
	if err != nil {
		if os.IsNotExist(err) {
			return []comment{}, nil
		}
		return nil, err
	}
	var list []comment
	if err := json.Unmarshal(b, &list); err != nil {
		return []comment{}, nil // corrupt/non-array → treat as empty
	}
	return list, nil
}

// writeComments persists a doc's comments.json (pretty-printed).
func (s *store) writeComments(slug string, list []comment) error {
	if list == nil {
		list = []comment{}
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.commentsPath(slug), append(b, '\n'), 0o644)
}

// listSlugs returns the doc slugs present in the store, sorted.
func (s *store) listSlugs() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var slugs []string
	for _, e := range entries {
		if e.IsDir() && e.Name()[0] != '.' {
			slugs = append(slugs, e.Name())
		}
	}
	sort.Strings(slugs)
	return slugs, nil
}
