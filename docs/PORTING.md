# Porting notes: TypeScript → Go

octo-doc's domain kernel (`internal/core`) is a verbatim-equivalent port of the
upstream Cloudflare Worker's logic (originally TypeScript). The success criterion
is **byte-equivalence**: the same input HTML must produce the same stamped output,
and the same event log must fold to the same snapshot. This document records how
that equivalence is guaranteed and the subtle traps the port had to clear.

## The golden-fixture safety net

Before any TypeScript was removed, a generator ran the *original* TS functions
over a battery of inputs and recorded the outputs under `testdata/golden`:

- `stamp/` — `<name>.in.html` → `<name>.out.html` (+ `.aids.json`)
- `cyrb53.json` — input string + seed → hash
- `fold/`, `ops/`, `reconcile/` — input event logs → folded snapshot JSON

The Go tests in `internal/core/*_test.go` replay these fixtures and assert:

- **Byte-equivalence** for `StampAids` and `Cyrb53` (pure, fully deterministic).
- **Logical equivalence** (structural JSON match) for the fold/ops/reconcile
  paths, because one-shot event ids (`EventEID`) are intentionally
  non-deterministic — but they never appear in a folded snapshot.

The fixtures are **frozen**: upstream `core` logic is considered stable, so the
golden set is a one-time parity baseline, permanently enforced in CI. To
regenerate them you need the archived TypeScript reference (kept outside this
repo); the generator script was removed when the TS source was deleted.

## The three traps

These are the places where a naive Go translation would silently diverge. Each
has a dedicated golden case.

### 1. `Math.imul` — 32-bit wrap-around multiply

`cyrb53` multiplies with JavaScript's `Math.imul`, which is C-style 32-bit
integer multiplication that wraps modulo 2³². Go's `uint32` multiplication wraps
identically, so `imul(a, b uint32) uint32 { return a * b }` is exact. The final
mix assembles a 53-bit value (`4294967296*(2097151&h2) + (h1>>>0)`) — computed in
`uint64` and base36-encoded to match `Number.prototype.toString(36)`.

### 2. `charCodeAt` — UTF-16 code units, not bytes or runes

`cyrb53` iterates `str.length` via `charCodeAt(i)`, i.e. **UTF-16 code units**. A
Go `string` is UTF-8 bytes and `range` yields runes — both differ from UTF-16 for
any non-ASCII text. The port first encodes to `[]uint16` with
`utf16.Encode([]rune(s))` and iterates that. Golden cases include CJK, emoji, and
astral-plane characters (surrogate pairs) to lock this down.

### 3. RE2 has no backreferences

The TS heading scanner used `</h\1>` (a backreference), which Go's `regexp` (RE2)
cannot compile. `collectHeadings` instead loops the three heading levels and
pairs open/close tags by hand. Several other `stamp.ts` regexes used a manual
`lastIndex` cursor; these became explicit scanning loops in `stamp.go`.

A bonus invariant makes the offset arithmetic safe: every structural delimiter
`StampAids` keys on (`<`, `>`, tag names, attribute quotes) is ASCII, so byte
offsets land on the same boundaries JavaScript's UTF-16 offsets would. Sliced
content is therefore identical bytes and hashes identically. The only place
UTF-16 length still matters is the 80-unit `head` excerpt, handled by
`utf16Slice`.

## Deliberate divergences

- **`EventEID` for one-shot events.** Upstream used `Math.random()`; the Go port
  uses an atomic counter + high-resolution time. This only affects the uniqueness
  suffix of non-idempotent event ids, never the fold result (`DedupEvents` keys on
  the id; idempotent events keep their deterministic ids).
- **Storage is PostgreSQL + S3 only.** The upstream KV/R2 split maps to
  `MetadataStore` (pgx/JSONB) and `BlobStore` (aws-sdk-go-v2). The SQLite+FS
  reference adapters were not ported.
