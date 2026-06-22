package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Mininglamp-OSS/octo-doc/internal/core"
	"github.com/Mininglamp-OSS/octo-doc/internal/platform/apperr"
	"github.com/Mininglamp-OSS/octo-doc/internal/storage"
)

// DocService handles publish, render-data, version listing, and deletion of
// documents. Publishing is the critical path: stamp artifacts (byte-equivalent
// to upstream), write the immutable blob, bump the monotonic version list, and
// reconcile/merge comments.
type DocService struct {
	blobs    storage.BlobStore
	meta     storage.MetadataStore
	comments *CommentService
	baseURL  string
	maxBytes int64
}

// NewDocService constructs a DocService.
func NewDocService(blobs storage.BlobStore, meta storage.MetadataStore, comments *CommentService, baseURL string, maxBytes int64) *DocService {
	return &DocService{blobs: blobs, meta: meta, comments: comments, baseURL: baseURL, maxBytes: maxBytes}
}

// PublishInput is the input to Publish.
type PublishInput struct {
	Slug          string
	HTML          string
	Version       int // 0 = auto-increment
	Title         string
	LocalComments []core.Comment
}

// PublishResult is the result of a successful publish.
type PublishResult struct {
	Slug           string `json:"slug"`
	Version        int    `json:"version"`
	URL            string `json:"url"`
	Size           int64  `json:"size"`
	AIDs           int    `json:"aids"`
	MergedComments int    `json:"merged_comments"`
}

// RenderData is the render payload for a document version.
type RenderData struct {
	HTML     string
	Versions []storage.VersionRef
}

// Publish publishes a new (or explicitly-versioned) document.
func (s *DocService) Publish(ctx context.Context, in PublishInput) (*PublishResult, error) {
	if in.HTML == "" {
		return nil, apperr.Validation("html (file) required", "html_required")
	}
	if int64(len(in.HTML)) > s.maxBytes {
		return nil, apperr.PayloadTooLarge(fmt.Sprintf("document exceeds %d bytes", s.maxBytes), "html_too_large")
	}

	version, err := s.resolveVersion(ctx, in.Slug, in.Version)
	if err != nil {
		return nil, err
	}

	stamped := core.StampAids(in.HTML)

	size, err := s.blobs.PutDoc(ctx, in.Slug, version, stamped.HTML)
	if err != nil {
		return nil, apperr.Upstream("blob write failed", "blob_write_failed", err)
	}
	if _, ok, herr := s.blobs.HeadDoc(ctx, in.Slug, version); herr != nil || !ok {
		return nil, apperr.Validation("blob write did not persist", "blob_write_lost")
	}

	if err := s.upsertMeta(ctx, in, version); err != nil {
		return nil, err
	}

	merge, err := s.comments.PublishMerge(ctx, in.Slug, in.LocalComments, stamped.AIDs, version)
	if err != nil {
		return nil, err
	}
	merged := 0
	if body, ok := merge.Body.(map[string]any); ok {
		if m, ok := body["mergedComments"].(int); ok {
			merged = m
		}
	}

	return &PublishResult{
		Slug:           in.Slug,
		Version:        version,
		URL:            fmt.Sprintf("%s/d/%s/v/%d", s.baseURL, in.Slug, version),
		Size:           size,
		AIDs:           len(stamped.AIDs),
		MergedComments: merged,
	}, nil
}

// Render fetches stored HTML + the version list for rendering, or nil if absent.
func (s *DocService) Render(ctx context.Context, slug string, version int) (*RenderData, error) {
	html, ok, err := s.blobs.GetDoc(ctx, slug, version)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	meta, err := s.meta.GetMeta(ctx, slug)
	if err != nil {
		return nil, err
	}
	var versions []storage.VersionRef
	if meta != nil {
		versions = meta.Versions
	}
	return &RenderData{HTML: html, Versions: versions}, nil
}

// VersionList is the response of ListVersions.
type VersionList struct {
	Slug     string               `json:"slug"`
	Title    string               `json:"title"`
	Versions []storage.VersionRef `json:"versions"`
}

// ListVersions lists versions for a slug (meta-derived, falling back to blobs).
func (s *DocService) ListVersions(ctx context.Context, slug string) (*VersionList, error) {
	meta, err := s.meta.GetMeta(ctx, slug)
	if err != nil {
		return nil, err
	}
	blobVersions, err := s.blobs.ListVersions(ctx, slug)
	if err != nil {
		return nil, err
	}
	if meta == nil && len(blobVersions) == 0 {
		return nil, nil
	}
	title := slug
	var versions []storage.VersionRef
	if meta != nil && len(meta.Versions) > 0 {
		versions = meta.Versions
		if meta.Title != "" {
			title = meta.Title
		}
	} else {
		for _, n := range blobVersions {
			versions = append(versions, storage.VersionRef{N: n})
		}
	}
	return &VersionList{Slug: slug, Title: title, Versions: versions}, nil
}

// Remove deletes all versions, metadata, and comments for a slug.
func (s *DocService) Remove(ctx context.Context, slug string) error {
	if err := s.blobs.DeleteDoc(ctx, slug); err != nil {
		return err
	}
	if err := s.meta.DeleteMeta(ctx, slug); err != nil {
		return err
	}
	_, err := s.comments.Wipe(ctx, slug)
	return err
}

// OwnerDoc is one row in the owner catalog.
type OwnerDoc struct {
	Slug   string
	Title  string
	Latest int
}

// ListAllForOwner lists all docs with a reachable latest version.
func (s *DocService) ListAllForOwner(ctx context.Context) ([]OwnerDoc, error) {
	all, err := s.meta.ListMeta(ctx)
	if err != nil {
		return nil, err
	}
	var out []OwnerDoc
	for _, e := range all {
		latest := 1
		if n := len(e.Meta.Versions); n > 0 {
			latest = e.Meta.Versions[n-1].N
		}
		_, ok, herr := s.blobs.HeadDoc(ctx, e.Slug, latest)
		if herr != nil || !ok {
			continue
		}
		title := e.Meta.Title
		if title == "" {
			title = e.Slug
		}
		out = append(out, OwnerDoc{Slug: e.Slug, Title: title, Latest: latest})
	}
	return out, nil
}

func (s *DocService) resolveVersion(ctx context.Context, slug string, explicit int) (int, error) {
	if explicit > 0 {
		return explicit, nil
	}
	existing, err := s.blobs.ListVersions(ctx, slug)
	if err != nil {
		return 0, err
	}
	maxV := 0
	for _, n := range existing {
		if n > maxV {
			maxV = n
		}
	}
	return maxV + 1, nil
}

func (s *DocService) upsertMeta(ctx context.Context, in PublishInput, version int) error {
	prev, err := s.meta.GetMeta(ctx, in.Slug)
	if err != nil {
		return err
	}
	if prev == nil {
		prev = &storage.DocMeta{Slug: in.Slug, Title: in.Slug, Versions: []storage.VersionRef{}}
	}
	versions := append([]storage.VersionRef{}, prev.Versions...)
	found := false
	for _, v := range versions {
		if v.N == version {
			found = true
			break
		}
	}
	if !found {
		created := time.Now().UTC().Format(time.RFC3339)
		versions = append(versions, storage.VersionRef{N: version, Created: &created})
	}
	sortVersions(versions)

	title := prev.Title
	if in.Title != "" {
		title = in.Title
	}
	if title == "" {
		title = in.Slug
	}
	return s.meta.PutMeta(ctx, in.Slug, storage.DocMeta{
		Slug:     in.Slug,
		Title:    title,
		Versions: versions,
		Extra:    prev.Extra,
	})
}

func sortVersions(v []storage.VersionRef) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j-1].N > v[j].N; j-- {
			v[j-1], v[j] = v[j], v[j-1]
		}
	}
}
