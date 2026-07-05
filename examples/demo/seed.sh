#!/usr/bin/env bash
# seed.sh — publish the octo-doc self-intro demo and seed a full collaboration
# scenario against a running octo-doc server.
#
# It demonstrates the whole product surface:
#   • publish v1 (an interactive HTML document)
#   • anchored human comments + a threaded reply
#   • an agent reply carrying an "applied" verdict
#   • an emoji reaction
#   • publish v2 (immutable versioning; comments re-anchor to the new version)
#
# Usage:
#   ./examples/demo/seed.sh            # publish + seed the scenario
#   ./examples/demo/seed.sh --reset    # wipe the slug's comments, then re-seed
#
# Config (env):
#   BASE   octo-doc base URL   (default http://localhost:18080)
#   TOKEN  write bearer token  (default local-test-token)
#   SLUG   document slug       (default octo-demo)
set -euo pipefail

BASE="${BASE:-http://localhost:18080}"
TOKEN="${TOKEN:-local-test-token}"
SLUG="${SLUG:-octo-demo}"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

bold=$'\033[1m'; grn=$'\033[32m'; red=$'\033[31m'; dim=$'\033[2m'; rst=$'\033[0m'
pass() { printf '  %s✓%s %s\n' "$grn" "$rst" "$1"; }
fail() { printf '  %s✗ %s%s\n' "$red" "$1" "$rst"; exit 1; }
step() { printf '%s==> %s%s\n' "$bold" "$1" "$rst"; }
# Substring test without a pipe (safe under pipefail on large bodies).
has()  { case "$1" in *"$2"*) return 0 ;; *) return 1 ;; esac; }

auth=(-H "Authorization: Bearer $TOKEN")
json=(-H "Content-Type: application/json")

# publish <html-file> — prints the new version number.
publish() {
  local file="$1" title="$2"
  local payload resp ver
  # Build the request body with jq so the HTML is safely JSON-encoded.
  payload="$(jq -n --arg slug "$SLUG" --arg title "$title" \
    --rawfile html "$file" '{slug:$slug, title:$title, html:$html}')"
  resp="$(curl -fsS -X POST "$BASE/v1/docs" "${auth[@]}" "${json[@]}" -d "$payload")" \
    || fail "publish request failed for $file"
  ver="$(printf '%s' "$resp" | jq -r '.data.version // empty')"
  [ -n "$ver" ] || fail "publish returned no version: $resp"
  printf '%s' "$ver"
}

# comment <version> <text> <anchor-text> — prints the new comment id.
comment() {
  local ver="$1" text="$2" anchor="$3" resp id
  local payload
  payload="$(jq -n --arg slug "$SLUG" --arg text "$text" --argjson ver "$ver" \
    --arg atext "$anchor" \
    '{slug:$slug, version:$ver, text:$text, anchor:{kind:"text", text:$atext}}')"
  resp="$(curl -fsS -X POST "$BASE/v1/comments" "${json[@]}" -d "$payload")" \
    || fail "comment request failed"
  id="$(printf '%s' "$resp" | jq -r '.data.id // empty')"
  [ -n "$id" ] || fail "comment returned no id: $resp"
  printf '%s' "$id"
}

# reply <version> <parent-id> <text> — prints the new reply id.
reply() {
  local ver="$1" parent="$2" text="$3" resp id payload
  payload="$(jq -n --arg slug "$SLUG" --arg text "$text" --argjson ver "$ver" \
    --arg pid "$parent" \
    '{slug:$slug, version:$ver, text:$text, parent_id:$pid}')"
  resp="$(curl -fsS -X POST "$BASE/v1/comments" "${json[@]}" -d "$payload")" \
    || fail "reply request failed"
  id="$(printf '%s' "$resp" | jq -r '.data.id // empty')"
  [ -n "$id" ] || fail "reply returned no id: $resp"
  printf '%s' "$id"
}

# agent_reply <version> <parent-id> <text> <status> — agent verdict on a comment.
agent_reply() {
  local ver="$1" parent="$2" text="$3" status="$4" payload
  payload="$(jq -n --arg slug "$SLUG" --arg text "$text" --argjson ver "$ver" \
    --arg pid "$parent" --arg st "$status" \
    '{slug:$slug, parent_id:$pid, text:$text, status:$st, applied_in:$ver}')"
  curl -fsS -X POST "$BASE/v1/agent/replies" "${auth[@]}" "${json[@]}" -d "$payload" >/dev/null \
    || fail "agent reply request failed"
}

# react <version> <comment-id> <emoji>
react() {
  local ver="$1" cid="$2" emoji="$3" payload
  payload="$(jq -n --arg slug "$SLUG" --argjson ver "$ver" --arg cid "$cid" --arg e "$emoji" \
    '{slug:$slug, version:$ver, comment_id:$cid, emoji:$e}')"
  curl -fsS -X POST "$BASE/v1/reactions" "${json[@]}" -d "$payload" >/dev/null \
    || fail "reaction request failed"
}

main() {
  step "preflight — $BASE"
  curl -fsS "$BASE/healthz" >/dev/null && pass "server healthy" || fail "server not reachable (is the stack up?)"

  if [ "${1:-}" = "--reset" ]; then
    step "reset — wiping existing comments for '$SLUG'"
    curl -fsS -X DELETE "$BASE/v1/comments?slug=$SLUG&all=1" "${auth[@]}" >/dev/null \
      && pass "comments wiped" || fail "wipe failed"
  fi

  step "publish v1 — interactive self-intro document"
  v1="$(publish "$DIR/index.v1.html" "octo-doc — documents you can talk to")"
  pass "published v$v1  ·  $BASE/d/$SLUG/v/$v1"

  step "seed the review thread on v$v1"
  c_anchor="$(comment "$v1" \
    "Love this — the re-anchoring guarantee is the killer feature. Does it survive a full rewrite of the paragraph?" \
    "re-anchors each comment to the same content")"
  pass "human comment on the anchoring guarantee"

  reply "$v1" "$c_anchor" \
    "Good question — if the text is gone entirely it's flagged as unanchored so you can move it deliberately." >/dev/null
  pass "threaded reply"

  c_chart="$(comment "$v1" \
    "Can we add a projected series to this chart so stakeholders see the runway?" \
    "single unit you can comment on")"
  pass "human comment on the chart artifact"

  agent_reply "$v1" "$c_chart" \
    "Done — added a projected two-month series (lighter bars) in v2, with a legend. See the updated artifact." \
    "applied"
  pass "agent reply with an ${bold}applied${rst}${grn} verdict"

  # React on the parent comment the agent addressed.
  react "$v1" "$c_chart" "👍"
  pass "emoji reaction"

  step "publish v2 — revised (chart projection + a Versioning section)"
  v2="$(publish "$DIR/index.v2.html" "octo-doc — documents you can talk to")"
  pass "published v$v2  ·  $BASE/d/$SLUG/v/$v2"

  printf '\n%sDemo ready.%s\n' "$bold" "$rst"
  printf '  v1 (original) : %s/d/%s/v/%s\n' "$BASE" "$SLUG" "$v1"
  printf '  v2 (latest)   : %s/d/%s/v/%s\n' "$BASE" "$SLUG" "$v2"
  printf '\n%sTry it:%s select a sentence to comment · open the version picker to\n' "$dim" "$rst"
  printf '  compare v1/v2 · see the agent %sapplied%s verdict + 👍 on the chart thread.\n' "$dim" "$rst"
}

main "$@"
