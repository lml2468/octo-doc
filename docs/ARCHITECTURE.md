# octo-doc Architecture

octo-doc is a self-hosted reimplementation of [tdoc](https://github.com/serenakeyitan/tdoc)
that removes every Cloudflare dependency (Workers, KV, D1, R2, Durable Objects)
while preserving the document model, URL scheme, comment semantics, and the
`SKILL.md` tool contract byte-for-byte.

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
│   tdoc-publish ──HTTP──▶  Hono app (Node 22)                          │
│   (Bearer token)             │                                         │
│                              ├── core/  (PORTED VERBATIM, CF-free):    │
│                              │     stamp.js     — data-tdoc-aid stamper │
│                              │     comments.js  — event-log fold        │
│                              │     ops.js       — applyCommentOp        │
│                              │     render.js    — overlay injection     │
│                              │     store.js     — serialized writes     │
│                              │     mutex.js     — per-slug lock (≈ DO)   │
│                              │                                          │
│                              └── storage/ {MetadataStore, BlobStore}:   │
│                                    sqlite.js + fs.js   (default)        │
│                                    postgres.js + s3.js (optional)       │
│                           overlay.js read at RUNTIME (same bytes)       │
└───────────────────────────────────────────────────────────────────────┘
```

### What maps to what

| Cloudflare primitive            | octo-doc replacement                                  |
| ------------------------------- | ----------------------------------------------------- |
| R2 bucket `DOCS`                | `BlobStore` → FS (`./data/blobs`) or S3/MinIO         |
| KV `META` (meta + comments)     | `MetadataStore` → SQLite (`node:sqlite`) or Postgres  |
| KV `session:*`                  | `MetadataStore.sessions` table                        |
| Durable Object `CommentsStore`  | in-process per-slug async mutex (`core/mutex.js`)     |
| `wrangler secret TDOC_UPLOAD_TOKEN` | `WRITE_TOKEN` env, or `/api/admin/bootstrap` token |
| Worker build-time overlay inline | runtime `readFileSync` of `src/overlay.js`           |
| `caches.default`, `Request.cf`, `waitUntil` | none — no Cloudflare assumptions leak in |

## Rendering parity (byte-equivalent output)

The success criterion *"相同输入下渲染字节级等价于上游 Workers"* is met by
**porting the rendering-critical functions verbatim** into `src/core/` rather
than rewriting them:

- `stampAids()` — stamps `data-tdoc-aid="<cyrb53 hash>"` on every commentable
  artifact. Copied character-for-character from `worker.js`. Verified against
  the upstream implementation in `test/unit/core.test.js` ("byte-parity with
  the upstream Cloudflare worker") across ordinary and adversarial HTML.
- The event-log comment model (`snapshotAt`, `dedupEvents`, `reconcileAnchors`,
  `compactComments`) — copied verbatim.
- Overlay injection (`injectOverlayCfg`) — copied verbatim; the only change is
  that `overlay.js` is read at runtime instead of inlined at build time. The
  bytes reaching the browser are identical.

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
  Mutations append events; they never overwrite. See `src/core/comments.js`.

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

### Comment mutations (session if `GITHUB_CLIENT_ID` set, else anonymous)

| Method | Path | Description |
| ------ | ---- | ----------- |
| `POST`   | `/api/comments` | create a comment or reply |
| `PATCH`  | `/api/comments` | re-anchor (author/owner only) |
| `DELETE` | `/api/comments?slug=&id=&version=` | soft-delete (author/owner only) |
| `POST`   | `/api/reactions` | toggle an emoji reaction |

### Write endpoints (Bearer token required)

| Method | Path | Description |
| ------ | ---- | ----------- |
| `POST`   | `/api/docs` | publish (multipart `file=@doc.html, slug=` **or** JSON `{slug, version, html, meta, comments}`); auto-increments version when omitted |
| `POST`   | `/api/upload` | legacy alias of `/api/docs` (JSON) — preserves the existing CLI contract |
| `POST`   | `/api/agent/reply` | agent posts a reply + verdict (✅/🟡/❓) |
| `DELETE` | `/api/doc?slug=` | delete all versions + comments |
| `DELETE` | `/api/comments?slug=&all=1` | wipe all comments for a slug |
| `GET`    | `/api/admin/bootstrap` | mint the first write token (then 409s) |

### Auth (only active when `GITHUB_CLIENT_ID` is set)

`GET /api/auth/me`, `POST /api/auth/device/start`, `POST /api/auth/device/poll`,
`POST /api/auth/logout` — GitHub Device Flow, identical to upstream.

## Concurrency

Per-slug comment writes are serialized by `core/mutex.js` — an in-process async
mutex that makes `read → applyCommentOp → write` atomic for a given slug,
exactly the guarantee the Durable Object provided. This is correct for the
default **single-instance** deployment. The event log additionally converges
under concurrent writes via `dedupEvents` (stable event ids), so even races
that the mutex doesn't cover (e.g. future multi-instance) degrade to
last-write-wins-per-event rather than corruption. Multi-instance horizontal
scaling would swap the mutex for a Postgres advisory lock (sketched in
`storage/postgres.js`), documented in [DESIGN.md](./DESIGN.md).

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
