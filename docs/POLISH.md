# POLISH.md — local polish protocol log

A record of the five polish iterations run against octo-doc, each with what was
found and what changed. The discipline: run it locally, look hard, fix the real
thing (no TODOs left behind), re-run.

---

## Iteration 1 — Walk the happy path

**Goal:** publish a single file, open it, see the content.

```
node dist/index.js & TOKEN=$(curl -s localhost:8080/api/admin/bootstrap | jq -r .token)
curl -H "Authorization: Bearer $TOKEN" -F file=@fixtures/hello.html -F slug=hello localhost:8080/api/docs
open localhost:8080/d/hello/v/1
```

**Findings & fixes**

- _Adapter selection silently wrong._ `makeStores` read `env.STORAGE` but was
  passed the parsed config object (`config.storage`), so `STORAGE=postgres+s3`
  fell back to `sqlite+fs` with no error. This is exactly the "偷渡 adapter
  default" failure mode. **Fix:** the storage factory now takes the typed
  `Config` and reads `config.storage`; adapters read their own env explicitly.
  Caught a second time in the TS rewrite and locked down with the adapter-swap
  contract test running against real Postgres+S3 in CI.
- _Version auto-increment unclear from the response._ Added `version`, `aids`,
  and `mergedComments` to the publish response so the walk is self-explaining.

---

## Iteration 2 — Harden (inject faults)

**Goal:** kill the process mid-publish, hammer the same slug, send junk.

**Findings & fixes**

- _Half-written documents on crash._ A naive `writeFileSync(index.html)` can
  leave a truncated file if the process dies mid-write. **Fix:** the FS blob
  store writes to a temp file and `rename`s into place (atomic on POSIX). The
  `listVersions` scan also ignores a `v<N>` dir whose `index.html` isn't present
  yet, so an in-flight publish is never advertised. Verified by the chaos test
  (`test/e2e/lifecycle.test.ts`): fire 8 concurrent publishes, `SIGKILL`,
  restart, assert every reported version is readable and **no `.tmp` files leak**.
- _Lost updates under concurrent same-slug comments._ Two writers could read the
  same list, append, and clobber. **Fix:** a per-slug async mutex
  (`core/mutex.ts`) makes read→apply→write atomic — the role the Cloudflare
  Durable Object played. Verified by a 50-way concurrent-create test that asserts
  all 50 land. The event log additionally converges via stable event-ids.
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
  **Fix:** split into `routes → services → adapters` with unidirectional deps.
  No source file now exceeds 300 lines.
- The event-fold and `applyCommentOp` switch statements exceeded complexity 10.
  **Fix:** decomposed into category reducers (`applyContentEvent`,
  `applyStatusEvent`, `applyParentReaction`, `applyReplyEvent`) and per-op
  handlers. ESLint's `complexity: [error, 10]` rule now passes with zero
  overrides.
- _Swallowed exceptions._ Replaced ad-hoc `{ error }` returns with a typed
  `AppError` hierarchy mapped to HTTP by one middleware; no route catches and
  drops.
- Removed all `TODO`/`FIXME`/`console.log`/commented-out code. `no-console` is
  an ESLint error in `src/` (entrypoints excepted).

---

## Iteration 4 — Performance

**Goal:** find the p99 on the render hot path; meet p50 ≤ 50ms / p99 ≤ 200ms.

```
pnpm bench           # autocannon, 50 conns × 10s, GET /d/<slug>/v/1
```

**Measured (Apple M-series dev box, via tsx):** p50 **8 ms**, p99 **18 ms**,
~5,600 req/s. The compiled `dist` build is faster still (no TS transform). Both
are an order of magnitude under the targets, so no hot-spot surgery was needed —
the render path is a single blob read + string concat + overlay inject. The
overlay JS is read once at module init, not per request (the original prototype
re-read it from disk on every render — fixed here by hoisting to a module
constant).

See `benchmarks` in this file's sibling section of DESIGN.md for the comparison
to the upstream Workers baseline.

---

## Iteration 5 — Developer experience

**Goal:** stand the thing up from the docs, fix every rough edge.

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

## Standing invariants (enforced, not aspirational)

- `pnpm lint` — ESLint type-checked rules + `complexity ≤ 10` + `no-console`.
- `pnpm typecheck` — `tsc --noEmit`, strict, `noUncheckedIndexedAccess`,
  `exactOptionalPropertyTypes`, no `any`.
- `pnpm coverage` — 85% lines/statements/functions gate (actual ~94%).
- `pnpm build` — tsup → `dist`, asserted to boot in CI's image smoke-test.
- Husky pre-commit runs lint-staged + `tsc`; commit-msg runs commitlint
  (Conventional Commits).
