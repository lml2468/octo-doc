# POLISH.md — local polish protocol log

> **Historical (TS era).** This log records the five polish iterations run
> against the original **Node 22 / TypeScript** prototype. octo-doc has since
> been rewritten in **Go 1.26** (single static binary, Postgres + S3 only). The
> findings and design decisions below still describe *why* the system behaves as
> it does — atomic blob writes, the per-slug lock, soft-failing comment parsing,
> the typed error hierarchy — but the toolchain specifics (pnpm, tsup, vitest,
> ESLint, `node:sqlite`) are superseded. Their Go equivalents are noted inline
> and the current invariants live in the [Standing invariants](#standing-invariants-go)
> section at the bottom.

A record of the five polish iterations run against octo-doc, each with what was
found and what changed. The discipline: run it locally, look hard, fix the real
thing (no TODOs left behind), re-run.

---

## Iteration 1 — Walk the happy path

**Goal:** publish a single file, open it, see the content.

```
node dist/index.js & TOKEN=$(curl -s localhost:8080/api/admin/bootstrap | jq -r .token)   # TS era
curl -H "Authorization: Bearer $TOKEN" -F file=@fixtures/hello.html -F slug=hello localhost:8080/api/docs
open localhost:8080/d/hello/v/1
```

**Findings & fixes**

- _Adapter selection silently wrong._ `makeStores` read `env.STORAGE` but was
  passed the parsed config object (`config.storage`), so a non-default storage
  selection fell back silently with no error. This is exactly the "偷渡 adapter
  default" failure mode. **Fix:** the storage factory took the typed `Config` and
  read `config.storage`; adapters read their own env explicitly. (Moot in the Go
  rewrite — there is no storage switch at all: Postgres + S3 are the only
  backends, wired from `internal/platform/config`. The lesson lives on as the
  adapter-swap contract test that still runs the service suite against real
  Postgres + S3 in CI.)
- _Version auto-increment unclear from the response._ Added `version`, `aids`,
  and `mergedComments` to the publish response so the walk is self-explaining.

---

## Iteration 2 — Harden (inject faults)

**Goal:** kill the process mid-publish, hammer the same slug, send junk.

**Findings & fixes**

- _Half-written documents on crash._ A naive `writeFileSync(index.html)` can
  leave a truncated file if the process dies mid-write. **Fix (TS-era FS store,
  now superseded):** the FS blob store wrote to a temp file and `rename`d into
  place (atomic on POSIX), and `listVersions` ignored a `v<N>` dir whose
  `index.html` wasn't present yet, so an in-flight publish was never advertised.
  Verified by the chaos test: fire 8 concurrent publishes, `SIGKILL`, restart,
  assert every reported version is readable and no `.tmp` files leak. In the Go
  rewrite the FS path is gone — blobs live in S3, where `PutObject` is atomic per
  key by construction, so the partial-file failure mode does not exist.
- _Lost updates under concurrent same-slug comments._ Two writers could read the
  same list, append, and clobber. **Fix:** a per-slug lock (then `core/mutex.ts`;
  now `internal/platform/sluglock`) makes read→apply→write atomic — the role the
  Cloudflare Durable Object played. Verified by a 50-way concurrent-create test
  that asserts all 50 land. The event log additionally converges via stable
  event-ids.
- _Corrupt stored values caused permanent 500s._ **Fix:** `safeParseList` folds
  malformed JSON / non-arrays to `[]` so a slug self-heals on the next write.
- _Unbounded input._ **Fix:** `MAX_HTML_BYTES` (5 MiB) → typed 413; write
  rate-limit (token + IP) → typed 429 + `Retry-After`.

---

## Iteration 3 — Refactor to the quality bar

**Goal:** every file ≤300 lines, every function ≤50 lines, cyclomatic
complexity ≤10, no `any`, no swallowed exceptions, no dead code.

**Findings & fixes**

- The first JS prototype had a 315-line `routes/docs.js` and 90-line functions.
  **Fix:** split into layered modules with unidirectional deps (in the Go rewrite:
  `transport → service → storage`, with `internal/core` a leaf). No source file
  exceeds the line cap.
- The event-fold and `applyCommentOp` switch statements exceeded complexity 10.
  **Fix:** decomposed into category reducers (`applyContentEvent`,
  `applyStatusEvent`, `applyParentReaction`, `applyReplyEvent`) and per-op
  handlers. The complexity bound (then ESLint `complexity: [error, 10]`, now
  `golangci-lint`'s cyclomatic analyzers) passes with zero overrides.
- _Swallowed exceptions._ Replaced ad-hoc `{ error }` returns with a typed error
  hierarchy (now `internal/platform/apperr`) mapped to HTTP by one middleware; no
  handler catches and drops.
- Removed all `TODO`/`FIXME`/stray logging/commented-out code.

---

## Iteration 4 — Performance

**Goal:** find the p99 on the render hot path; meet p50 ≤ 50ms / p99 ≤ 200ms.

> TS-era measurement (autocannon against the Node build). The render path —
> single blob read + string concat + overlay inject — carries over to the Go
> rewrite, but the numbers below have been superseded; see
> [BENCHMARKS.md](./BENCHMARKS.md).

```
pnpm bench           # autocannon, 50 conns × 10s, GET /d/<slug>/v/1  (TS era)
```

**Measured (Apple M-series dev box, via tsx):** p50 **8 ms**, p99 **18 ms**,
~5,600 req/s. Both are an order of magnitude under the targets, so no hot-spot
surgery was needed — the render path is a single blob read + string concat +
overlay inject. The overlay JS is loaded once at init, not per request (the
original prototype re-read it from disk on every render — fixed by hoisting it;
the Go build embeds it via `go:embed`).

See `benchmarks` in this file's sibling section of DESIGN.md for the comparison
to the upstream Workers baseline.

---

## Iteration 5 — Developer experience

**Goal:** stand the thing up from the docs, fix every rough edge.

> Entirely TS-era. Every finding below is a Node/TypeScript toolchain wrinkle
> that the Go rewrite dissolves: there is no `node:sqlite` (storage is Postgres +
> S3), no bundler step (`go build` emits one static binary), and no separate
> test runner (`go test`). Kept for the record.

**Findings & fixes**

- _`better-sqlite3` would not build_ on Node 26 (prebuild ABI mismatch, source
  build fails). **Fix:** switched to Node 22's built-in `node:sqlite` — zero
  native build, smaller image, true `npx`/`bun` portability. Documented the
  choice in DESIGN.md.
- _`node:sqlite` broke the bundler._ esbuild/tsup rewrote `node:sqlite` to a
  bare `sqlite` import that fails at runtime. **Fix:** load it via
  `createRequire` so the bundler never sees a static specifier; verified the
  built `dist/index.js` boots and serves.
- _Vitest couldn't resolve `node:sqlite`_ on v2. **Fix:** upgraded to vitest 3
  (recognizes the builtin); contract tests run clean.
- `pnpm dev` gives full hot-reload via `tsx watch` with `--env-file=.env`, no
  Docker required.

---

## Standing invariants (Go)

These are the invariants enforced on the current **Go 1.26** codebase (the
TS-era list — ESLint `complexity ≤ 10`, `tsc --strict`, `pnpm coverage`, tsup
build, husky/commitlint — is superseded by the Go toolchain below):

- `make lint` — `golangci-lint v2` (vet, staticcheck, and the configured
  analyzers); no `// nolint` overrides.
- `make test` / `make test-race` — `go test ./...`, including the
  `go test ./internal/core/` byte-equivalence suite against `testdata/golden`.
  The pg/s3 integration suites run when the `OCTO_TEST_*` env vars are set.
- `make cover` — coverage gate over the same suite.
- `make check` — the aggregate gate (build + lint + test) CI runs.
- `make build` — `go build ./cmd/octo-doc` → a single static binary, smoke-tested
  to boot in CI's image.
