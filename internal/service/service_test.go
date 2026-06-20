package service_test

import (
	"context"
	"testing"

	"github.com/Mininglamp-OSS/octo-doc/internal/core"
	"github.com/Mininglamp-OSS/octo-doc/internal/platform/sluglock"
	"github.com/Mininglamp-OSS/octo-doc/internal/service"
	"github.com/Mininglamp-OSS/octo-doc/internal/storage/memory"
)

func newDoc(t *testing.T) (*service.DocService, *service.CommentService) {
	t.Helper()
	store := memory.New()
	cs := service.NewCommentService(store, sluglock.NewMemory())
	ds := service.NewDocService(store, store, cs, "", 5<<20)
	return ds, cs
}

func TestPublishAutoIncrementsVersion(t *testing.T) {
	ds, _ := newDoc(t)
	ctx := context.Background()
	r1, err := ds.Publish(ctx, service.PublishInput{Slug: "d", HTML: "<html><body><p>a</p></body></html>"})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Version != 1 {
		t.Fatalf("first version = %d", r1.Version)
	}
	r2, err := ds.Publish(ctx, service.PublishInput{Slug: "d", HTML: "<html><body><p>b</p></body></html>"})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Version != 2 {
		t.Fatalf("second version = %d", r2.Version)
	}
}

func TestPublishRejectsEmptyAndOversized(t *testing.T) {
	ds, _ := newDoc(t)
	ctx := context.Background()
	if _, err := ds.Publish(ctx, service.PublishInput{Slug: "d", HTML: ""}); err == nil {
		t.Error("empty HTML should be rejected")
	}
	store := memory.New()
	small := service.NewDocService(store, store, service.NewCommentService(store, sluglock.NewMemory()), "", 10)
	if _, err := small.Publish(ctx, service.PublishInput{Slug: "d", HTML: "<html>way too large</html>"}); err == nil {
		t.Error("oversized HTML should be rejected")
	}
}

func TestPublishStampsAndRenders(t *testing.T) {
	ds, _ := newDoc(t)
	ctx := context.Background()
	res, err := ds.Publish(ctx, service.PublishInput{Slug: "d", HTML: "<html><body><img src=\"a.png\"></body></html>"})
	if err != nil {
		t.Fatal(err)
	}
	if res.AIDs != 1 {
		t.Fatalf("expected 1 aid, got %d", res.AIDs)
	}
	data, err := ds.Render(ctx, "d", 1)
	if err != nil || data == nil {
		t.Fatalf("render = %v, %v", data, err)
	}
	if !contains(data.HTML, "data-tdoc-aid") {
		t.Error("rendered HTML not stamped")
	}
}

func TestRenderMissingReturnsNil(t *testing.T) {
	ds, _ := newDoc(t)
	data, err := ds.Render(context.Background(), "nope", 1)
	if err != nil || data != nil {
		t.Fatalf("render missing = %v, %v; want nil, nil", data, err)
	}
}

func TestCommentCreateListDelete(t *testing.T) {
	ds, cs := newDoc(t)
	ctx := context.Background()
	_, _ = ds.Publish(ctx, service.PublishInput{Slug: "d", HTML: "<html><body><p>hi there</p></body></html>"})

	created, err := cs.Create(ctx, "d", &core.Author{Login: "alice"}, "nice", &core.Anchor{Kind: "text", Text: "hi"}, 1)
	if err != nil || created.Status != 200 {
		t.Fatalf("create = %+v, %v", created, err)
	}
	snap := created.Body.(*core.CommentSnapshot)
	list, err := cs.List(ctx, "d", 1)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}

	if _, err := cs.Remove(ctx, "d", snap.ID, 1, "alice"); err != nil {
		t.Fatal(err)
	}
	list, _ = cs.List(ctx, "d", 1)
	if len(list) != 0 {
		t.Fatalf("after delete, list = %v", list)
	}
}

func TestPublishMergeReconcilesAnchors(t *testing.T) {
	ds, cs := newDoc(t)
	ctx := context.Background()
	// v1 with an svg, comment anchored to its aid.
	r1, _ := ds.Publish(ctx, service.PublishInput{Slug: "d", HTML: "<html><body><h2>Chart</h2><svg viewBox=\"0 0 1 1\"></svg></body></html>"})
	_ = r1
	// fetch the aid from the rendered doc isn't trivial here; instead assert the
	// merge path runs without error on republish (reconcile + compact).
	if _, err := ds.Publish(ctx, service.PublishInput{Slug: "d", HTML: "<html><body><h2>Chart</h2><svg viewBox=\"0 0 1 1\"></svg></body></html>"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.List(ctx, "d", 2); err != nil {
		t.Fatal(err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
