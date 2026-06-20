# octo-doc

**Self-hosted, prompt-native interactive HTML documents** — with text- and
artifact-anchored inline commenting and immutable versioning. A Cloudflare-free
reimplementation of [tdoc](https://github.com/serenakeyitan/tdoc): same document
model, same URLs, same [agent skill](https://github.com/lml2468/octo-doc-skill)
contract — now a single static Go binary.

> Credit to Serena Keyitan's **tdoc** and, upstream of that, Jesse Pollak's
> *bdocs* concept. octo-doc keeps the product identical and removes the vendor
> coupling: no Workers, no KV, no D1, no R2, no Durable Objects.

## Why

tdoc is great but ties publishing to a Cloudflare account (`wrangler login`, R2,
KV, a claimed subdomain, a DO migration). octo-doc gives you the same product as
a plain HTTP server you own: one static binary, PostgreSQL for metadata, and any
S3-compatible store for blobs. `docker compose up -d` and you're live.

## Quick start

```bash
git clone https://github.com/Mininglamp-OSS/octo-doc && cd octo-doc

# Full stack (app + PostgreSQL + MinIO + Caddy auto-TLS):
DOMAIN=docs.example.com docker compose -f deploy/docker-compose.yml up -d --wait

# Mint a write token (one-shot), then publish:
TOKEN=$(curl -s http://localhost:8080/api/admin/bootstrap | jq -r .token)
curl -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"slug":"hello","html":"<html><body><h1>Hi</h1></body></html>"}' \
  http://localhost:8080/api/docs
#   → { "ok": true, "url": "/d/hello/v/1", ... }
open http://localhost:8080/d/hello/v/1
```

Full guide: **[docs/SELF_HOSTING.md](docs/SELF_HOSTING.md)** ($5 VPS in 15 min).

## How it works

| | |
| --- | --- |
| **Document** | `slug` + monotonically increasing `version` → immutable HTML |
| **URL** | `/d/<slug>/v/<version>` (preserved from tdoc) |
| **Comments** | append-only event log; every version is a snapshot |
| **Artifacts** | each commentable element is stamped `data-tdoc-aid="<hash>"` so comments anchor by identity, not DOM position — **byte-identical to upstream** |
| **Auth** | Bearer token for writes; reads public by default (`PRIVATE=1` to lock) |
| **Storage** | PostgreSQL (metadata) + S3-compatible (blobs) behind two interfaces |

Architecture, data model, and the full API spec:
**[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)**. Design rationale, threat model,
backup/upgrade: **[docs/DESIGN.md](docs/DESIGN.md)**. How the Go port preserves
byte-equivalence with upstream tdoc: **[docs/PORTING.md](docs/PORTING.md)**.

## Agent skill

The companion **[octo-doc-skill](https://github.com/lml2468/octo-doc-skill)** repo
holds the Claude Code / Codex skill that turns a prompt into a self-contained
interactive HTML doc and publishes it here:

```bash
export TDOC_BASE_URL="https://docs.example.com"
export TDOC_TOKEN="$(octo-doc bootstrap)"   # or GET /api/admin/bootstrap
/tdoc new "an interactive explainer of compound interest"
/tdoc publish my-explainer                   # → https://docs.example.com/d/my-explainer/v/1
```

The skill is agent-side tooling and ships separately from this server.

## Configuration

12-factor; every knob is an env var (see **[.env.example](.env.example)**).
Highlights:

| Var | Default | Purpose |
| --- | ------- | ------- |
| `DATABASE_URL` | _(required)_ | PostgreSQL connection string |
| `S3_BUCKET` / `S3_ENDPOINT` | `octo-doc` / _(AWS)_ | blob store (MinIO/R2: set endpoint + `S3_FORCE_PATH_STYLE=1`) |
| `WRITE_TOKEN` | _(bootstrap)_ | static write token; else `/api/admin/bootstrap` |
| `PRIVATE` | `false` | require the token for reads too |
| `FRAME_ANCESTORS` | `'none'` | CSP embedding policy for rendered docs |
| `MAX_HTML_BYTES` | `5242880` | per-document size cap |
| `GITHUB_CLIENT_ID` | _(off)_ | enable GitHub sign-in for per-user comments |

## Commands

```bash
octo-doc serve       # run the HTTP server (default)
octo-doc migrate     # apply the database schema (idempotent)
octo-doc bootstrap   # mint and print the first write token
octo-doc health      # local healthcheck (used by the container)
```

## Development

Go 1.26, [chi](https://github.com/go-chi/chi) router, [pgx](https://github.com/jackc/pgx),
[aws-sdk-go-v2](https://github.com/aws/aws-sdk-go-v2). Layered
**transport → service → storage** with a dependency-free `core` kernel.

```bash
make build        # build bin/octo-doc
make test         # all tests (pg/s3 suites skip without OCTO_TEST_* env)
make test-race    # tests under the race detector
make cover        # coverage summary
make lint         # golangci-lint
make check        # fmt + vet + lint + test (the local gate)
```

To run the storage and e2e suites against real services, start PostgreSQL +
MinIO and export `OCTO_TEST_DATABASE_URL`, `OCTO_TEST_S3_BUCKET`,
`OCTO_TEST_S3_ENDPOINT`, `OCTO_TEST_S3_ACCESS_KEY_ID`,
`OCTO_TEST_S3_SECRET_ACCESS_KEY` (see the `Makefile` defaults).

The comment engine and artifact stamper are ported byte-for-byte from upstream
tdoc and verified against golden fixtures in `testdata/golden`
([docs/PORTING.md](docs/PORTING.md)). Contributions: see
**[CONTRIBUTING.md](CONTRIBUTING.md)**.

## License

See [LICENSE](LICENSE).
