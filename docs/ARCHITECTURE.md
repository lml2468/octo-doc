# octo-doc Architecture

octo-doc is a self-hosted reimplementation of [tdoc](https://github.com/serenakeyitan/tdoc)
that removes every Cloudflare dependency (Workers, KV, D1, R2, Durable Objects)
while preserving the document model, URL scheme, and comment semantics
byte-for-byte. It is written in Go 1.26 and ships as a single static binary.

## Before / after

```
┌─────────────────────── UPSTREAM (Cloudflare) ───────────────────────┐
│                                                                       │
│   tdoc-publish ──HTTP──▶  Worker (worker.js, 1921 LOC)                │
│                              ├── R2 bucket  DOCS   (immutable HTML)    │
│                              ├── KV         META   (meta + comments    │
│                              │                      + sessions)        │
│                              └── Durable Object COMMENTS               │
│                                   (per-slug write serialization)       │
│                           overlay.js inlined at BUILD time via         │
│                           the __TDOC_OVERLAY_JS__ placeholder          │
└───────────────────────────────────────────────────────────────────────┘

┌──────────────────────── octo-doc (self-hosted) ─────────────────────┐
│                                                                       │
│   tdoc-publish ──HTTP──▶  Go 1.26 app (chi router, static binary)     │
│   (Bearer token)             │                                         │
│                              │  transport/ ─▶ service/ ─▶ storage/     │
│                              │  (thin httpx) (logic)     (interfaces)  │
│                              │                                          │
│                              ├── internal/core/ (PORTED VERBATIM):     │
│                              │     cyrb53.go         hash primitive      │
│                              │     stamp.go          data-tdoc-aid       │
│                              │     fold.go           event-log fold       │
│                              │     events.go         eid/dedup/migrate    │
│                              │     ops.go            applyCommentOp        │
│                              │     reconcile.go      anchor reconcile       │
│                              │     render.go         overlay injection      │
│                              │     types.go          shared domain types     │
│                              │                                          │
│                              ├── internal/service/ DocService,          │
│                              │     CommentService, AuthService, github   │
│                              ├── internal/platform/ sluglock (per-slug  │
│                              │     lock ≈DO), config, log, apperr        │
│                              └── internal/storage/ {MetadataStore,      │
│                                    BlobStore}: postgres/ + s3/          │
│                                    (memory/ for tests)                  │
│                           assets/overlay.js embedded via go:embed       │
└───────────────────────────────────────────────────────────────────────┘
```

### Layering

Dependencies flow one way: **transport → service → storage**, with
`internal/core/` as a dependency-free domain kernel (a leaf) and cross-cutting
`internal/platform/` (`config`, `log`, `apperr`, `sluglock`). Handlers in
`internal/transport/httpx/` are thin (validate + shape); all logic lives in
services; no storage type (a pgx row, an S3 object) ever reaches a handler.
Module boundaries are ordinary Go packages exporting their public surface; there
are no import cycles.

### What maps to what

| Cloudflare primitive            | octo-doc replacement                                  |
| ------------------------------- | ----------------------------------------------------- |
| R2 bucket `DOCS`                | `BlobStore` → S3-compatible (S3 / MinIO)              |
| KV `META` (meta + comments)     | `MetadataStore` → PostgreSQL (pgx)                    |
| KV `session:*`                  | `MetadataStore.sessions` table                        |
| Durable Object `CommentsStore`  | in-process per-slug keyed mutex (`internal/platform/sluglock`) |
| `wrangler secret TDOC_UPLOAD_TOKEN` | `WRITE_TOKEN` env, or `/api/admin/bootstrap` token |
| Worker build-time overlay inline | `assets/overlay.js` embedded via `go:embed`         |
| `caches.default`, `Request.cf`, `waitUntil` | none — no Cloudflare assumptions leak in |

## Rendering parity (byte-equivalent output)

The success criterion *"相同输入下渲染字节级等价于上游 Workers"* is met by
**porting the rendering-critical functions verbatim** into `internal/core/`
rather than rewriting them:

- `stampAids()` — stamps `data-tdoc-aid="<cyrb53 hash>"` on every commentable
  artifact. Ported character-for-character from upstream worker.js. Verified by
  `go test ./internal/core/` against the golden fixtures in `testdata/golden`
  ("byte-parity with the upstream Cloudflare worker") across ordinary and
  adversarial HTML.
- The event-log comment model (`snapshotAt`, `dedupEvents`, `reconcileAnchors`,
  `compactComments`) — ported verbatim.
- Overlay injection (`injectOverlayCfg`) — ported verbatim; the only change is
  that `assets/overlay.js` is embedded via `go:embed` instead of inlined at
  build time. The bytes reaching the browser are identical.

Three porting traps that would silently break byte-equivalence are documented in
[PORTING.md](./PORTING.md): `Math.imul` 32-bit wraparound (reproduced with
`uint32` arithmetic), `charCodeAt` operating on UTF-16 code units (not Go runes
or bytes), and RE2's lack of backreferences (no `\1` in the Go `regexp`
package).

The single deliberate divergence: `eventEid()` for one-shot events used
`Math.random()` upstream; octo-doc uses a monotonic counter + high-res time.
This only affects the *uniqueness suffix* of non-idempotent event ids, never
the fold result — `dedupEvents` keys on the id, and idempotent events keep
their deterministic ids unchanged.

## Data model

Unchanged from upstream:

- **Document**: `slug` + monotonically increasing integer `version` →
  immutable HTML blob. A republish of the same slug always gets `max(version)+1`.
- **URL**: `/d/<slug>/v/<version>` (preserved). Plus `/export` and `/fork`.
- **Comments**: an append-only **event log** per slug. Each version is a
  snapshot — reading "as of version N" folds events with `at_version <= N`.
  Mutations append events; they never overwrite. See `internal/core/fold.go`.

### Storage records

| Store               | Key            | Value                                            |
| ------------------- | -------------- | ------------------------------------------------ |
| `MetadataStore.meta`     | slug      | `{ title, slug, versions: [{n, created}] }`      |
| `MetadataStore.comments` | slug      | the full event-log comment array                 |
| `MetadataStore.sessions` | sid       | `{ login, avatar_url, name, created }` (+ TTL)    |
| `MetadataStore.tokens`   | token     | `{ token, created, label }`                      |
| `BlobStore`         | (slug, version) | immutable stamped HTML                          |

## API specification

All endpoints from the upstream Worker are preserved equivalently, plus three
new ones (`/api/docs`, `/api/docs/:slug/versions`, `/api/admin/bootstrap`).

### Public reads (no auth unless `PRIVATE=1`)

| Method | Path | Description |
| ------ | ---- | ----------- |
| `GET`  | `/api/ping` | `{ ok, service: "tdoc" }` health/identity marker |
| `GET`  | `/healthz` | `{ ok }` liveness for orchestrators |
| `GET\|HEAD` | `/d/:slug/v/:version` | rendered doc with overlay injected |
| `GET`  | `/d/:slug/v/:version/export` | doc + comment banner, `Content-Disposition: attachment` |
| `GET`  | `/d/:slug/v/:version/fork` | doc + comments, overlay in read-only fork mode |
| `GET`  | `/api/docs/:slug/versions` | `{ slug, title, versions: [{n, created}] }` |
| `GET`  | `/api/comments?slug=&version=` | folded comment snapshot (`version=all` for full history) |
| `GET`  | `/` | neutral landing page (no catalog) |
| `GET`  | `/me` | owner-only doc catalog (redirects others) |

### Comment mutations (anonymous)

| Method | Path | Description |
| ------ | ---- | ----------- |
| `POST`   | `/api/comments` | create a comment or reply |
| `PATCH`  | `/api/comments` | re-anchor (author/owner only once a session exists) |
| `DELETE` | `/api/comments?slug=&id=&version=` | soft-delete (author/owner only once a session exists) |
| `POST`   | `/api/reactions` | toggle an emoji reaction |

Comments are anonymous: there is no built-in login provider, so these endpoints
require no session. The author/owner checks on PATCH/DELETE are a no-op while
anonymous (unowned comments) and activate automatically once a future Octo
unified login populates viewer sessions.

### Write endpoints (Bearer token required)

| Method | Path | Description |
| ------ | ---- | ----------- |
| `POST`   | `/api/docs` | publish (multipart `file=@doc.html, slug=` **or** JSON `{slug, version, html, meta, comments}`); auto-increments version when omitted |
| `POST`   | `/api/upload` | legacy alias of `/api/docs` (JSON) — preserves the existing CLI contract |
| `POST`   | `/api/agent/reply` | agent posts a reply + verdict (✅/🟡/❓) |
| `DELETE` | `/api/doc?slug=` | delete all versions + comments |
| `DELETE` | `/api/comments?slug=&all=1` | wipe all comments for a slug |
| `GET`    | `/api/admin/bootstrap` | mint the first write token (then 409s) |

### Viewer sessions

`GET /api/auth/me` (reports the current viewer; anonymous → `identity: null`) and
`POST /api/auth/logout` (clears a session). There is no built-in login provider
yet; the session machinery (`sessions` table, `AuthService.CreateSession`) is the
seam a future Octo unified login plugs into.

## Concurrency

Per-slug comment writes are serialized by `internal/platform/sluglock` — an
in-process keyed mutex that makes `read → applyCommentOp → write` atomic for a
given slug, exactly the guarantee the Durable Object provided. This is correct
for the default **single-instance** deployment. The event log additionally
converges under concurrent writes via `dedupEvents` (stable event ids), so even
races that the mutex doesn't cover (e.g. future multi-instance) degrade to
last-write-wins-per-event rather than corruption. `sluglock` is an interface, so
multi-instance horizontal scaling can swap the in-process lock for a Postgres
advisory-lock implementation, documented in [DESIGN.md](./DESIGN.md).

## Request lifecycle (publish)

```
tdoc-publish <slug>
  └─ POST /api/docs  (Authorization: Bearer <token>, multipart or JSON)
       ├─ requireWriteAuth         constant-time token check
       ├─ size cap check           (MAX_HTML_BYTES, default 5 MiB)
       ├─ next version = max(blobStore.listVersions)+1   (if not explicit)
       ├─ stampAids(html)          identity-stamp artifacts (verbatim port)
       ├─ blobStore.putDoc         immutable write + head-verify
       ├─ metaStore.putMeta        monotonic versions[]
       └─ commentStore.publish_merge   reconcile anchors + merge local comments
     → { ok, slug, version, url: "/d/<slug>/v/<n>", size, aids, mergedComments }
```

See [DESIGN.md](./DESIGN.md) for the runtime/framework selection rationale,
threat model, adapter contract, and backup/upgrade procedures.
