# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
adhere to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- **Rewrote the server in Go** (was TypeScript/Node). The domain kernel
  (`internal/core`) is a byte-equivalent port verified against golden fixtures;
  see [docs/PORTING.md](docs/PORTING.md). The server is now a single static
  binary with no runtime dependencies.
- **Unified storage on PostgreSQL + S3-compatible object storage.** The previous
  pluggable `sqlite+fs` / `postgres+s3` selection was removed; PostgreSQL (via
  pgx) and an S3-compatible store (via aws-sdk-go-v2) are now required.
- `overlay.js` is embedded into the binary with `go:embed` (single source of
  truth, no runtime path lookup).
- The Docker image is now a distroless static build (rootless).

### Removed

- The TypeScript implementation, Node tooling (pnpm, tsup, vitest, eslint), and
  the bundled `/tdoc` agent skill (extracted to a separate repository).

### Added

- `make` targets for the full local quality gate; `golangci-lint` configuration.
- A reusable storage contract suite run against the in-memory, PostgreSQL, and S3
  implementations, plus a full-lifecycle e2e test against live PostgreSQL + MinIO.
- `octo-doc health` subcommand for container healthchecks.
