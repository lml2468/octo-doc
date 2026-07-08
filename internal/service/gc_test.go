package service_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/lml2468/octo-doc/internal/platform/sluglock"
	"github.com/lml2468/octo-doc/internal/service"
	"github.com/lml2468/octo-doc/internal/storage"
	"github.com/lml2468/octo-doc/internal/storage/memory"
)

// gcFixture wires a shared store with doc + asset services and returns them.
func gcFixture(t *testing.T) (*memory.Store, *service.DocService, *service.AssetService) {
	t.Helper()
	store := memory.New()
	locker := sluglock.NewMemory()
	comments := service.NewCommentService(store, locker)
	docs := service.NewDocService(store, store, comments, locker, "", 5<<20)
	assets := service.NewAssetService(store, store, locker, 1<<20, []string{"image/png"})
	return store, docs, assets
}

// putAged writes an asset (bytes + meta) for doc "d" with an explicit Created
// timestamp so GC grace behavior is testable without waiting.
func putAged(t *testing.T, store *memory.Store, sha string, created time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := store.PutAsset(ctx, "d", sha, []byte("bytes-for-"+sha)); err != nil {
		t.Fatal(err)
	}
	if err := store.PutAssetMeta(ctx, storage.AssetMeta{
		Slug: "d", SHA256: sha, MIME: "image/png", Size: 10, OriginalName: "x.png",
		Created: created.UTC().Format("2006-01-02T15:04:05.000Z"),
	}); err != nil {
		t.Fatal(err)
	}
}

const sha1s = "1111111111111111111111111111111111111111111111111111111111111111"
const sha2s = "2222222222222222222222222222222222222222222222222222222222222222"

func TestGCDeletesUnreferencedAgedAsset(t *testing.T) {
	store, docs, assets := gcFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	// Publish a doc that references NO assets.
	if _, err := docs.Publish(ctx, service.PublishInput{Slug: "d", HTML: "<html><body><p>x</p></body></html>"}); err != nil {
		t.Fatal(err)
	}
	// An old, unreferenced asset → should be deleted.
	putAged(t, store, sha1s, now.Add(-48*time.Hour))

	rep, err := assets.GCAssets(ctx, 24*time.Hour, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Deleted) != 1 || rep.Deleted[0].SHA256 != sha1s {
		t.Fatalf("expected sha1 deleted, got %+v", rep.Deleted)
	}
	if _, _, err := assets.Get(ctx, "d", sha1s); err == nil {
		t.Error("asset still present after GC")
	}
}

func TestGCKeepsReferencedAsset(t *testing.T) {
	store, docs, assets := gcFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	// The published HTML references sha1 via an asset URL.
	html := `<html><body><img src="/d/d/assets/` + sha1s + `"></body></html>`
	if _, err := docs.Publish(ctx, service.PublishInput{Slug: "d", HTML: html}); err != nil {
		t.Fatal(err)
	}
	putAged(t, store, sha1s, now.Add(-48*time.Hour)) // old, but referenced

	rep, err := assets.GCAssets(ctx, 24*time.Hour, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Deleted) != 0 {
		t.Fatalf("referenced asset must be kept, deleted=%+v", rep.Deleted)
	}
	if rep.Referenced != 1 {
		t.Errorf("Referenced = %d; want 1", rep.Referenced)
	}
	if _, _, err := assets.Get(ctx, "d", sha1s); err != nil {
		t.Errorf("referenced asset was removed: %v", err)
	}
}

func TestGCKeepsAssetWithinGrace(t *testing.T) {
	store, docs, assets := gcFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	if _, err := docs.Publish(ctx, service.PublishInput{Slug: "d", HTML: "<html><body><p>x</p></body></html>"}); err != nil {
		t.Fatal(err)
	}
	// Unreferenced but recent (uploaded 1h ago, grace 24h) → kept.
	putAged(t, store, sha1s, now.Add(-1*time.Hour))

	rep, err := assets.GCAssets(ctx, 24*time.Hour, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Deleted) != 0 {
		t.Fatalf("within-grace asset must be kept, deleted=%+v", rep.Deleted)
	}
	if _, _, err := assets.Get(ctx, "d", sha1s); err != nil {
		t.Errorf("within-grace asset removed: %v", err)
	}
}

func TestGCReferencesDraft(t *testing.T) {
	store, docs, assets := gcFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	// Doc exists; the DRAFT (not any published version) references sha1.
	if _, err := docs.Publish(ctx, service.PublishInput{Slug: "d", HTML: "<html><body><p>v1</p></body></html>"}); err != nil {
		t.Fatal(err)
	}
	draft := `<html><body><img src="/d/d/assets/` + sha1s + `"></body></html>`
	if _, err := docs.SaveDraft(ctx, "d", draft, "T"); err != nil {
		t.Fatal(err)
	}
	putAged(t, store, sha1s, now.Add(-48*time.Hour))

	rep, err := assets.GCAssets(ctx, 24*time.Hour, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Deleted) != 0 {
		t.Fatalf("draft-referenced asset must be kept, deleted=%+v", rep.Deleted)
	}
}

func TestGCDryRunDeletesNothing(t *testing.T) {
	store, docs, assets := gcFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	if _, err := docs.Publish(ctx, service.PublishInput{Slug: "d", HTML: "<html><body><p>x</p></body></html>"}); err != nil {
		t.Fatal(err)
	}
	putAged(t, store, sha1s, now.Add(-48*time.Hour))
	putAged(t, store, sha2s, now.Add(-48*time.Hour))

	rep, err := assets.GCAssets(ctx, 24*time.Hour, now, true) // dry-run
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Deleted) != 2 {
		t.Fatalf("dry-run should report 2 deletions, got %d", len(rep.Deleted))
	}
	// Both still present.
	if _, _, err := assets.Get(ctx, "d", sha1s); err != nil {
		t.Error("dry-run deleted sha1")
	}
	if _, _, err := assets.Get(ctx, "d", sha2s); err != nil {
		t.Error("dry-run deleted sha2")
	}
}

func TestGCMixedReferenceAndDraftBytesRoundTrip(t *testing.T) {
	// Guard: uploading via Put (normal path) and referencing it keeps it.
	_, docs, assets := gcFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	res, err := assets.Put(ctx, "d", bytes.NewReader([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}), "a.png")
	if err != nil {
		t.Fatal(err)
	}
	html := `<html><body><img src="/d/d/assets/` + res.SHA256 + `"></body></html>`
	if _, err := docs.Publish(ctx, service.PublishInput{Slug: "d", HTML: html}); err != nil {
		t.Fatal(err)
	}
	// Even with zero grace, a referenced fresh upload survives.
	rep, err := assets.GCAssets(ctx, 0, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Deleted) != 0 {
		t.Fatalf("referenced upload deleted: %+v", rep.Deleted)
	}
}
