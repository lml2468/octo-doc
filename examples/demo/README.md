# octo-doc demo

A self-contained, reproducible showcase of everything octo-doc does. The demo
document is *about* octo-doc and is *served by* octo-doc — you review the product
inside the product.

It exercises the full surface:

- **Interactive HTML artifacts** — the doc renders a live SVG adoption chart with
  a Monthly/Cumulative toggle (vanilla JS, no dependencies).
- **Immutable versioning** — published as **v1**, revised as **v2** (the chart
  gains a projected series; a "Versioning" section is added), then **v3** (a new
  section answers the open anchoring question). Every version keeps a permanent URL.
- **Anchored comments** — comments stick to the exact phrase/artifact they refer
  to, and **re-anchor across versions** when you republish.
- **Threaded replies + agent verdicts** — a human comment, a threaded reply, and
  **agent replies carrying `applied` verdicts** on both threads.
- **Comment-driven editing** — the open "does it survive a full rewrite?" question
  is resolved *in the document* by v3's new section, the loop `/octo edit` models.
- **Reactions** — a 👍 on the chart thread.

## Prerequisites

A running octo-doc server. The simplest path is the local Docker stack:

```bash
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.local.yml up -d --build --wait
```

This serves the app at **http://localhost:18080** with the write token
`local-test-token` (see `docs/SELF_HOSTING.md` for production setup).

`seed.sh` drives everything through the **`octo` CLI** — it builds `./bin/octo`
from this repo on first run (so a Go toolchain is the only extra prerequisite),
then uses it for every step. No `curl` or `jq` required.

## Run

```bash
./examples/demo/seed.sh            # author + publish v1/v2/v3 and seed the threads
./examples/demo/seed.sh --reset    # remove the doc from the server first, then re-seed
```

Every step runs through `octo`, exactly as a user would drive it:

| Step | Command |
|------|---------|
| author v1, add v2/v3 | `octo new` · `octo version-add` |
| publish all versions | `octo publish` |
| anchored comments + a reply | `octo comment` (`--anchor`, `--parent`) |
| agent verdicts | `octo reply --remote --status applied` |
| reaction | `octo react` |

The docs are authored into a throwaway store (`OCTO_DIR` is a temp dir), so the
demo never touches your `~/octo-docs`.

Configuration via env (all optional):

| Var     | Default                     | Meaning                    |
|---------|-----------------------------|----------------------------|
| `BASE`  | `http://localhost:18080`    | octo-doc base URL          |
| `TOKEN` | `local-test-token`          | write bearer token         |
| `SLUG`  | `octo-demo`                 | document slug              |

A clean run prints the three shareable URLs:

```
Demo ready.
  v1 (original) : http://localhost:18080/d/octo-demo/v/1
  v2 (chart)    : http://localhost:18080/d/octo-demo/v/2
  v3 (latest)   : http://localhost:18080/d/octo-demo/v/3
```

> Publishing is **immutable and append-only** — each publish creates a new
> version. `--reset` removes the doc from the server first, so a re-seed always
> yields a clean v1/v2/v3 (without it, re-running would append v4, v5, …).

## What to try

1. Open **v3** (`/d/octo-demo/v/3`) and read the document — it explains itself.
2. **Select any sentence** to leave an anchored comment; the highlight marks the
   exact words.
3. Toggle the chart between **Monthly** and **Cumulative** — it redraws live.
4. Open the **version picker** in the toolbar and switch to **v1**; note the
   "you're viewing an older version" strip, and that the seeded comments appear on
   every version because they re-anchored.
5. Find the two review threads, both marked **`applied`**:
   - the **chart thread** — a human asks for a projected series; the agent reply is
     `applied`, with the projection now visible in v2's chart, and a 👍 on the thread.
   - the **anchoring thread** — a human asks "does it survive a full rewrite?"; the
     agent reply is `applied`, pointing at v3's new "What happens when the text is
     rewritten?" section that answers it in the document itself.

## Files

| File            | What it is                                             |
|-----------------|--------------------------------------------------------|
| `index.v1.html` | The interactive self-intro document (version 1)        |
| `index.v2.html` | The revised document (version 2: projection + versioning section) |
| `index.v3.html` | The revised document (version 3: anchoring-states section answering the review question) |
| `seed.sh`       | Authors, publishes, and seeds the whole scenario via the `octo` CLI |

All three HTML files are fully self-contained (inline CSS + JS, no external
assets), so they render offline and stay within the overlay's content-security
policy.
