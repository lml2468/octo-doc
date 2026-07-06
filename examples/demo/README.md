# octo-doc demo

A self-contained showcase of everything octo-doc does. The demo document is
*about* octo-doc and is *served by* octo-doc — you review the product inside the
product. The three `index.v*.html` files are ready-to-publish authoring sources;
you drive the flow yourself with the `octo` CLI, exactly as an agent would.

It exercises the full surface:

- **Interactive HTML artifacts** — a live SVG adoption chart with a
  Monthly/Cumulative toggle (vanilla JS, no dependencies).
- **Remote-first drafts → immutable versions** — `octo new` saves a mutable draft;
  `octo publish` freezes it as **v1**; iterate and publish again for **v2**, **v3**.
- **Default-private + share codes** — a fresh doc is author-only; `octo share`
  mints a read+comment `?code=` link anyone can open.
- **Anchored comments that re-anchor across versions**, threaded replies, and
  **agent verdicts** (`applied` / `partial` / `question`), plus emoji reactions.
- **Comment-driven editing** — v3 adds a section that answers a reviewer's
  "does it survive a full rewrite?" question, the loop `octo` models.

## Prerequisites

A running octo-doc server + the `octo` CLI. The simplest path is the local stack:

```bash
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.local.yml up -d --build --wait
make build-octo   # builds ./bin/octo

export OCTO_BASE_URL=http://localhost:18080
export OCTO_TOKEN=local-test-token          # the local stack's write token = author
```

The app serves at **http://localhost:18080** (see `docs/SELF_HOSTING.md` for
production, and `docs/AUTH.md` for the capability/code model).

## Walkthrough

```bash
D=examples/demo
OCTO=./bin/octo

# 1) Create the doc as a private draft, then publish it (v1).
$OCTO new --slug octo-demo --title "octo-doc — documents you can talk to" \
  --html-file $D/index.v1.html
$OCTO publish octo-demo                       # → /d/octo-demo/v/1

# 2) Iterate: chart projection + a Versioning section → v2.
$OCTO version-add --slug octo-demo --html-file $D/index.v2.html
$OCTO publish octo-demo                       # → /d/octo-demo/v/2

# 3) Iterate again: a section answering the anchoring question → v3.
$OCTO version-add --slug octo-demo --html-file $D/index.v3.html
$OCTO publish octo-demo                       # → /d/octo-demo/v/3

# 4) Share it — mint a read+comment link (the doc is private by default).
$OCTO share octo-demo                         # prints .../d/octo-demo/v/3?code=<code>
```

Open the printed `?code=` link in a browser: the code is exchanged for a cookie,
the URL is cleaned, and the doc renders with the review overlay. Select any
sentence to leave an anchored comment; open the version picker to compare v1/v2/v3.

To seed the review threads shown in the screenshots (a human comment + an agent
`applied` reply + a 👍), use the CLI with the share code as the reader credential:

```bash
CODE=$($OCTO share octo-demo | grep -o 'code=[^ ]*' | cut -d= -f2)

# A reader comment (code = read+comment capability), anchored to a phrase.
CID=$(OCTO_CODE=$CODE $OCTO comment --slug octo-demo --version 3 \
  --anchor "re-anchors each comment to the same content" \
  --text "Does the guarantee survive a full rewrite of the paragraph?")

# The author resolves it (write token), and reacts.
$OCTO reply --slug octo-demo --parent "$CID" --status applied --applied-in 3 \
  --text "Yes — v3's new \"What happens when the text is rewritten?\" section covers it."
OCTO_CODE=$CODE $OCTO react --slug octo-demo --comment "$CID" --emoji "👍" --version 3
```

> Publishing is **immutable and append-only** — each `publish` promotes the current
> draft to the next version. To start over, `octo unpublish octo-demo` and re-run.

## Files

| File            | What it is                                             |
|-----------------|--------------------------------------------------------|
| `index.v1.html` | The interactive self-intro document (version 1)        |
| `index.v2.html` | Revised (v2: chart projection + a Versioning section)  |
| `index.v3.html` | Revised (v3: anchoring-states section answering the review question) |

All three HTML files are fully self-contained (inline CSS + JS, no external
assets), so they render offline and stay within the overlay's content-security
policy.
