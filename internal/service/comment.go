package service

import (
	"context"
	"time"

	"github.com/Mininglamp-OSS/octo-doc/internal/core"
	"github.com/Mininglamp-OSS/octo-doc/internal/platform/sluglock"
	"github.com/Mininglamp-OSS/octo-doc/internal/storage"
)

// CommentService is the serialized owner of per-slug comment mutations. All
// writes for a slug run under a per-slug lock, making read→apply→write atomic
// (the role the Cloudflare Durable Object played). Reads fold the stored log.
type CommentService struct {
	meta storage.MetadataStore
	lock sluglock.Locker
}

// NewCommentService constructs a CommentService.
func NewCommentService(meta storage.MetadataStore, lock sluglock.Locker) *CommentService {
	return &CommentService{meta: meta, lock: lock}
}

// MutationResult is the HTTP-shaped result of a serialized comment mutation.
type MutationResult struct {
	Status int
	Body   any
}

func nowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// List folds a slug's comments to a version snapshot, or the full history when
// version is core.VersionLatest.
func (s *CommentService) List(ctx context.Context, slug string, version int) ([]core.CommentSnapshot, error) {
	list, err := s.read(ctx, slug)
	if err != nil {
		return nil, err
	}
	if version == core.VersionLatest {
		return core.HistoryList(list), nil
	}
	return core.SnapshotList(list, version), nil
}

// Read returns the migrated raw comment list for a slug (callers fold it).
func (s *CommentService) Read(ctx context.Context, slug string) ([]core.Comment, error) {
	return s.read(ctx, slug)
}

func (s *CommentService) read(ctx context.Context, slug string) ([]core.Comment, error) {
	list, err := s.meta.GetComments(ctx, slug)
	if err != nil {
		return nil, err
	}
	core.EnsureMigrated(list)
	return list, nil
}

// Create adds a top-level comment.
func (s *CommentService) Create(ctx context.Context, slug string, author *core.Author, text string, anchor *core.Anchor, version int) (MutationResult, error) {
	now := nowISO()
	return s.mutate(ctx, slug, core.CommentOp{
		Kind: "create", ID: newCommentID(now), Author: author, Text: text, Anchor: anchor, Version: version, At: now,
	})
}

// Reply adds a reply to a parent comment.
func (s *CommentService) Reply(ctx context.Context, slug, parentID string, author *core.Author, text string, version int) (MutationResult, error) {
	now := nowISO()
	return s.mutate(ctx, slug, core.CommentOp{
		Kind: "reply", ParentID: parentID, ReplyID: newReplyID(now), Author: author, Text: text, Version: version, At: now,
	})
}

// React toggles an emoji reaction on a comment or reply.
func (s *CommentService) React(ctx context.Context, slug, commentID, emoji, by string, version int) (MutationResult, error) {
	return s.mutate(ctx, slug, core.CommentOp{
		Kind: "react", CommentID: commentID, Emoji: emoji, By: by, Version: version, At: nowISO(),
	})
}

// Reanchor re-anchors a comment (resetting its agent verdict).
func (s *CommentService) Reanchor(ctx context.Context, slug, id string, anchor *core.Anchor, version int, actor string) (MutationResult, error) {
	return s.mutate(ctx, slug, core.CommentOp{
		Kind: "patch_anchor", ID: id, Anchor: anchor, ResetStatus: true, Version: version, Actor: actor, At: nowISO(),
	})
}

// Remove soft-deletes a comment or reply at a version.
func (s *CommentService) Remove(ctx context.Context, slug, id string, version int, actor string) (MutationResult, error) {
	return s.mutate(ctx, slug, core.CommentOp{
		Kind: "delete", ID: id, Version: version, Actor: actor, At: nowISO(),
	})
}

// AppendRaw appends pre-built events to a comment (agent reply path).
func (s *CommentService) AppendRaw(ctx context.Context, slug, id string, events []core.CommentEvent, responseBody any) (MutationResult, error) {
	return s.mutate(ctx, slug, core.CommentOp{
		Kind: "raw_events", ID: id, Events: events, ResponseBody: responseBody, At: nowISO(),
	})
}

// Wipe removes all comments for a slug.
func (s *CommentService) Wipe(ctx context.Context, slug string) (MutationResult, error) {
	return s.mutate(ctx, slug, core.CommentOp{Kind: "wipe", At: nowISO()})
}

// PublishMerge performs the publish-time non-destructive merge + anchor reconcile.
func (s *CommentService) PublishMerge(ctx context.Context, slug string, local []core.Comment, aids []core.StampedArtifact, version int) (MutationResult, error) {
	return s.mutate(ctx, slug, core.CommentOp{
		Kind: "publish_merge", LocalComments: local, AIDs: aids, Version: version, At: nowISO(),
	})
}

// mutate runs a comment op under the per-slug lock, persisting on success.
func (s *CommentService) mutate(ctx context.Context, slug string, op core.CommentOp) (MutationResult, error) {
	var res MutationResult
	err := s.lock.With(ctx, slug, func() error {
		list, lerr := s.meta.GetComments(ctx, slug)
		if lerr != nil {
			return lerr
		}
		newList, opRes := core.ApplyCommentOp(list, op)
		if opRes.Status == 200 {
			if opRes.Wipe {
				if derr := s.meta.DeleteComments(ctx, slug); derr != nil {
					return derr
				}
			} else if perr := s.meta.PutComments(ctx, slug, newList); perr != nil {
				return perr
			}
		}
		res = MutationResult{Status: opRes.Status, Body: opRes.Body}
		return nil
	})
	return res, err
}
