# octo-doc — agent guide

Self-hosted, Cloudflare-free reimplementation of tdoc: prompt-native interactive
HTML docs with versioning + anchored comments. Node 22, TypeScript strict, Hono.
Full design in `docs/ARCHITECTURE.md` / `docs/DESIGN.md`.

## Commands (pnpm + Node 22)

```bash
pnpm install
pnpm dev          # hot-reload server (tsx watch + .env)
pnpm test         # vitest: unit + contract + integration + e2e
pnpm coverage     # tests + 85% gate (CI uses this)
pnpm lint         # eslint, type-checked, complexity ≤ 10
pnpm typecheck    # tsc --noEmit, strict, no any
pnpm build        # tsup → dist
pnpm bench        # autocannon render-path benchmark
```

Tests are **vitest**, not node:test. A free port is auto-picked, so e2e/bench
never collide with a running dev server.

## Architecture

Dependencies flow one way: **routes → services → adapters**.

- `src/core/` — pure domain kernel (no I/O): aid stamping, event-log fold, ops.
- `src/services/` — DocService, CommentService (per-slug mutex), AuthService.
- `src/routes/` + `src/middleware/` — thin HTTP; logic lives in services.
- `src/storage/` — `{ MetadataStore, BlobStore }` adapters (sqlite+fs default,
  postgres+s3 optional), selected by `STORAGE`. No driver type leaks to routes.
- Cross-cutting: `config.ts`, `logger.ts`, typed `errors.ts` (one error→HTTP map).

Import across module boundaries through each dir's `index.ts` barrel.

## Gotchas (these are enforced — don't fight them)

- **`src/core/` is ported verbatim from upstream tdoc and must stay
  byte-equivalent.** `stampAids` + the comment fold have a contract test
  (`test/unit/stamp.test.ts`) asserting parity with `worker.js`. Refactor for
  clarity only if the test still passes.
- **`node:sqlite` is loaded via `createRequire`** (`src/storage/sqlite.ts`) so
  tsup/esbuild don't rewrite it to a bare `sqlite` import that fails at runtime.
  Don't change it to a static `import`.
- **Quality bar is linted:** files ≤ 300 lines, functions ≤ 50, cyclomatic
  complexity ≤ 10, no `any`, no `console.*` in `src/`. `pnpm lint` fails on these.
- **Commits go through commitlint** (husky): Conventional Commits, **lowercase
  subject** (e.g. `fix: …` not `Fix: …`). Pre-commit also runs lint-staged + tsc.
- **`overlay.js` is browser code served verbatim** — not transpiled, excluded
  from eslint/tsc. `core/render.ts` reads it once at module load.
- Optional adapters (`pg`, `@aws-sdk/client-s3`) are dynamic-imported; the
  default sqlite+fs stack needs neither installed.

## Config

12-factor via env (`.env.example`). Parsed once in `config.ts`; no other module
reads `process.env` for app settings. Key vars: `STORAGE`, `WRITE_TOKEN`,
`PRIVATE`, `GITHUB_CLIENT_ID`, `DATA_DIR`.
