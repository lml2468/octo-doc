# Benchmarks

Throughput/latency of the document-render hot path (`GET /d/<slug>/v/1`), the
path the non-functional targets cover.

> **Latency/throughput numbers below are TS-era and superseded.** They were
> measured against the original Node/TypeScript build. octo-doc is now a Go 1.26
> static binary; the render path is unchanged in shape (single blob read + string
> concat + overlay inject) but the Go figures have not been re-measured here and
> the old `pnpm bench`/autocannon harness no longer exists. The size figures
> further down (binary, Docker image) reflect the **current Go build**.

## How to run

The Go build does not ship the old autocannon/k6 harness. Drive load against a
running instance with any HTTP benchmarking tool, e.g.:

```bash
# example only — point at a running `octo-doc serve`
hey  -z 10s -c 50 http://localhost:8080/d/<slug>/v/1
# or
oha  -z 10s -c 50 http://localhost:8080/d/<slug>/v/1
```

## Results (TS era — superseded)

The figures in this section were measured on an Apple M-series dev box against
the compiled Node `dist` build (`node dist/index.js`), autocannon 50 connections
× 8s on `GET /d/<slug>/v/1`. They are retained for historical context and have
**not** been re-measured for the Go build.

| Metric      | Target (1 vCPU/1GB) | TS build (measured, superseded) |
| ----------- | ------------------- | ------------------------------- |
| p50 latency | ≤ 50 ms             | **7 ms**                        |
| p99 latency | ≤ 200 ms            | **15 ms**                       |
| Throughput  | —                   | ~6,300 req/s                    |
| Idle memory | ≤ 150 MB            | **21 MB** (1g/1cpu container)   |

Latency is dominated by a single blob read + string concatenation + overlay
injection. The overlay JS is loaded once at init (not per request), so the
render path holds no avoidable I/O — this remains true in the Go build, where
the overlay is embedded via `go:embed`.

## Artifact sizes (Go build — current)

These reflect the present Go 1.26 build and are the figures to quote today:

| Artifact            | Size   | Notes                                              |
| ------------------- | ------ | -------------------------------------------------- |
| `octo-doc` binary   | ~21 MB | single static executable, `go build ./cmd/octo-doc` |
| Docker image        | ~25 MB | multi-stage distroless static (`deploy/Dockerfile`) |

The distroless image carries no shell or package manager — just the static
binary — which is both the size win and the minimal-CVE-surface win over the
previous Node-based image.

## Comparison to the upstream Cloudflare Workers baseline

The upstream tdoc Worker serves the same render path from R2 + KV at the edge.
A like-for-like latency comparison is not apples-to-apples — the Worker's
numbers are edge-PoP round-trips dominated by network geography, while octo-doc's
are a single origin you control. What's directly comparable is the **work per
request**, which is identical by construction (the same `stampAids` output is
served, the same overlay injected):

| Aspect                     | Upstream Worker           | octo-doc                        |
| -------------------------- | ------------------------- | ------------------------------- |
| Render work per request    | blob read + inject        | blob read + inject (same code)  |
| Cold start                 | ~5 ms (isolate)           | none (long-lived process)       |
| Storage round-trip         | R2/KV over CF network     | Postgres + S3 (network, often same VPC/region) |
| Egress cost                | Cloudflare-metered        | your VPS bandwidth              |

The render output is **byte-identical** to the Worker (verified by the
`go test ./internal/core/` byte-equivalence suite against `testdata/golden`), so
any rendering-time difference is purely infrastructure, not application behavior.

## Profiling

The Go build exposes the standard `net/http/pprof` and `runtime/pprof`
machinery; capture a CPU profile of the render path with `go test -cpuprofile`
on the relevant package, or scrape `/debug/pprof` from a load-driven instance,
then `go tool pprof`. The dominant work is `BlobStore.GetDoc` (the S3 fetch) and
`injectOverlayCfg` (string replace) — both expected and irreducible for a
static-blob server.
