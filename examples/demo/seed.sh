#!/usr/bin/env bash
# seed.sh — publish the octo-doc self-intro demo and seed a full collaboration
# scenario, driven entirely through the `octo` CLI (no curl, no jq).
#
# It demonstrates the whole product surface the way a user actually drives it:
#   • octo new / version-add / publish   — author v1, v2, v3 and publish them
#   • octo comment                       — anchored human comments + a threaded reply
#   • octo reply --remote                — agent replies carrying "applied" verdicts
#   • octo react                         — an emoji reaction
#
# The doc evolves from its own review: v3 adds a section answering the open
# anchoring question, and the agent marks that thread applied — the /octo edit
# loop, end to end.
#
# Usage:
#   ./examples/demo/seed.sh            # build octo if needed, publish + seed
#   ./examples/demo/seed.sh --reset    # wipe the slug's comments, then re-seed
#
# Config (env):
#   BASE   octo-doc base URL   (default http://localhost:18080)
#   TOKEN  write bearer token  (default local-test-token)
#   SLUG   document slug       (default octo-demo)
#   OCTO   path to the octo binary (default: build ./bin/octo from this repo)
set -euo pipefail

BASE="${BASE:-http://localhost:18080}"
TOKEN="${TOKEN:-local-test-token}"
SLUG="${SLUG:-octo-demo}"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$DIR/../.." && pwd)"

bold=$'\033[1m'; grn=$'\033[32m'; red=$'\033[31m'; dim=$'\033[2m'; rst=$'\033[0m'
pass() { printf '  %s✓%s %s\n' "$grn" "$rst" "$1"; }
fail() { printf '  %s✗ %s%s\n' "$red" "$1" "$rst"; exit 1; }
step() { printf '%s==> %s%s\n' "$bold" "$1" "$rst"; }

# Resolve the octo CLI: honor $OCTO, else a prebuilt ./bin/octo, else build it.
OCTO="${OCTO:-$ROOT/bin/octo}"
if [ ! -x "$OCTO" ]; then
  step "building the octo CLI"
  ( cd "$ROOT" && go build -o bin/octo ./cmd/octo ) || fail "go build ./cmd/octo failed"
  pass "built $OCTO"
fi

# The CLI keeps docs in its own store; use a throwaway one so the demo never
# touches ~/octo-docs. Point every octo invocation at the same server + store.
STORE="$(mktemp -d "${TMPDIR:-/tmp}/octo-demo-seed.XXXXXX")"
trap 'rm -rf "$STORE"' EXIT
export OCTO_BASE_URL="$BASE" OCTO_TOKEN="$TOKEN" OCTO_DIR="$STORE"

octo() { "$OCTO" "$@"; }

main() {
  step "preflight — $BASE"
  octo doctor >/dev/null 2>&1 || true   # doctor never fails; we check the server explicitly next

  step "author the document locally (v1 → v2 → v3)"
  # --no-serve: this is a headless seed, so don't spin up a local preview.
  octo new --slug "$SLUG" --title "octo-doc — documents you can talk to" \
    --html-file "$DIR/index.v1.html" --no-serve --quiet --force \
    || fail "octo new failed"
  octo version-add --slug "$SLUG" --html-file "$DIR/index.v2.html" \
    --prompt "chart projection + a Versioning section" >/dev/null || fail "version-add v2 failed"
  octo version-add --slug "$SLUG" --html-file "$DIR/index.v3.html" \
    --prompt "answer the anchoring review question" >/dev/null || fail "version-add v3 failed"
  pass "scaffolded v1/v2/v3 in a throwaway store"

  step "publish all versions"
  octo publish "$SLUG" >/dev/null 2>&1 || fail "octo publish failed (is the stack up at $BASE?)"
  pass "published  ·  $BASE/d/$SLUG/v/1 … v/3"

  step "seed the review threads (anchored to v1)"
  c_anchor="$(octo comment --slug "$SLUG" --version 1 \
    --anchor "re-anchors each comment to the same content" \
    --text "Love this — the re-anchoring guarantee is the killer feature. Does it survive a full rewrite of the paragraph?")" \
    || fail "anchoring comment failed"
  pass "human comment on the anchoring guarantee"

  octo comment --slug "$SLUG" --version 1 --parent "$c_anchor" \
    --text "Good question — if the text is gone entirely it's flagged as unanchored so you can move it deliberately." \
    >/dev/null || fail "threaded reply failed"
  pass "threaded reply"

  c_chart="$(octo comment --slug "$SLUG" --version 1 \
    --anchor "single unit you can comment on" \
    --text "Can we add a projected series to this chart so stakeholders see the runway?")" \
    || fail "chart comment failed"
  pass "human comment on the chart artifact"

  step "resolve both threads with agent verdicts"
  octo reply --slug "$SLUG" --parent "$c_chart" --status applied --applied-in 2 --remote \
    --text "Done — added a projected two-month series (lighter bars) in v2, with a legend. See the updated artifact." \
    >/dev/null || fail "agent reply (chart) failed"
  pass "agent reply on the chart thread — ${bold}applied${rst}${grn} in v2"

  octo reply --slug "$SLUG" --parent "$c_anchor" --status applied --applied-in 3 --remote \
    --text "Yes — a full rewrite is covered. v3 adds a \"What happens when the text is rewritten?\" section spelling out the three states: Anchored (re-attaches automatically), Drifted (re-attaches to the closest match and flags it), and Unanchored (when the text is gone entirely, the comment is flagged for deliberate re-anchoring — never silently dropped or moved)." \
    >/dev/null || fail "agent reply (anchoring) failed"
  pass "agent reply on the anchoring thread — ${bold}applied${rst}${grn} in v3"

  octo react --slug "$SLUG" --comment "$c_chart" --emoji "👍" >/dev/null || fail "reaction failed"
  pass "emoji reaction on the chart thread"

  printf '\n%sDemo ready.%s\n' "$bold" "$rst"
  printf '  v1 (original) : %s/d/%s/v/1\n' "$BASE" "$SLUG"
  printf '  v2 (chart)    : %s/d/%s/v/2\n' "$BASE" "$SLUG"
  printf '  v3 (latest)   : %s/d/%s/v/3\n' "$BASE" "$SLUG"
  printf '\n%sTry it:%s select a sentence to comment · open the version picker to\n' "$dim" "$rst"
  printf '  compare v1/v2/v3 · see both threads resolved with an agent %sapplied%s verdict.\n' "$dim" "$rst"
}

if [ "${1:-}" = "--reset" ]; then
  step "reset — removing '$SLUG' from the server first"
  # Build a client env and unpublish; ignore "not found" on a clean server.
  OCTO_BASE_URL="$BASE" OCTO_TOKEN="$TOKEN" OCTO_DIR="$STORE" "$OCTO" unpublish "$SLUG" >/dev/null 2>&1 || true
  pass "server state cleared (fresh v1/v2/v3 on re-seed)"
fi

main
