# Benchmarks

Throughput/latency of the document-render hot path (`GET /d/<slug>/v/1`), the
path the non-functional targets cover.

## How to run

```bash
pnpm bench                 # autocannon, defaults: 50 conns × 10s
pnpm bench 30 100          # 30s × 100 connections
# or k6:
k6 run -e BASE=http://localhost:8080 -e SLUG=bench bench/k6.js
```

## Results

Measured on an Apple M-series dev box against the compiled `dist` build
(`node dist/index.js`), autocannon 50 connections × 8s on `GET /d/<slug>/v/1`.
Idle memory measured in a `--memory=1g --cpus=1` container via `docker stats`
(the deployment target; host `ps rss` over-counts Node's mapped runtime).

| Metric      | Target (1 vCPU/1GB) | octo-doc (measured) |
| ----------- | ------------------- | ------------------- |
| p50 latency | ≤ 50 ms             | **7 ms**            |
| p99 latency | ≤ 200 ms            | **15 ms**           |
| Throughput  | —                   | ~6,300 req/s        |
| Idle memory | ≤ 150 MB            | **21 MB** (1g/1cpu container) |

Latency is dominated by a single blob read + string concatenation + overlay
injection. The overlay JS is read once at module init (not per request), so the
render path holds no avoidable I/O.

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
| Storage round-trip         | R2/KV over CF network     | local FS / SQLite (sub-ms)      |
| Egress cost                | Cloudflare-metered        | your VPS bandwidth              |

The render output is **byte-identical** to the Worker (verified by the
`stampAids` contract test), so any rendering-time difference is purely
infrastructure, not application behavior.

## Flame graph

To profile and capture a flame graph of the hot path:

```bash
npx 0x -- node dist/index.js          # then drive load with pnpm bench
# 0x writes an interactive flamegraph.html on exit
```

The dominant frames are `BlobStore.getDoc` (fs read) and `injectOverlayCfg`
(string replace) — both expected and irreducible for a static-blob server. No
application hot-spot warranted optimization at the measured latencies.
