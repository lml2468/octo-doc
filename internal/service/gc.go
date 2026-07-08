package service

import (
	"context"
	"regexp"
	"time"

	"github.com/lml2468/octo-doc/internal/storage"
)

// Orphan asset garbage collection (P2.4). Content-addressed assets can outlive the
// HTML that referenced them — an author uploads one and never references it, or
// edits the reference away. GCAssets finds assets no live HTML references and, if
// they are older than a grace period, deletes them. See docs/ASSETS.md (P2.4).

// assetRefRe matches an asset reference in HTML: the ".../assets/<sha256>" tail of
// a same-origin URL. Anchored on the "assets/" segment so it matches regardless of
// host/scheme, and requires exactly 64 hex chars (the content address). A trailing
// [0-9a-f] boundary is enforced by the fixed length + the surrounding class check.
var assetRefRe = regexp.MustCompile(`assets/([0-9a-f]{64})`)

// GCReport summarizes a garbage-collection pass.
type GCReport struct {
	Scanned    int        // docs scanned
	Assets     int        // asset records examined
	Deleted    []GCDelete // assets removed (or that would be, in dry-run)
	Kept       int        // assets kept (referenced or within grace)
	Referenced int        // assets kept specifically because they are referenced
}

// GCDelete identifies one removed asset.
type GCDelete struct {
	Slug   string
	SHA256 string
	Size   int64
	Reason string // "unreferenced"
}

// GCAssets scans every doc's published versions and current draft for asset
// references, then deletes assets that are BOTH unreferenced AND older than
// `grace` (measured from the asset's Created timestamp against `now`). Assets
// within the grace window are kept even if unreferenced, so an upload that hasn't
// yet been wired into a draft isn't reaped mid-authoring. In dryRun, nothing is
// deleted but the report lists what would be. `now` is injected for testability.
func (s *AssetService) GCAssets(ctx context.Context, grace time.Duration, now time.Time, dryRun bool) (GCReport, error) {
	docs, err := s.meta.ListMeta(ctx)
	if err != nil {
		return GCReport{}, err
	}
	var report GCReport
	for _, entry := range docs {
		slug := entry.Slug
		report.Scanned++

		referenced, err := s.referencedSHAs(ctx, slug, entry.Meta)
		if err != nil {
			return GCReport{}, err
		}

		assets, err := s.meta.ListAssetMeta(ctx, slug)
		if err != nil {
			return GCReport{}, err
		}
		for _, a := range assets {
			report.Assets++
			if _, ok := referenced[a.SHA256]; ok {
				report.Kept++
				report.Referenced++
				continue
			}
			// Unreferenced: keep if still within the grace window (uploaded but not
			// yet wired in). A Created that won't parse is treated as "old enough".
			if within, ok := withinGrace(a.Created, now, grace); ok && within {
				report.Kept++
				continue
			}
			report.Deleted = append(report.Deleted, GCDelete{
				Slug: slug, SHA256: a.SHA256, Size: a.Size, Reason: "unreferenced",
			})
			if !dryRun {
				if derr := s.Delete(ctx, slug, a.SHA256); derr != nil {
					return report, derr
				}
			}
		}
	}
	return report, nil
}

// referencedSHAs collects every asset sha referenced by a doc's live HTML: all
// published versions plus the current draft.
func (s *AssetService) referencedSHAs(ctx context.Context, slug string, meta storage.DocMeta) (map[string]struct{}, error) {
	refs := map[string]struct{}{}
	scan := func(html string) {
		for _, m := range assetRefRe.FindAllStringSubmatch(html, -1) {
			refs[m[1]] = struct{}{}
		}
	}
	for _, v := range meta.Versions {
		html, ok, err := s.blobs.GetDoc(ctx, slug, v.N)
		if err != nil {
			return nil, err
		}
		if ok {
			scan(html)
		}
	}
	if html, ok, err := s.blobs.GetDraft(ctx, slug); err != nil {
		return nil, err
	} else if ok {
		scan(html)
	}
	return refs, nil
}

// withinGrace reports whether an asset created at `created` (RFC3339-ish, as
// nowISO writes) is still inside the grace window ending at `now`. The second
// return is false when the timestamp can't be parsed, letting the caller treat an
// unparseable timestamp as past-grace (eligible for deletion).
func withinGrace(created string, now time.Time, grace time.Duration) (within bool, parsed bool) {
	t, err := time.Parse("2006-01-02T15:04:05.000Z", created)
	if err != nil {
		// Try RFC3339 as a fallback for any differently-formatted timestamps.
		t, err = time.Parse(time.RFC3339, created)
		if err != nil {
			return false, false
		}
	}
	return now.Sub(t) < grace, true
}
