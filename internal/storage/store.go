// Package storage defines the persistence contracts — MetadataStore for small
// structured records and BlobStore for immutable documents — plus a hash-based
// key helper shared by blob backends. The service layer depends only on these
// interfaces; no driver type (pgx row, S3 object) ever escapes an implementation.
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/Mininglamp-OSS/octo-doc/internal/core"
)

// DocMeta is the metadata persisted per slug.
type DocMeta struct {
	Slug     string       `json:"slug"`
	Title    string       `json:"title"`
	Versions []VersionRef `json:"versions"`
	// Extra holds any additional caller-supplied metadata fields.
	Extra map[string]any `json:"-"`
}

// VersionRef is one published version of a document.
type VersionRef struct {
	N       int     `json:"n"`
	Created *string `json:"created,omitempty"`
}

// Session is a signed-in viewer session.
type Session struct {
	Login     string  `json:"login"`
	Name      string  `json:"name,omitempty"`
	AvatarURL *string `json:"avatar_url,omitempty"`
	Created   string  `json:"created"`
}

// TokenRecord is a provisioned write token.
type TokenRecord struct {
	Token   string `json:"token"`
	Created string `json:"created"`
	Label   string `json:"label,omitempty"`
}

// MetaEntry pairs a slug with its metadata for listing.
type MetaEntry struct {
	Slug string
	Meta DocMeta
}

// MetadataStore persists doc metadata, comment logs, sessions, and write tokens.
// Implementations return plain Go values, never driver row types.
type MetadataStore interface {
	GetMeta(ctx context.Context, slug string) (*DocMeta, error)
	PutMeta(ctx context.Context, slug string, meta DocMeta) error
	DeleteMeta(ctx context.Context, slug string) error
	ListMeta(ctx context.Context) ([]MetaEntry, error)

	// GetComments always returns a slice; corrupt/absent values yield an empty one.
	GetComments(ctx context.Context, slug string) ([]core.Comment, error)
	PutComments(ctx context.Context, slug string, list []core.Comment) error
	DeleteComments(ctx context.Context, slug string) error

	GetSession(ctx context.Context, sid string) (*Session, error)
	PutSession(ctx context.Context, sid string, data Session, ttlSeconds int) error
	DeleteSession(ctx context.Context, sid string) error

	GetToken(ctx context.Context, token string) (*TokenRecord, error)
	PutToken(ctx context.Context, token string, rec TokenRecord) error
	AnyToken(ctx context.Context) (bool, error)

	// Health verifies the backend is reachable (readiness probe).
	Health(ctx context.Context) error

	Close() error
}

// BlobStore persists immutable HTML documents keyed by (slug, version).
type BlobStore interface {
	// PutDoc writes a version atomically (no half-writes) and returns its size.
	PutDoc(ctx context.Context, slug string, version int, html string) (size int64, err error)
	GetDoc(ctx context.Context, slug string, version int) (string, bool, error)
	HeadDoc(ctx context.Context, slug string, version int) (size int64, exists bool, err error)
	// ListVersions returns the versions present for a slug, ascending.
	ListVersions(ctx context.Context, slug string) ([]int, error)
	DeleteDoc(ctx context.Context, slug string) error

	// Draft is a single mutable, overwritable slot per slug, stored outside the
	// versioned key namespace so it never appears in ListVersions. It holds the
	// work-in-progress HTML before it is promoted to an immutable version.
	PutDraft(ctx context.Context, slug string, html string) (size int64, err error)
	GetDraft(ctx context.Context, slug string) (string, bool, error)
	DeleteDraft(ctx context.Context, slug string) error

	// Health verifies the backend is reachable (readiness probe).
	Health(ctx context.Context) error
}

// HashSlug hashes a slug to a fixed-length hex key safe as a path/prefix segment.
// Defined once so every blob backend shares the path-traversal defense.
func HashSlug(slug string) string {
	sum := sha256.Sum256([]byte(slug))
	return hex.EncodeToString(sum[:])[:32]
}
