package service

import (
	"context"
	"regexp"
	"time"
)

// Orphan asset garbage collection (P2.4). Content-addressed assets can outlive the
// HTML that referenced them — an author uploads one and never references it, or
// edits the reference away. GCAssets finds assets no live HTML references and, if
// they are older than a grace period, deletes them. See docs/ASSETS.md (P2.4).

// shaRe matches a bare 64-hex content address anywhere in HTML. It is deliberately
// NOT anchored on an "assets/<sha>" URL shape: references are collected GLOBALLY
// across every doc, so an asset stays alive whether it is referenced by a normal
// same-origin URL, by a fork/other doc that copied that URL (#44), or by JS that
// builds the URL from a bare sha string held in a variable or data island (#44).
// FindAllString is non-overlapping, so a longer hex run yields extra 64-char
// tokens — harmless, since over-retaining an asset is the safe failure direction.
var shaRe = regexp.MustCompile(`[0-9a-f]{64}`)

// GCReport summarizes a garbage-collection pass.
type GCReport struct {
	Scanned    int        // slugs scanned for assets
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

// GCAssets deletes assets that are BOTH unreferenced by any live HTML AND older
// than `grace` (measured from the asset's Created timestamp against `now`). Assets
// within the grace window are kept even if unreferenced, so an upload not yet
// wired into a draft isn't reaped mid-authoring. In dryRun, nothing is deleted but
// the report lists what would be. `now` is injected for testability.
//
// References are resolved GLOBALLY — across every doc's published versions and
// draft — so a content address reused across docs (e.g. a fork keeping the source
// slug's URL) keeps the asset alive (#44). Asset-bearing slugs are enumerated via
// ListAssetSlugs, not ListMeta, so assets under a slug with no doc row are still
// collected (#45). Each slug's delete decision runs under that slug's lock, with a
// fresh re-scan of the slug's own current HTML inside the lock, so a concurrent
// same-slug publish/version-add that re-references an asset is not clobbered (#46).
func (s *AssetService) GCAssets(ctx context.Context, grace time.Duration, now time.Time, dryRun bool) (GCReport, error) {
	// Phase 1: global reference set from a snapshot of all docs' HTML.
	referenced, err := s.allReferencedSHAs(ctx)
	if err != nil {
		return GCReport{}, err
	}

	// Phase 2: per asset-bearing slug, decide + delete under the slug lock.
	slugs, err := s.meta.ListAssetSlugs(ctx)
	if err != nil {
		return GCReport{}, err
	}
	var report GCReport
	for _, slug := range slugs {
		report.Scanned++
		if err := s.gcSlug(ctx, slug, referenced, grace, now, dryRun, &report); err != nil {
			return report, err
		}
	}
	return report, nil
}

// gcSlug garbage-collects one slug's assets under that slug's lock. It re-scans the
// slug's own current HTML inside the lock and merges those references with the
// global snapshot, so a same-slug publish that landed after the phase-1 snapshot
// cannot cause a just-referenced asset to be deleted (#46).
func (s *AssetService) gcSlug(ctx context.Context, slug string, globalRefs map[string]struct{}, grace time.Duration, now time.Time, dryRun bool, report *GCReport) error {
	return s.lock.With(ctx, slug, func() error {
		fresh, err := s.slugReferencedSHAs(ctx, slug)
		if err != nil {
			return err
		}
		assets, err := s.meta.ListAssetMeta(ctx, slug)
		if err != nil {
			return err
		}
		for _, a := range assets {
			report.Assets++
			_, refGlobal := globalRefs[a.SHA256]
			_, refFresh := fresh[a.SHA256]
			if refGlobal || refFresh {
				report.Kept++
				report.Referenced++
				continue
			}
			// Unreferenced: keep if still within the grace window (uploaded but not
			// yet wired in). Fail SAFE — if the timestamp can't be parsed, keep the
			// asset rather than delete it (deletion is irreversible).
			within, parsed := withinGrace(a.Created, now, grace)
			if !parsed || within {
				report.Kept++
				continue
			}
			report.Deleted = append(report.Deleted, GCDelete{
				Slug: slug, SHA256: a.SHA256, Size: a.Size, Reason: "unreferenced",
			})
			if !dryRun {
				if derr := s.deleteLocked(ctx, slug, a.SHA256); derr != nil {
					return derr
				}
			}
		}
		return nil
	})
}

// allReferencedSHAs scans every doc's published versions and draft for bare
// content addresses, returning the union across ALL slugs. The union is what makes
// cross-doc / fork / JS-built references keep an asset alive (#44).
func (s *AssetService) allReferencedSHAs(ctx context.Context) (map[string]struct{}, error) {
	refs := map[string]struct{}{}

	// Every slug that has a doc row (published versions + draft)...
	docs, err := s.meta.ListMeta(ctx)
	if err != nil {
		return nil, err
	}
	scanned := map[string]struct{}{}
	for _, entry := range docs {
		if err := s.scanSlugInto(ctx, entry.Slug, refs); err != nil {
			return nil, err
		}
		scanned[entry.Slug] = struct{}{}
	}
	// ...plus any asset-bearing slug that has no doc row but may still hold a draft
	// blob referencing its own assets (defensive; keeps the union complete).
	assetSlugs, err := s.meta.ListAssetSlugs(ctx)
	if err != nil {
		return nil, err
	}
	for _, slug := range assetSlugs {
		if _, done := scanned[slug]; done {
			continue
		}
		if err := s.scanSlugInto(ctx, slug, refs); err != nil {
			return nil, err
		}
	}
	return refs, nil
}

// slugReferencedSHAs returns the content addresses referenced by one slug's own
// current HTML (all versions + draft).
func (s *AssetService) slugReferencedSHAs(ctx context.Context, slug string) (map[string]struct{}, error) {
	refs := map[string]struct{}{}
	if err := s.scanSlugInto(ctx, slug, refs); err != nil {
		return nil, err
	}
	return refs, nil
}

// scanSlugInto scans a slug's published versions and draft HTML, adding every bare
// content address it finds to refs.
func (s *AssetService) scanSlugInto(ctx context.Context, slug string, refs map[string]struct{}) error {
	scan := func(html string) {
		for _, m := range shaRe.FindAllString(html, -1) {
			refs[m] = struct{}{}
		}
	}
	versions, err := s.blobs.ListVersions(ctx, slug)
	if err != nil {
		return err
	}
	for _, n := range versions {
		html, ok, err := s.blobs.GetDoc(ctx, slug, n)
		if err != nil {
			return err
		}
		if ok {
			scan(html)
		}
	}
	if html, ok, err := s.blobs.GetDraft(ctx, slug); err != nil {
		return err
	} else if ok {
		scan(html)
	}
	return nil
}

// withinGrace reports whether an asset created at `created` (RFC3339-ish, as
// nowISO writes) is still inside the grace window ending at `now`. The second
// return (parsed) is false when the timestamp can't be parsed; callers fail SAFE
// on !parsed (keep the asset) since deletion is irreversible.
func withinGrace(created string, now time.Time, grace time.Duration) (within bool, parsed bool) {
	t, err := time.Parse(isoLayout, created)
	if err != nil {
		// Try RFC3339 as a fallback for any differently-formatted timestamps.
		t, err = time.Parse(time.RFC3339, created)
		if err != nil {
			return false, false
		}
	}
	return now.Sub(t) < grace, true
}
