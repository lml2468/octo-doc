package service

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"time"

	"github.com/lml2468/octo-doc/internal/core"
	"github.com/lml2468/octo-doc/internal/platform/apperr"
	"github.com/lml2468/octo-doc/internal/platform/sluglock"
	"github.com/lml2468/octo-doc/internal/storage"
)

// DocService handles publish, render-data, version listing, and deletion of
// documents. Publishing is the critical path: stamp artifacts (byte-equivalent
// to upstream), write the immutable blob, bump the monotonic version list, and
// reconcile/merge comments.
type DocService struct {
	blobs    storage.BlobStore
	meta     storage.MetadataStore
	comments *CommentService
	lock     sluglock.Locker
	baseURL  string
	maxBytes int64
}

// NewDocService constructs a DocService. The locker MUST be the same instance the
// CommentService uses, so that a publish (which holds the slug lock across the
// whole resolve→put→meta→merge sequence) is serialized against comment mutations
// for the same slug.
func NewDocService(blobs storage.BlobStore, meta storage.MetadataStore, comments *CommentService, lock sluglock.Locker, baseURL string, maxBytes int64) *DocService {
	return &DocService{blobs: blobs, meta: meta, comments: comments, lock: lock, baseURL: baseURL, maxBytes: maxBytes}
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

	stamped := core.StampAids(in.HTML)

	// Hold the per-slug lock across the whole critical section: version resolution,
	// the immutable blob write, the version-list bump, and the comment merge must be
	// atomic, or two concurrent publishes of the same slug can resolve to the same
	// version and clobber each other (and drift meta vs blobs).
	var result *PublishResult
	err := s.lock.With(ctx, in.Slug, func() error {
		r, perr := s.publishLocked(ctx, in, stamped)
		result = r
		return perr
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// publishLocked runs the publish critical section. The caller MUST hold the
// per-slug lock (Publish does); it therefore uses PublishMergeLocked and never
// re-acquires the lock.
func (s *DocService) publishLocked(ctx context.Context, in PublishInput, stamped core.StampResult) (*PublishResult, error) {
	version, err := s.resolveVersion(ctx, in.Slug, in.Version)
	if err != nil {
		return nil, err
	}

	size, err := s.blobs.PutDoc(ctx, in.Slug, version, stamped.HTML)
	if err != nil {
		return nil, apperr.Upstream("blob write failed", "blob_write_failed", err)
	}
	if _, ok, herr := s.blobs.HeadDoc(ctx, in.Slug, version); herr != nil {
		return nil, apperr.Upstream("blob head failed", "blob_head_failed", herr)
	} else if !ok {
		return nil, apperr.Upstream("blob write did not persist", "blob_write_lost", nil)
	}

	if err := s.upsertMeta(ctx, in, version); err != nil {
		return nil, err
	}

	merge, err := s.comments.PublishMergeLocked(ctx, in.Slug, in.LocalComments, stamped.AIDs, version)
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

// DraftResult is the result of saving a draft.
type DraftResult struct {
	Slug string `json:"slug"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
	AIDs int    `json:"aids"`
}

// SaveDraft stamps and writes the mutable draft slot for a slug, creating the
// meta record if the slug is new (draft-only docs have an empty Versions list).
// The draft never enters the immutable version numbering until Promote.
func (s *DocService) SaveDraft(ctx context.Context, slug, html, title string) (*DraftResult, error) {
	if html == "" {
		return nil, apperr.Validation("html required", "html_required")
	}
	if int64(len(html)) > s.maxBytes {
		return nil, apperr.PayloadTooLarge(fmt.Sprintf("document exceeds %d bytes", s.maxBytes), "html_too_large")
	}
	stamped := core.StampAids(html)
	var result *DraftResult
	err := s.lock.With(ctx, slug, func() error {
		size, perr := s.blobs.PutDraft(ctx, slug, stamped.HTML)
		if perr != nil {
			return apperr.Upstream("draft write failed", "draft_write_failed", perr)
		}
		if merr := s.setDraftMeta(ctx, slug, title); merr != nil {
			return merr
		}
		result = &DraftResult{
			Slug: slug,
			URL:  fmt.Sprintf("%s/d/%s/draft", s.baseURL, slug),
			Size: size,
			AIDs: len(stamped.AIDs),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// GetDraft fetches the draft HTML + version list for rendering, or nil if absent.
func (s *DocService) GetDraft(ctx context.Context, slug string) (*RenderData, error) {
	html, ok, err := s.blobs.GetDraft(ctx, slug)
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

// Promote turns the current draft into a new immutable version via the normal
// publish path (monotonic maxV+1), then clears the draft blob + meta marker. It
// holds the per-slug lock across the whole sequence so it can't race a publish.
//
// publishLocked is the point of no return: once it succeeds the version is durably
// committed and cannot be rolled back. Clearing the draft afterwards is best-effort
// cleanup — if it fails we log and still return success, because reporting a failure
// would invite a retry that re-runs publishLocked and mints a duplicate version. A
// leftover draft blob is harmless: it's invisible to ListVersions and is overwritten
// by the next SaveDraft.
func (s *DocService) Promote(ctx context.Context, slug, title string) (*PublishResult, error) {
	var result *PublishResult
	err := s.lock.With(ctx, slug, func() error {
		html, ok, gerr := s.blobs.GetDraft(ctx, slug)
		if gerr != nil {
			return gerr
		}
		if !ok {
			return apperr.NotFound("no draft to publish for " + slug)
		}
		stamped := core.StampAids(html)
		r, perr := s.publishLocked(ctx, PublishInput{Slug: slug, HTML: html, Title: title}, stamped)
		if perr != nil {
			return perr
		}
		result = r
		// Best-effort cleanup past the commit point — never fail the promote here.
		if derr := s.blobs.DeleteDraft(ctx, slug); derr != nil {
			slog.Default().Warn("promote: draft blob clear failed (harmless, will be overwritten)",
				"slug", slug, "version", r.Version, "err", derr)
		}
		if merr := s.clearDraftMeta(ctx, slug); merr != nil {
			slog.Default().Warn("promote: draft meta clear failed (harmless)",
				"slug", slug, "version", r.Version, "err", merr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// setDraftMeta records a draft marker in the meta Extra catch-all, creating the
// meta record if the slug is new. It leaves Versions untouched.
func (s *DocService) setDraftMeta(ctx context.Context, slug, title string) error {
	prev, err := s.meta.GetMeta(ctx, slug)
	if err != nil {
		return err
	}
	if prev == nil {
		prev = &storage.DocMeta{Slug: slug, Title: slug, Versions: []storage.VersionRef{}}
	}
	metaTitle := prev.Title
	if title != "" {
		metaTitle = title
	}
	if metaTitle == "" {
		metaTitle = slug
	}
	extra := map[string]any{}
	maps.Copy(extra, prev.Extra)
	extra["draft"] = map[string]any{"updated_at": time.Now().UTC().Format(time.RFC3339)}
	return s.meta.PutMeta(ctx, slug, storage.DocMeta{
		Slug:     slug,
		Title:    metaTitle,
		Versions: prev.Versions,
		Extra:    extra,
	})
}

// clearDraftMeta removes the draft marker from meta (no-op if none / no meta).
func (s *DocService) clearDraftMeta(ctx context.Context, slug string) error {
	prev, err := s.meta.GetMeta(ctx, slug)
	if err != nil || prev == nil {
		return err
	}
	if _, has := prev.Extra["draft"]; !has {
		return nil
	}
	extra := map[string]any{}
	for k, v := range prev.Extra {
		if k != "draft" {
			extra[k] = v
		}
	}
	if len(extra) == 0 {
		extra = nil
	}
	return s.meta.PutMeta(ctx, slug, storage.DocMeta{
		Slug:     prev.Slug,
		Title:    prev.Title,
		Versions: prev.Versions,
		Extra:    extra,
	})
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

// Remove deletes all versions, metadata, and comments for a slug. It holds the
// per-slug lock across all three deletes so it is serialized against a concurrent
// Publish of the same slug (which holds the same lock); otherwise a delete could
// interleave with a publish and leave orphaned blobs or meta pointing at a
// missing blob.
func (s *DocService) Remove(ctx context.Context, slug string) error {
	return s.lock.With(ctx, slug, func() error {
		if err := s.blobs.DeleteDoc(ctx, slug); err != nil {
			return err
		}
		// blobs.DeleteDoc purges asset bytes (they share the doc's key prefix), but
		// the asset metadata rows are a separate store — purge them too, or they'd
		// orphan and resurface if the slug is later reused.
		assets, err := s.meta.ListAssetMeta(ctx, slug)
		if err != nil {
			return err
		}
		for _, a := range assets {
			if derr := s.meta.DeleteAssetMeta(ctx, slug, a.SHA256); derr != nil {
				return derr
			}
		}
		if err := s.meta.DeleteMeta(ctx, slug); err != nil {
			return err
		}
		_, err = s.comments.WipeLocked(ctx, slug)
		return err
	})
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
