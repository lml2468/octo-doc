<div align="center">

# octo-doc

**Self-hosted, prompt-native interactive HTML documents — with anchored inline commenting and immutable versioning.**

[![CI](https://github.com/lml2468/octo-doc/actions/workflows/ci.yml/badge.svg)](https://github.com/lml2468/octo-doc/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/lml2468/octo-doc?sort=semver)](https://github.com/lml2468/octo-doc/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/lml2468/octo-doc.svg)](https://pkg.go.dev/github.com/lml2468/octo-doc)
[![Go Report Card](https://goreportcard.com/badge/github.com/lml2468/octo-doc)](https://goreportcard.com/report/github.com/lml2468/octo-doc)
[![Go 1.26](https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[Quick start](#quick-start) ·
[How it works](#how-it-works) ·
[Configuration](#configuration) ·
[Self-hosting](docs/SELF_HOSTING.md) ·
[Architecture](docs/ARCHITECTURE.md) ·
[Contributing](CONTRIBUTING.md)

</div>

---

octo-doc turns a prompt into a self-contained interactive HTML document — models,
SVG diagrams, simulations, explainers, RFCs — publishes it at a stable URL, and
lets reviewers leave comments anchored to the **text or the artifact** they're
looking at. Every publish is an immutable version; comments re-anchor across
versions. It runs as a single static Go binary backed by PostgreSQL and any
S3-compatible object store — `docker compose up -d` and you own the whole stack.

## Features

- **Prompt-native documents.** Author with an AI agent via the companion
  [skill](#agent-workflow); the doc is real, self-contained HTML — not a
  proprietary format.
- **Anchored commenting.** Comments attach to highlighted text *or* to a stamped
  artifact (image, SVG, canvas, chart) by content-hash identity, so they survive
  edits and re-anchor across versions.
- **Immutable versioning.** `slug` + monotonic `version` → write-once HTML at
  `/d/<slug>/v/<n>`; the comment history is an append-only event log.
- **Private by default.** The write token is the author; a per-doc share **code**
  grants read + comment. No credential → `404` (existence hidden). See
  [docs/AUTH.md](docs/AUTH.md).
- **Self-hosted, no vendor lock-in.** PostgreSQL for metadata, S3-compatible for
  blobs, both behind narrow interfaces. One static binary, no runtime deps.
- **Horizontally scalable.** Stateless app; run N replicas — per-slug writes
  serialize cluster-wide via PostgreSQL advisory locks.

## Quick start

Bring up the full stack (app + PostgreSQL + MinIO + Caddy auto-TLS) and publish a
document:

```bash
git clone https://github.com/lml2468/octo-doc && cd octo-doc

# App + PostgreSQL + MinIO + Caddy (automatic TLS from DOMAIN):
DOMAIN=docs.example.com docker compose -f deploy/docker-compose.yml up -d --wait

# Mint the first write token (one-shot), then publish a doc:
TOKEN=$(curl -sX POST http://localhost:8080/v1/admin/bootstrap | jq -r .data.token)
curl -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"slug":"hello","html":"<html><body><h1>Hi</h1></body></html>"}' \
  http://localhost:8080/v1/docs
#   → { "data": { "url": "/d/hello/v/1", "version": 1, ... } }

open http://localhost:8080/d/hello/v/1
```

Going to production on a \$5 VPS in ~15 minutes: **[docs/SELF_HOSTING.md](docs/SELF_HOSTING.md)**.

## How it works

| Concept | Detail |
| --- | --- |
| **Document** | `slug` + monotonically increasing `version` → immutable HTML blob |
| **URL** | `/d/<slug>/v/<version>` |
| **Comments** | append-only event log; each version renders a folded snapshot |
| **Artifacts** | every commentable element is stamped `data-odoc-aid="<hash>"` so comments anchor by identity, not DOM position |
| **Auth** | private by default — write token = author, per-doc share **code** = read + comment ([docs/AUTH.md](docs/AUTH.md)) |
| **Storage** | PostgreSQL (metadata) + S3-compatible (blobs), behind `MetadataStore` / `BlobStore` interfaces |
| **Scaling** | stateless app; concurrent same-slug writes serialize via PostgreSQL advisory locks |

Dependencies flow one way — **transport → service → storage** — around a
dependency-free `core` kernel. Full design, data model, and the `/v1` API spec are
in **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)**; rationale, threat model, and
backup/upgrade in **[docs/DESIGN.md](docs/DESIGN.md)**.

## Agent workflow

octo-doc is **API-first**: an agent turns a prompt into a self-contained HTML
document and drives the doc lifecycle over the versioned `/v1` API. Authoring is
**remote-first** — a doc lives on the server from creation as a mutable draft, and
promoting the draft mints an immutable version. A dedicated client
(`octo-cli`, packaged separately) wraps these calls, but the API is the contract:

```bash
export BASE=https://docs.example.com
export TOKEN=<write-token>   # from: octo-doc bootstrap  (or POST /v1/admin/bootstrap)

# Save HTML as a private draft, then promote it to an immutable version:
curl -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"slug":"explainer","html":"<html><body><h1>Hi</h1></body></html>"}' \
  "$BASE/v1/docs"                                    # → /d/explainer/v/1

# Mint a per-doc read + comment share code:
curl -H "Authorization: Bearer $TOKEN" -X POST "$BASE/v1/docs/explainer/share"
#   → { "data": { "code": "…", "url": ".../d/explainer/v/1?code=…" } }
```

See **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** for the request lifecycle and
the full `/v1` surface (docs, drafts, comments, reactions, assets, share).

## Configuration

12-factor: every knob is an environment variable (full list in
**[.env.example](.env.example)**). The essentials:

| Variable | Default | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | _(required)_ | PostgreSQL connection string |
| `S3_BUCKET` / `S3_ENDPOINT` | `octo-doc` / _(AWS)_ | blob store — for MinIO/R2 set the endpoint + `S3_FORCE_PATH_STYLE=1` |
| `WRITE_TOKEN` | _(bootstrap)_ | static author token; if unset, mint one via `POST /v1/admin/bootstrap` |
| `PG_POOL_MAX` | `10` | max connections **per pool**; the app keeps two (queries + advisory locks) |
| `FRAME_ANCESTORS` | `'none'` | CSP embedding policy for rendered docs |
| `MAX_HTML_BYTES` | `5242880` | per-document size cap (5 MiB) |

The server binary `cmd/octo-doc` exposes four subcommands:

```bash
octo-doc serve       # run the HTTP server (default)
octo-doc migrate     # apply the database schema (idempotent)
octo-doc bootstrap   # mint and print the first write token
octo-doc health      # local healthcheck (used by the container)
```

## Development

Go 1.26 · [chi](https://github.com/go-chi/chi) router ·
[pgx](https://github.com/jackc/pgx) · [aws-sdk-go-v2](https://github.com/aws/aws-sdk-go-v2).

```bash
make build        # build bin/octo-doc (server)
make test         # all tests (pg/s3 suites skip without OCTO_TEST_* env)
make check        # fmt + vet + lint + test — the local gate
```

The `core` kernel (artifact stamping, the comment event-log fold, anchor
reconciliation) is a **byte-equivalent port** verified against golden fixtures in
`testdata/golden`; keep `go test ./internal/core/` green. To run the storage and
e2e suites against real services, start PostgreSQL + MinIO and export the
`OCTO_TEST_*` variables (see the `Makefile` defaults).

See **[CONTRIBUTING.md](CONTRIBUTING.md)** before opening a pull request, and
**[CHANGELOG.md](CHANGELOG.md)** for release notes. All participation is governed
by our **[Code of Conduct](CODE_OF_CONDUCT.md)**.

## Security

Please report vulnerabilities privately — see the **[Security Policy](SECURITY.md)**.
Do not open a public issue for security reports. Operator hardening guidance is in
the [production checklist](docs/SELF_HOSTING.md), and the access-control model is
documented in [docs/AUTH.md](docs/AUTH.md).

## Credits

octo-doc is a self-hosted reimplementation of Serena Keyitan's
[tdoc](https://github.com/serenakeyitan/tdoc) — and, upstream of that, Jesse
Pollak's *bdocs* concept. It keeps the product identical and makes it something you
run yourself.

## License

Released under the [MIT License](LICENSE).
