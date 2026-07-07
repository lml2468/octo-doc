# octo-doc

**Self-hosted, prompt-native interactive HTML documents** — with text- and
artifact-anchored inline commenting and immutable versioning. A self-hosted,
single-binary reimplementation of [tdoc](https://github.com/serenakeyitan/tdoc):
same document model, same URLs, same [agent skill](https://github.com/lml2468/octo-doc-skill)
contract — now a single static Go binary.

> Credit to Serena Keyitan's **tdoc** and, upstream of that, Jesse Pollak's
> *bdocs* concept. octo-doc keeps the product identical and makes it something you
> run yourself: a static binary backed by PostgreSQL and any S3-compatible store.

## Why

octo-doc gives you prompt-native, commentable documents as a plain HTTP server
you own: one static binary, PostgreSQL for metadata, and any S3-compatible store
for blobs. `docker compose up -d` and you're live — no managed platform, no
vendor account.

## Quick start

```bash
git clone https://github.com/Mininglamp-OSS/octo-doc && cd octo-doc

# Full stack (app + PostgreSQL + MinIO + Caddy auto-TLS):
DOMAIN=docs.example.com docker compose -f deploy/docker-compose.yml up -d --wait

# Mint a write token (one-shot), then publish:
TOKEN=$(curl -sX POST http://localhost:8080/v1/admin/bootstrap | jq -r .data.token)
curl -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"slug":"hello","html":"<html><body><h1>Hi</h1></body></html>"}' \
  http://localhost:8080/v1/docs
#   → { "data": { "url": "/d/hello/v/1", "version": 1, ... } }
open http://localhost:8080/d/hello/v/1
```

Full guide: **[docs/SELF_HOSTING.md](docs/SELF_HOSTING.md)** ($5 VPS in 15 min).

## How it works

| | |
| --- | --- |
| **Document** | `slug` + monotonically increasing `version` → immutable HTML |
| **URL** | `/d/<slug>/v/<version>` (preserved from tdoc) |
| **Comments** | append-only event log; every version is a snapshot |
| **Artifacts** | each commentable element is stamped `data-odoc-aid="<hash>"` so comments anchor by identity, not DOM position — the aid **hash** stays byte-identical to upstream (only the attribute name is octo-doc-native) |
| **Auth** | private by default: the write token = author; a per-doc share **code** grants read+comment. See [docs/AUTH.md](docs/AUTH.md) |
| **Storage** | PostgreSQL (metadata) + S3-compatible (blobs) behind two interfaces |
| **Scaling** | stateless app; run N replicas behind a load balancer — per-slug writes serialize via PostgreSQL advisory locks |

Architecture, data model, and the full API spec:
**[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)**. Design rationale, threat model,
backup/upgrade: **[docs/DESIGN.md](docs/DESIGN.md)**. How the Go port preserves
byte-equivalence with upstream tdoc: **[docs/PORTING.md](docs/PORTING.md)**.

## Running multiple instances

The app holds no local state, so you can run as many replicas as you like behind
a load balancer — they share the same PostgreSQL and S3. Concurrent writes to the
**same** document (publishing a new version, or a comment's read-modify-write) are
serialized **across instances** by a per-slug **PostgreSQL advisory lock**, so two
replicas can't resolve to the same version number or clobber each other's blob.
The first-token `bootstrap` is guarded the same way, so it stays one-shot cluster-wide.

Operationally there's nothing to configure — it's on by default. Just size the
database: each instance opens **two** connection pools (one for queries, one for
the advisory locks), so plan for up to `2 × PG_POOL_MAX × replicas` connections
against Postgres and set its `max_connections` accordingly.

## Agent skill

The companion **[octo-doc-skill](https://github.com/lml2468/octo-doc-skill)** repo
holds the Claude Code / Codex skill that turns a prompt into a self-contained
interactive HTML doc and publishes it here. The skill is a thin authoring layer
over the **`octo` client CLI** (built from this repo — see below):

```bash
export OCTO_BASE_URL="https://docs.example.com"
export OCTO_TOKEN="$(octo-doc bootstrap)"   # or POST /v1/admin/bootstrap
/octo new "an interactive explainer of compound interest"  # draft
/octo publish my-explainer                   # → https://docs.example.com/d/my-explainer/v/1
/octo share my-explainer                     # → a read+comment ?code= link
```

### The `octo` client CLI

`cmd/octo` is a second, self-contained binary: the agent-side client. Authoring is
**remote-first** — a doc lives on the server from creation as a mutable draft;
`octo publish` promotes the draft to an immutable version. It links no database or
blob store (only the pure `core` kernel + embedded `overlay.js`), and there is no
local preview server: creating and previewing happen against a running octo-doc
server (the local docker stack counts).

```bash
make build-octo                                        # build bin/octo
export OCTO_BASE_URL=https://docs.example.com OCTO_TOKEN=<write-token>
octo new --slug demo --title "Demo" --html-file demo.html --open  # save + open the draft
octo version-add --slug demo --html-file demo2.html    # iterate the draft
octo publish demo                                      # promote draft → immutable v1
octo share demo                                        # mint a read+comment ?code= link
octo pull demo                                          # merge server comments to disk
octo doctor                                            # check the CLI + the server
octo update                                             # self-update from GitHub Releases
```

Config resolves from `OCTO_BASE_URL` / `OCTO_TOKEN` / `OCTO_CODE` / `OCTO_DIR`,
then `~/.octo/config.json`.
Prebuilt binaries for macOS/Linux/Windows are attached to each
[GitHub Release](https://github.com/Mininglamp-OSS/octo-doc/releases); `octo update`
downloads and checksum-verifies the matching one.

## Configuration

12-factor; every knob is an env var (see **[.env.example](.env.example)**).
Highlights:

| Var | Default | Purpose |
| --- | ------- | ------- |
| `DATABASE_URL` | _(required)_ | PostgreSQL connection string |
| `PG_POOL_MAX` | `10` | max connections **per pool**; the app keeps two (queries + advisory locks), so total ≤ `2×` this |
| `S3_BUCKET` / `S3_ENDPOINT` | `octo-doc` / _(AWS)_ | blob store (MinIO/R2: set endpoint + `S3_FORCE_PATH_STYLE=1`) |
| `WRITE_TOKEN` | _(bootstrap)_ | static write token = author; else POST `/v1/admin/bootstrap` |
| `FRAME_ANCESTORS` | `'none'` | CSP embedding policy for rendered docs |
| `MAX_HTML_BYTES` | `5242880` | per-document size cap |

Docs are **private by default** — access is per-document via share codes, not a
global flag (see [docs/AUTH.md](docs/AUTH.md)).

## Commands

The server binary (`cmd/octo-doc`):

```bash
octo-doc serve       # run the HTTP server (default)
octo-doc migrate     # apply the database schema (idempotent)
octo-doc bootstrap   # mint and print the first write token
octo-doc health      # local healthcheck (used by the container)
```

The agent client binary (`cmd/octo`) — see [Agent skill](#agent-skill):

```bash
octo new | publish | share | pull | unpublish | list | fork | version-add | comment | react | reply | doctor | update
```

## Development

Go 1.26, [chi](https://github.com/go-chi/chi) router, [pgx](https://github.com/jackc/pgx),
[aws-sdk-go-v2](https://github.com/aws/aws-sdk-go-v2). Layered
**transport → service → storage** with a dependency-free `core` kernel.

```bash
make build        # build bin/octo-doc (server)
make build-octo   # build bin/octo (agent client)
make release-octo # cross-compile octo for all platforms + SHA256SUMS
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
