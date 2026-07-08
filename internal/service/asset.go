// Package service — AssetService owns media asset uploads: it validates size and
// MIME, content-addresses the bytes by SHA-256, and persists blob + registry
// under a per-slug lock. See docs/ASSETS.md.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/lml2468/octo-doc/internal/platform/apperr"
	"github.com/lml2468/octo-doc/internal/platform/sluglock"
	"github.com/lml2468/octo-doc/internal/storage"
)

// AssetService stores and serves per-doc media assets. Uploads run under the same
// per-slug lock as document and comment mutations so a concurrent doc delete
// cannot interleave with an asset write for the same slug.
type AssetService struct {
	blobs     storage.BlobStore
	meta      storage.MetadataStore
	lock      sluglock.Locker
	maxBytes  int64
	mimeAllow []string
}

// NewAssetService constructs an AssetService. The locker MUST be the same instance
// the other services use, so per-slug operations serialize across the app.
func NewAssetService(blobs storage.BlobStore, meta storage.MetadataStore, lock sluglock.Locker, maxBytes int64, mimeAllow []string) *AssetService {
	return &AssetService{blobs: blobs, meta: meta, lock: lock, maxBytes: maxBytes, mimeAllow: mimeAllow}
}

// AssetResult is the outcome of a successful upload.
type AssetResult struct {
	SHA256 string `json:"sha256"`
	MIME   string `json:"mime"`
	Size   int64  `json:"size"`
}

// Put reads r (bounded by the configured cap), sniffs and validates the MIME
// type, content-addresses the bytes, and persists blob + registry. It is
// idempotent: uploading identical bytes twice returns the same SHA-256 and does
// not error. The sniffed MIME is authoritative — a client-declared type is never
// trusted.
func (s *AssetService) Put(ctx context.Context, slug string, r io.Reader, originalName string) (AssetResult, error) {
	// Read up to maxBytes+1 so we can distinguish "exactly at cap" from "over cap".
	data, err := io.ReadAll(io.LimitReader(r, s.maxBytes+1))
	if err != nil {
		return AssetResult{}, apperr.Upstream("read upload failed", "asset_read_failed", err)
	}
	if int64(len(data)) > s.maxBytes {
		return AssetResult{}, apperr.PayloadTooLarge("asset exceeds size limit", "asset_too_large").
			WithDetails(map[string]any{"max_bytes": s.maxBytes})
	}
	if len(data) == 0 {
		return AssetResult{}, apperr.Validation("empty asset", "asset_empty")
	}

	mime := sniffMIME(data)
	if !slices.Contains(s.mimeAllow, mime) {
		return AssetResult{}, apperr.Validation("unsupported media type: "+mime, "unsupported_media_type").
			WithDetails(map[string]any{"detected": mime})
	}

	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])

	res := AssetResult{SHA256: sha, MIME: mime, Size: int64(len(data))}
	err = s.lock.With(ctx, slug, func() error {
		if perr := s.blobs.PutAsset(ctx, slug, sha, data); perr != nil {
			return apperr.Upstream("asset blob write failed", "asset_write_failed", perr)
		}
		return s.meta.PutAssetMeta(ctx, storage.AssetMeta{
			Slug: slug, SHA256: sha, MIME: mime, Size: int64(len(data)),
			OriginalName: originalName, Created: nowISO(),
		})
	})
	if err != nil {
		return AssetResult{}, err
	}
	return res, nil
}

// Get returns an asset's bytes and metadata, or a 404 apperr if absent.
func (s *AssetService) Get(ctx context.Context, slug, sha string) ([]byte, storage.AssetMeta, error) {
	meta, err := s.meta.GetAssetMeta(ctx, slug, sha)
	if err != nil {
		return nil, storage.AssetMeta{}, err
	}
	if meta == nil {
		return nil, storage.AssetMeta{}, apperr.NotFound("no such asset")
	}
	data, ok, err := s.blobs.GetAsset(ctx, slug, sha)
	if err != nil {
		return nil, storage.AssetMeta{}, apperr.Upstream("asset blob read failed", "asset_read_failed", err)
	}
	if !ok {
		return nil, storage.AssetMeta{}, apperr.NotFound("no such asset")
	}
	return data, *meta, nil
}

// List returns a slug's asset registry (metadata only).
func (s *AssetService) List(ctx context.Context, slug string) ([]storage.AssetMeta, error) {
	return s.meta.ListAssetMeta(ctx, slug)
}

// Delete removes an asset's blob and registry entry under the per-slug lock. A
// missing asset is not an error (idempotent delete).
func (s *AssetService) Delete(ctx context.Context, slug, sha string) error {
	return s.lock.With(ctx, slug, func() error {
		if err := s.blobs.DeleteAsset(ctx, slug, sha); err != nil {
			return apperr.Upstream("asset blob delete failed", "asset_delete_failed", err)
		}
		return s.meta.DeleteAssetMeta(ctx, slug, sha)
	})
}

// sniffMIME determines the content type from the bytes. http.DetectContentType
// returns a charset-parameterized type for text (e.g. "image/svg+xml" is served
// as text) — normalize by dropping any parameter so it matches the allowlist.
func sniffMIME(data []byte) string {
	ct := http.DetectContentType(data)
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct
}
