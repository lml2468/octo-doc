# Media assets: self-hosted inline images, video, and other binary content

> Status: **P0 + P1 implemented; P2 designed.** P0 (explicit CSP `media-src` /
> `frame-src` / `object-src`) and P1 (the asset subsystem: storage, service,
> HTTP surface, CLI) have shipped. P2 (ergonomics) is design-only. Section
> headings note per-item status.

## Problem

An octo-doc document is a single, self-contained, byte-frozen HTML blob. Today a
document can *reference* any inline media the browser supports (the CSP allows
`<img>`, `<video>`, `<audio>`, `<iframe>`, `<object>`, fonts, SVG, canvas — see
below), but it has nowhere to *host* the bytes. The only two options are both
poor:

- **base64-inline into the HTML** — bounded by `MAX_HTML_BYTES` (default 5 MiB),
  inflated ~33% by base64, and re-shipped in full on every render. A couple of
  photos or any video blows the budget.
- **hot-link a third-party CDN** — breaks self-containment, adds an external
  availability dependency, and contradicts the "self-hosted, immutable version"
  product stance. Third-party media URLs are also typically unauthenticated,
  leaking a private doc's imagery.

The blob backend (S3 `BlobStore`) is already present; it simply isn't exposed for
per-doc media. This design adds a **media asset subsystem** so authors can upload
binary resources that are hosted by the same server, addressed by stable URLs,
and governed by the same per-doc capability model as the document itself.

## What the CSP already allows (P0, shipped)

`docSecurityHeaders` now emits explicit directives so rich inline content is an
intentional, independently-tunable capability rather than a `default-src`
fallback:

```
default-src 'self' data: blob: https:
script-src  'self' 'unsafe-inline' 'unsafe-eval' data: blob: https:
style-src   'self' 'unsafe-inline' https:
img-src     'self' data: blob: https:
media-src   'self' data: blob: https:
font-src    'self' data: https:
connect-src 'self' https:
frame-src   'self' https:
object-src  'self' data: blob:
base-uri    'self'
frame-ancestors <configured>
```

Self-hosted assets are served **same-origin**, so `'self'` in `img-src` /
`media-src` / `object-src` covers them with no CSP change required by this
subsystem.

## Design principles

1. **Never touch `internal/core/`.** Assets are orthogonal to aid stamping,
   event-log fold, and overlay injection. The golden byte-equivalence tests must
   remain untouched and green. Everything here lives in `storage` / `service` /
   `transport` / `config` / `cmd/octo`.
2. **Content-addressed and immutable.** An asset is named by the SHA-256 of its
   bytes. Identical bytes dedupe automatically; a URL, once minted, never changes
   meaning — the same immutability guarantee versions already have.
3. **Same capability model.** An asset inherits the access control of its owning
   doc. No new auth axis: author uploads/deletes, reader (share code) reads,
   `none` gets 404. See `docs/AUTH.md`.
4. **Dependencies flow one way** — `transport → service → storage`, `core` a
   leaf. Adapters stay behind the `storage` interfaces; no driver type leaks.

---

# P1 — the asset subsystem *(implemented)*

## Storage model

Assets are content-addressed and scoped to a document (not to a single version —
sharing one image across versions is the common case and dedupe should span the
whole doc).

**Blob layout (S3):**

```
docs/<hashSlug>/assets/<sha256>          # raw bytes, immutable
```

`<hashSlug>` reuses `storage.HashSlug` (the existing path-traversal defense).
`<sha256>` is the lowercase hex digest of the uploaded bytes. No file extension
in the key — the MIME type is authoritative metadata, not the URL suffix.

**Metadata (PostgreSQL):** one row per (doc, asset).

```sql
CREATE TABLE assets (
    slug          TEXT        NOT NULL,
    sha256        TEXT        NOT NULL,       -- lowercase hex, 64 chars
    mime          TEXT        NOT NULL,       -- validated against an allowlist
    size          BIGINT      NOT NULL,
    original_name TEXT        NOT NULL,       -- for display / download filename
    created       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (slug, sha256)
);
CREATE INDEX assets_slug_idx ON assets (slug);
```

`(slug, sha256)` PK gives free intra-doc dedupe: re-uploading identical bytes is
idempotent. `slug` index supports listing and cascade-delete when a doc is
removed.

## Storage interface additions

Add to `internal/storage`. Keep the two interfaces separate, mirroring
`BlobStore` vs `MetadataStore`.

```go
// BlobStore — raw asset bytes, content-addressed.
type BlobStore interface {
    // ... existing PutDoc/GetDoc/Draft/... unchanged ...

    // PutAsset stores raw bytes at docs/<hashSlug>/assets/<sha256>. Idempotent:
    // writing the same key twice is a no-op success (content-addressed).
    PutAsset(ctx context.Context, slug, sha256 string, data []byte) error
    // GetAsset returns the raw bytes; ok=false when absent.
    GetAsset(ctx context.Context, slug, sha256 string) (data []byte, ok bool, err error)
    DeleteAsset(ctx context.Context, slug, sha256 string) error
    // DeleteDoc must also drop the assets/ prefix (extend existing impl).
}

// MetadataStore — asset registry.
type MetadataStore interface {
    // ... existing methods unchanged ...

    PutAssetMeta(ctx context.Context, meta AssetMeta) error
    GetAssetMeta(ctx context.Context, slug, sha256 string) (*AssetMeta, error)
    ListAssetMeta(ctx context.Context, slug string) ([]AssetMeta, error)
    DeleteAssetMeta(ctx context.Context, slug, sha256 string) error
}

type AssetMeta struct {
    Slug         string `json:"slug"`
    SHA256       string `json:"sha256"`
    MIME         string `json:"mime"`
    Size         int64  `json:"size"`
    OriginalName string `json:"original_name"`
    Created      string `json:"created"`
}
```

The `memory/` fake gains a `map[string][]byte` for bytes and a
`map[string]AssetMeta` for metadata so the httpx tests run without real
services, exactly as they do for docs today.

## Service layer

`internal/service/asset.go` — `AssetService`, holding a `BlobStore` +
`MetadataStore` and the config (size cap, MIME allowlist). Reuses the per-slug
`sluglock` so concurrent uploads/deletes on one doc serialize, consistent with
`CommentService`.

```go
func (s *AssetService) Put(ctx, slug string, r io.Reader, originalName string) (AssetMeta, error)
func (s *AssetService) Get(ctx, slug, sha256 string) (bytes []byte, meta AssetMeta, err error)
func (s *AssetService) List(ctx, slug string) ([]AssetMeta, error)
func (s *AssetService) Delete(ctx, slug, sha256 string) error
```

`Put`:
1. Read the reader with a hard cap of `MaxAssetBytes+1`; over-limit → validation
   error (`asset_too_large`).
2. Sniff MIME via `http.DetectContentType` on the first 512 bytes; cross-check
   against the allowlist (§ Config). Reject on mismatch/absent
   (`unsupported_media_type`). The **sniffed** type wins over any client-declared
   type — never trust the client's `Content-Type`.
3. Compute SHA-256, `PutAsset` (idempotent), then `PutAssetMeta`.
4. Return `AssetMeta` (the handler turns it into a URL).

## HTTP surface (`/v1` envelope)

Uploads/mutations are author-only (write token via `Authorization: Bearer`) and
subject to the CORS allowlist, like all mutating `/v1` routes. Reads carry the
per-doc capability (author Bearer **or** reader cookie/`?code=`), identical to
document render.

| Method & path | Cap | Body / Result |
| --- | --- | --- |
| `POST /v1/docs/{slug}/assets` | author | multipart `file=`; → `{ sha256, url, mime, size }` |
| `GET  /v1/docs/{slug}/assets` | reader | → `[ AssetMeta … ]` |
| `DELETE /v1/docs/{slug}/assets/{sha256}` | author | → `204` |

**Serving path (what HTML references):**

```
GET /d/{slug}/assets/{sha256}      cap: reader (same as /d/{slug}/v/N)
```

Served with:
- `Content-Type: <registered mime>`
- `X-Content-Type-Options: nosniff`
- `Content-Security-Policy` — a **locked-down** CSP for the raw-asset response
  (`default-src 'none'; sandbox`), since this endpoint returns attacker-supplied
  bytes and must never be interpretable as an active document. This is stricter
  than `docSecurityHeaders` and applies only to the asset route.
- `Cache-Control: private, max-age=31536000, immutable` — safe to cache forever
  because the URL is content-addressed; `private` because a doc's assets inherit
  its per-doc access control.
- Optional `Content-Disposition: inline; filename="<original_name>"`.

Reusing the existing `secHeaders`/capability middleware for `/d/*` means the
share-code cookie already set on the doc visit authorizes asset fetches with no
extra round-trip.

### Why `sha256` in the URL and not a random id

Content addressing makes the URL a pure function of the bytes: safe to cache
forever, dedupe for free, and impossible to desync from metadata. The tradeoff —
you can't "replace" an asset in place — is a feature here, matching immutable
versions. To change an image you upload new bytes (new URL) and edit the HTML,
which is a new draft anyway.

## CLI (`cmd/octo`)

Three flat subcommands (matching `main.go`'s flat dispatch):

```
octo asset-add    --slug <s> <file>     # upload, print the referenceable URL
octo asset-list   --slug <s>            # list a doc's assets
octo asset-rm     --slug <s> <sha256>   # delete one asset
```

`asset-add` prints the absolute URL (`<OCTO_BASE_URL>/d/<slug>/assets/<sha>`) so
the author can paste it straight into `<img src=…>` / `<video>` before the next
`octo publish` (`--quiet` prints only the URL, for scripting). Upload and delete
are author ops → require `OCTO_TOKEN`; list needs only reader capability.

## Config additions

`internal/config` (parsed once, as always — no other package reads env):

| Var | Default | Meaning |
| --- | --- | --- |
| `MAX_ASSET_BYTES` | `26214400` (25 MiB) | per-asset upload cap |
| `ASSET_MIME_ALLOW` | see below | comma-separated MIME allowlist |

Default allowlist (conservative, image + common AV + PDF):

```
image/png, image/jpeg, image/gif, image/webp, image/avif, image/svg+xml,
video/mp4, video/webm, audio/mpeg, audio/ogg, audio/wav, application/pdf
```

`image/svg+xml` is allowlisted but **must only ever be served from the
locked-down asset CSP** (`sandbox; default-src 'none'`) — an SVG can carry
script, and serving it same-origin without sandboxing would be an XSS vector.
This is why the asset serving route does **not** reuse `docSecurityHeaders`.

## Testing

- `storage/memory`: unit tests for put/get/list/delete + dedupe idempotence.
- `service`: size-cap rejection, MIME sniff vs allowlist, sniff-overrides-client.
- `httpx`: full lifecycle (author uploads → reader GETs with code → `none` 404 →
  author deletes → 404), plus header assertions (immutable cache, locked CSP,
  nosniff). Follows `TestPublishRenderLifecycle` structure.
- Real pg/s3 suites gated on `OCTO_TEST_*` as today.

## Migration

The `assets` table is part of the canonical `postgres.Schema`, applied
idempotently at store `Open` and by `octo-doc migrate` (all statements are
`IF NOT EXISTS`) — no standalone migration file. No change to existing tables; no
backfill. `DeleteDoc` (blob) purges asset bytes via the shared `docs/<hash>/`
prefix, and `DocService.Remove` additionally deletes the asset metadata rows so
none orphan when a slug is removed or reused.

## Out of scope for P1

- Rewriting HTML references automatically (see P2).
- Image transforms / thumbnails / transcoding.
- Cross-doc asset sharing (assets are per-doc; dedupe is within a doc).
- Range requests for large video seeking (see P2).

---

# P2 — ergonomics & polish

Layered on P1; each item is independent.

## P2.1 — local reference rewriting *(implemented)*

`octo new` / `octo version-add` accept `--rewrite-assets`: before the draft is
saved, scan the HTML for local references (`src=`/`poster=` attributes, `srcset=`
candidate lists, and CSS `url(...)`), upload each referenced local file as an
asset, and rewrite the reference to the minted asset URL. Turns "author with
local files, publish self-contained" into one step. (The flag lives on the
HTML-bearing commands; `octo publish` only promotes an existing draft and carries
no HTML.)

Implementation (`cmd/octo/rewrite.go`):
- Paths resolve relative to the HTML file's directory (cwd for `--html-stdin`);
  a traversal guard refuses references that escape it. Remote (`http`/`https`/
  protocol-relative), `data:`/`blob:`, site-absolute (`/…`), and anchor (`#…`)
  references are left untouched.
- Identical files (by resolved path) upload once and share one URL (dedupe).
- Purely a CLI-side transform on the bytes *before* they reach `/v1/docs`; the
  server still receives final HTML and stamps it in `core` unchanged — **golden
  parity untouched.**
- Dry-run mode (`--rewrite-assets=dry`) prints the planned uploads and leaves the
  HTML unchanged.

## P2.2 — HTTP range requests for media *(implemented)*

`GET/HEAD /d/{slug}/assets/{sha256}` serves through `http.ServeContent`, which
adds `Range` / `Accept-Ranges: bytes` / `206 Partial Content` (and `416` for
unsatisfiable ranges), so browsers can seek within `<video>`/`<audio>` without
fetching the whole file. It also handles HEAD and conditional requests, and
respects the pre-set `Content-Type` (no sniffing) so the locked-down
CSP/nosniff/`Cache-Control` headers stay intact on partial responses.

The current implementation wraps the already-fetched bytes in a `bytes.Reader`
(an `io.ReadSeeker`) — no `BlobStore` change. If assets grow large enough that
holding the whole blob in memory per request matters, this can later be swapped
for a streaming ranged read (`GetAssetRange`, or an `io.ReadSeeker` backed by S3's
`Range` header) with no change to the route or its headers.

## P2.3 — `<object>`/`<embed>` PDF embedding *(implemented / documented)*

Inline PDF works with no new mechanism, and is now covered by an end-to-end test
(`TestAssetPDFEmbedPath`):

- `application/pdf` is in the default `ASSET_MIME_ALLOW`, and PDF bytes sniff as
  `application/pdf`, so upload/list/serve all work through the P1 asset path.
- The document render CSP already permits `object-src 'self' data: blob:` (P0),
  and assets are same-origin, so a doc can embed an uploaded PDF directly.
- The asset is served with `Content-Type: application/pdf` under the locked-down
  `default-src 'none'; sandbox` CSP + `nosniff` — the browser's PDF viewer renders
  it, but it cannot act as an active document.

Authoring pattern:

```bash
octo asset-add --slug spec report.pdf   # → https://host/d/spec/assets/<sha>
```

```html
<object data="/d/spec/assets/<sha>" type="application/pdf" width="100%" height="800">
  <p>Your browser can't display PDFs. <a href="/d/spec/assets/<sha>">Download it.</a></p>
</object>
```

`--rewrite-assets` (P2.1) also rewrites `<object data="./report.pdf">` when
authoring with a local file: `data` is one of the scanned attributes (alongside
`src`/`poster`/`srcset`/CSS `url()`). Because matching is by attribute name with a
word boundary, `data-src="./x.png"` (a common lazy-load attribute) is also
rewritten via the `src` match — usually what you want; unrelated attributes like
`href` are left alone.

## P2.4 — orphan GC *(implemented)*

Content-addressed assets can outlive the HTML that referenced them (author
uploads, never references, or edits the reference away). The `octo-doc gc-assets`
maintenance subcommand scans every doc's published versions **and** current draft
for referenced `sha256`s, then deletes assets that are BOTH unreferenced AND older
than a grace window (default 24h; keeps a just-uploaded asset that hasn't been
wired into a draft yet).

```bash
octo-doc gc-assets                    # delete unreferenced assets older than 24h
octo-doc gc-assets --grace 168h       # 7-day grace
octo-doc gc-assets --dry-run          # report what would be deleted, delete nothing
```

Logic lives in `AssetService.GCAssets(ctx, grace, now, dryRun) → GCReport`
(`internal/service/gc.go`), with `now` injected for testability; the command
(`cmd/octo-doc`) wires flags and logs the per-asset and summary results. Deletion
reuses `AssetService.Delete` (per-slug lock, drops blob + metadata row). Grace is
measured from each asset's `Created` timestamp; an unparseable timestamp is
treated as past-grace. Run it from cron/a maintenance job — it is never automatic,
since immutability is safer than aggressive deletion.

---

## Summary of touched packages

| Package | P1 | P2 |
| --- | --- | --- |
| `internal/core/` | **untouched** | **untouched** |
| `internal/storage` (+ `memory`, `postgres`, `s3`) | interfaces + adapters | — |
| `internal/service` | `AssetService` | `GCAssets` (P2.4) |
| `internal/transport/httpx` | 3 `/v1` handlers + 1 `/d` serve route | range serving via `http.ServeContent` (P2.2) |
| `internal/config` | `MAX_ASSET_BYTES`, `ASSET_MIME_ALLOW` | — |
| `cmd/octo` | `asset-add/list/rm` | `--rewrite-assets` (P2.1) |
| `cmd/octo-doc` | `assets` schema; `DocService.Remove` purges asset rows | `gc-assets` (P2.4) |
| `docs/` | this file | — |
