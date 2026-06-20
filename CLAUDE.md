# octo-doc — agent guide

Self-hosted, Cloudflare-free reimplementation of tdoc: prompt-native interactive
HTML docs with versioning + anchored comments. **Go 1.26**, chi router, PostgreSQL
+ S3-compatible storage. Full design in `docs/ARCHITECTURE.md` / `docs/DESIGN.md`;
the TS→Go port strategy is in `docs/PORTING.md`.

## Commands

```bash
make build        # build bin/octo-doc
make run          # run the server (needs DATABASE_URL + S3_*)
make test         # all tests (pg/s3 suites skip without OCTO_TEST_* env)
make test-race    # tests under the race detector
make cover        # coverage summary
make lint         # golangci-lint (v2)
make check        # fmt + vet + lint + test — the local gate
```

The storage + e2e suites run against real services when `OCTO_TEST_DATABASE_URL`
and `OCTO_TEST_S3_BUCKET` (+ endpoint/creds) are set; otherwise they skip. The
`Makefile` exports sensible localhost defaults (pg on :55432, MinIO on :59000).

## Architecture

Dependencies flow one way: **transport → service → storage**, with `core` a
dependency-free leaf and `platform` as cross-cutting support.

- `internal/core/` — pure domain kernel (no I/O): aid stamping, event-log fold,
  ops, reconcile, overlay injection. **Byte-equivalent port of upstream tdoc.**
- `internal/service/` — DocService, CommentService (per-slug lock), AuthService.
- `internal/transport/httpx/` — chi router, thin handlers, middleware.
- `internal/storage/` — `MetadataStore` (postgres) + `BlobStore` (s3) interfaces;
  `memory/` is a test fake. No driver type leaks past a store package.
- `internal/platform/` — `config`, `log` (slog), typed `apperr`, `sluglock`.
- `assets/overlay.js` — browser code, embedded via `go:embed`, served verbatim.

## Gotchas (these are enforced — don't fight them)

- **`internal/core/` is a byte-equivalent port and must stay that way.** Every
  change must keep the golden tests green (`go test ./internal/core/`), which
  assert parity against fixtures in `testdata/golden`. See `docs/PORTING.md` for
  the three porting traps (Math.imul 32-bit wrap, charCodeAt UTF-16 code units,
  RE2's lack of backreferences).
- **`testdata/golden` is frozen.** It was generated from the original TypeScript
  before that source was removed. Don't hand-edit fixtures.
- **`overlay.js` is the single source of truth**, embedded with `go:embed` in
  `assets/`. It is browser code — never reformat or transpile it.
- **Storage is PostgreSQL + S3 only.** There is no embedded/sqlite fallback. The
  two interfaces live in `internal/storage`; keep adapters behind them.
- **`golangci-lint` must pass with 0 issues** (`make lint`). Exported symbols
  need doc comments; unchecked errors and unclosed bodies are flagged.
- Use Conventional Commit subjects (lowercase, e.g. `fix: …`).

## Config

12-factor via env (`.env.example`). Parsed once in `internal/config`; no other
package reads the environment for app settings. Key vars: `DATABASE_URL`,
`S3_BUCKET`/`S3_ENDPOINT`/`S3_*`, `WRITE_TOKEN`, `PRIVATE`, `GITHUB_CLIENT_ID`.

## Entrypoint

`cmd/octo-doc` with subcommands: `serve` (default), `migrate`, `bootstrap`,
`health`.
