# octo-doc Design Notes

Motivation, runtime selection, threat model, the storage-adapter contract, and
operational procedures (backup, upgrade).

## Motivation

tdoc is excellent but couples you to Cloudflare: publishing requires a
Cloudflare account, `wrangler login`, an R2 bucket, a KV namespace, a claimed
`workers.dev` subdomain, and a Durable Object migration. That's a lot of
vendor-specific surface for "host an HTML file with comments." octo-doc keeps
the product identical and makes it run anywhere a Node process runs — a $5 VPS,
a homelab box, a container platform — with `docker compose up -d` or `npx`.

## Runtime & framework selection

**Runtime: Node 22 LTS.** Chosen over Bun for one decisive reason — Node 22+
ships a built-in SQLite (`node:sqlite`). That removes the only native dependency
(`better-sqlite3` needs a C++ build that breaks on bleeding-edge Node ABIs), so:

- the Docker image needs no build toolchain and ships small — measured **237 MB
  uncompressed (~75 MB compressed)** on `node:22-alpine` for the default
  sqlite+fs stack. The Node 22 runtime binary is the size floor; the relevant
  win is avoiding the dev-dep/toolchain bloat the spec warns about (>500 MB).
  Measured idle RSS in-container: **~23 MB** (target was ≤ 150 MB);
- `npx octo-doc` works with zero compilation on any Node 22+ machine;
- the dependency tree for the default sqlite+fs stack is ~15 MB.

Bun's `bun:sqlite` is equally capable, and the code is plain ESM that runs under
Bun too (`bun start` works) — but pinning Node 22 LTS gives the widest, most
boring deployment target. The HTTP layer (Hono) is runtime-agnostic, so a
future Bun-first build is a one-line server-adapter swap.

**Framework: Hono.** Express is excluded by the constraints. Hono was picked
over Fastify/Elysia because its handlers are built on the standard
`Request`/`Response` web API, which means the entire app is testable via
`app.fetch(new Request(...))` with no socket (see `test/unit/http.test.js`) and
trivially portable across Node/Bun/edge runtimes.

## Threat model

octo-doc serves **untrusted, user-authored HTML with inline JS** as its whole
purpose. The security posture:

| Threat | Mitigation |
| ------ | ---------- |
| **Path traversal via slug** (`slug=../../etc`) | `safeSlug()` validates `^[A-Za-z0-9_-]{1,64}$` at every route; blob keys are additionally the SHA-256 hash of the slug, so even a bypassed slug can't escape the storage root. |
| **Parent-panel XSS / clickjacking** | CSP `frame-ancestors` (default `'none'`) + `X-Frame-Options` on every `/d/*` response. Embedding requires explicit `FRAME_ANCESTORS`. |
| **Cookie theft via same-origin user HTML** | Recommended deployment serves docs from a **separate subdomain** (`d.example.com`) than any trusted panel, so a doc's inline JS lives in its own origin and cannot read app cookies. The Caddyfile documents this two-site layout. |
| **Token leakage** | Write token travels only in the `Authorization: Bearer` header, never the URL. Token comparison is constant-time. |
| **Unbounded HTML** | `MAX_HTML_BYTES` (default 5 MiB) rejects oversized docs with 413. |
| **Write flooding** | Fixed-window rate limiter keyed by `token + client IP` on all mutating routes. |
| **Disk exhaustion** | `MAX_VERSIONS_PER_SLUG` quota knob; immutable blobs are append-only so an operator can prune old versions out of band. |
| **Server never evals user content** | HTML is stored and served as an opaque blob. The server's own CSP wrapper does not execute it; `stampAids` is pure string rewriting. |
| **Corrupt stored comments** | `safeParseList` fails soft to `[]` for display; writes go through the mutex so a partial write can't interleave. |

What octo-doc does **not** defend against (by design): the *content* of a
document's own inline JavaScript. That JS runs in the viewer's browser on the
doc's origin — which is exactly why the separate-subdomain deployment matters.

## Storage adapter contract

The route layer depends only on two duck-typed interfaces; no SQLite/Postgres/
S3 type ever reaches a route. Full method signatures live in
[`src/storage/index.js`](../src/storage/index.js).

```
MetadataStore   small structured records (meta, comments, sessions, tokens)
  getMeta/putMeta/deleteMeta/listMeta
  getComments/putComments/deleteComments     ← always returns an array
  getSession/putSession(ttl)/deleteSession   ← honors TTL
  getToken/putToken/anyToken
  close()

BlobStore       immutable HTML keyed by (slug, version)
  putDoc/getDoc/headDoc/listVersions/deleteDoc
```

`STORAGE="<meta>+<blob>"` selects the pair at boot (`makeStores`). Swapping
`sqlite+fs` → `postgres+s3` changes **zero application code** — only the env var
and the optional-dependency install. The adapter-swap E2E in CI proves this by
running the identical `test/e2e.test.js` against both stacks.

### Why two stores, not one

Metadata is small, transactional, and queried (`listMeta` for the catalog).
Blobs are large, immutable, and write-once. Splitting them lets each pick the
right backend: a relational store for metadata, an object store for blobs —
the same split R2+KV made on Cloudflare, now pluggable.

## Concurrency, in depth

The per-slug mutex (`core/mutex.js`) is an in-process promise chain keyed by
slug. It guarantees the read-modify-write of a slug's comment list is atomic
within one process — the Durable Object's guarantee, minus the network hop.

For **multi-instance** deployments (several app containers behind a load
balancer sharing one Postgres), the in-process mutex is insufficient. The
intended upgrade: wrap the comment read-modify-write in
`SELECT pg_advisory_xact_lock(advisoryKey(slug))` inside a transaction
(`storage/postgres.js` exposes `advisoryKey`). The event log's `dedupEvents`
convergence is the safety net in the meantime — concurrent writes converge to
the same fold rather than corrupting. Single-instance is the documented,
supported default.

## Backup & restore

**SQLite + FS (default).** Everything is under `DATA_DIR` (`/data` in Docker):

```bash
# Online, consistent SQLite snapshot (WAL-safe):
sqlite3 /data/octo-doc.db ".backup '/backup/octo-doc.db'"
# Blobs are plain immutable files:
tar czf /backup/blobs.tgz -C /data blobs
# Restore: stop the app, drop both back into DATA_DIR, start.
```

**Postgres + S3.**

```bash
pg_dump "$DATABASE_URL" > octo-doc.sql           # metadata + comments
# Blobs live in the bucket; use S3 versioning + lifecycle rules for retention,
# or aws s3 sync s3://octo-doc ./backup/blobs for a cold copy.
```

S3 lifecycle policy is the recommended way to cap blob growth: e.g. transition
objects older than 90 days to infrequent-access, or expire non-current versions
if bucket versioning is on.

## Upgrade procedure

1. Pull the new image / `git pull`.
2. `npm run migrate` (idempotent — all DDL is `IF NOT EXISTS`; SQLite applies
   the schema at open automatically).
3. Restart. Immutable blobs and the append-only comment log mean no data
   transformation is needed across versions — old docs render unchanged.

Because documents are immutable and comments are an append-only event log,
upgrades are non-destructive by construction: there is no in-place mutation a
new version could corrupt.

## Migrating from a Cloudflare deployment

See [MIGRATING_FROM_WORKERS.md](./MIGRATING_FROM_WORKERS.md) for the KV/R2 →
SQLite/FS (or Postgres/S3) import path.
