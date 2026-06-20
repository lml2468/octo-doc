# octo-doc

**Self-hosted, prompt-native interactive HTML documents** — with text- and
artifact-anchored inline commenting and immutable versioning. A Cloudflare-free
reimplementation of [tdoc](https://github.com/serenakeyitan/tdoc): same document
model, same URLs, same `/tdoc` skill contract — runs anywhere Node 22 runs.

> Credit to Serena Keyitan's **tdoc** and, upstream of that, Jesse Pollak's
> *bdocs* concept. octo-doc keeps the product identical and removes the vendor
> coupling: no Workers, no KV, no D1, no R2, no Durable Objects.

## Why

tdoc is great but ties publishing to a Cloudflare account (`wrangler login`, R2,
KV, a claimed subdomain, a DO migration). octo-doc gives you the same thing as a
plain HTTP server you own — `docker compose up -d` on a $5 VPS, or `npx octo-doc`
locally. Storage is a pluggable `{ MetadataStore, BlobStore }`: SQLite + local
files by default, PostgreSQL + S3/MinIO when you want to scale — **swappable with
one env var, zero code changes.**

## Quick start

```bash
# Docker (app + Caddy auto-TLS):
git clone https://github.com/lml2468/octo-doc && cd octo-doc
DOMAIN=docs.example.com docker compose -f deploy/docker-compose.yml up -d --wait

# …or zero-Docker, zero-build (Node 22+, built-in SQLite):
npx octo-doc

# get a write token (one-shot), then publish:
TOKEN=$(curl -s http://localhost:8080/api/admin/bootstrap | jq -r .token)
curl -H "Authorization: Bearer $TOKEN" \
  -F file=@fixtures/hello.html -F slug=hello \
  http://localhost:8080/api/docs
#   → { "url": "/d/hello/v/1", ... }
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
| **Auth** | Bearer token for writes; reads public by default (`--private` to lock) |
| **Storage** | `STORAGE=sqlite+fs` (default) or `postgres+s3` — pluggable adapters |

Architecture, data model, and the full API spec:
**[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)**. Design rationale, threat model,
backup/upgrade: **[docs/DESIGN.md](docs/DESIGN.md)**.

## The `/tdoc` skill

The agent skill (`skill/`) keeps tdoc's exact command surface — `/tdoc new`,
`edit`, `publish`, `pull`, `list`, `fork`, `doctor`, `unpublish` — but targets
your server via two env vars instead of Cloudflare:

```bash
export TDOC_BASE_URL="https://docs.example.com"
export TDOC_TOKEN="<write token>"
/tdoc publish my-doc        # → https://docs.example.com/d/my-doc/v/1
```

Coming from Cloudflare? **[docs/MIGRATING_FROM_WORKERS.md](docs/MIGRATING_FROM_WORKERS.md)**
imports your KV/R2 docs and comments with no data loss.

## Configuration

12-factor; every knob is an env var (see **[.env.example](.env.example)**).
Highlights:

| Var | Default | Purpose |
| --- | ------- | ------- |
| `STORAGE` | `sqlite+fs` | `sqlite\|postgres` × `fs\|s3` |
| `WRITE_TOKEN` | _(bootstrap)_ | static write token; else `/api/admin/bootstrap` |
| `PRIVATE` | `false` | require the token for reads too |
| `FRAME_ANCESTORS` | `'none'` | CSP embedding policy for rendered docs |
| `MAX_HTML_BYTES` | `5242880` | per-document size cap |
| `GITHUB_CLIENT_ID` | _(off)_ | enable GitHub sign-in for per-user comments |

## Development

```bash
pnpm install
pnpm dev            # hot-reload server (Node --watch)
pnpm test           # unit tests (node:test)
pnpm test:e2e       # publish → pull → v2 → list-versions, < 1s
pnpm bench          # autocannon latency/throughput
pnpm lint && pnpm typecheck
```

CI (`.github/workflows/ci.yml`) runs lint, typecheck, unit, E2E on **both**
storage stacks (sqlite+fs and postgres+s3 via service containers), builds the
builds the Docker image (slim Alpine multi-stage, no dev/optional deps —
~55 MB compressed), and pushes to `ghcr.io` on `main`.

## License

MIT.
