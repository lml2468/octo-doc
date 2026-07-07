# Migrating from the Cloudflare Workers deployment

If you ran upstream [tdoc](https://github.com/serenakeyitan/tdoc) on Cloudflare
(Worker + KV + R2 + Durable Object), this moves your docs and comments to a
self-hosted octo-doc with **zero data loss**. Comment anchors survive because the
aid **hash** is a verbatim port; re-publishing re-stamps the artifacts with
octo-doc's `data-odoc-aid` attribute (the Worker emitted `data-tdoc-aid` — same
hash value, octo-doc-native name).

## What maps where

| Cloudflare | What's in it | → octo-doc |
| ---------- | ------------ | ---------- |
| R2 `docs/<slug>/v<N>/index.html` | immutable stamped HTML | `BlobStore` |
| KV `meta:<slug>` | `{ title, versions }` | `MetadataStore.meta` |
| KV `comments:<slug>` | event-log comment array | `MetadataStore.comments` |
| Durable Object `CommentsStore` | per-slug write serialization (no durable data beyond the KV mirror) | in-process mutex — **nothing to migrate** |
| KV `session:<sid>` | login sessions | not migrated (users re-auth; sessions are ephemeral) |

> The Durable Object held the *authoritative* comment list in its own storage,
> lazily migrated from KV. If your DO storage is ahead of the KV `comments:*`
> values, export comments from the DO instead — call
> `GET https://<worker>/api/comments?slug=<slug>&version=all` per slug (that
> reads the DO) and save each response as `kv/comments/<slug>.json`. This is the
> most reliable source of truth.

## Step 1 — dump from Cloudflare

You need the KV namespace id (`META`) and R2 bucket name (`tdoc-docs`) from your
`wrangler.toml`. Then:

```bash
mkdir -p cf-dump/kv/meta cf-dump/kv/comments cf-dump/r2/docs
KV_ID=<your META namespace id>
WORKER=https://<your-worker>.<subdomain>.workers.dev

# --- metadata + comments via KV ---
wrangler kv:key list --namespace-id "$KV_ID" --remote \
  | jq -r '.[].name' > keys.txt

while read -r key; do
  case "$key" in
    meta:*)
      slug="${key#meta:}"
      wrangler kv:key get "$key" --namespace-id "$KV_ID" --remote \
        > "cf-dump/kv/meta/${slug}.json" ;;
  esac
done < keys.txt

# --- comments: read the DO (authoritative) via the public API, full history ---
for slug in $(ls cf-dump/kv/meta | sed 's/\.json$//'); do
  curl -sf "$WORKER/api/comments?slug=${slug}&version=all" \
    > "cf-dump/kv/comments/${slug}.json" || true
done

# --- HTML blobs via R2 ---
# List objects, then fetch each. (Or fetch per known version from meta.json.)
for slug in $(ls cf-dump/kv/meta | sed 's/\.json$//'); do
  for n in $(jq -r '.versions[].n' "cf-dump/kv/meta/${slug}.json"); do
    mkdir -p "cf-dump/r2/docs/${slug}/v${n}"
    wrangler r2 object get "tdoc-docs/docs/${slug}/v${n}/index.html" \
      --file "cf-dump/r2/docs/${slug}/v${n}/index.html" --remote || true
  done
done
```

You now have:

```
cf-dump/
  kv/meta/<slug>.json
  kv/comments/<slug>.json
  r2/docs/<slug>/v<N>/index.html
```

## Step 2 — import into octo-doc

octo-doc has no bespoke importer binary; you replay the dump through the public
write API (`POST /v1/docs`), which performs the same stamping, versioning, and
comment merge as a normal publish. Point a running server at your PostgreSQL + S3
backend, mint a write token, then push each version:

```bash
BASE=http://localhost:8080
TOKEN=$(curl -sX POST "$BASE/v1/admin/bootstrap" | jq -r .data.token)

for slug in $(ls cf-dump/kv/meta | sed 's/\.json$//'); do
  comments=$(jq -c '.comments // []' "cf-dump/kv/comments/${slug}.json" 2>/dev/null || echo '[]')
  for n in $(jq -r '.versions[].n' "cf-dump/kv/meta/${slug}.json"); do
    html=$(cat "cf-dump/r2/docs/${slug}/v${n}/index.html")
    jq -n --arg slug "$slug" --argjson v "$n" --arg html "$html" --argjson c "$comments" \
      '{slug:$slug, version:$v, html:$html, comments:$c}' \
    | curl -s -X POST "$BASE/v1/docs" \
        -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d @-
    echo "  $slug v$n"
  done
done
```

The Worker stamped the HTML with `data-tdoc-aid`; octo-doc's `StampAids` re-stamps
it as `data-odoc-aid` on re-publish, computing the **same** aid hash (a verbatim
port — see [PORTING.md](./PORTING.md)), so comment anchors still resolve. Passing
an explicit `version` preserves the original
version numbers; the comment array is merged non-destructively on the first push.

## Step 3 — verify

```bash
curl -sf localhost:8080/v1/docs/plaud-explainer/versions | jq
curl -sf localhost:8080/d/plaud-explainer/v/1 | grep -c data-odoc-aid
curl -sf "localhost:8080/v1/comments?slug=plaud-explainer&version=all" | jq '.data | length'
```

Comment anchors are preserved because the aid hash is a verbatim port; the
re-published HTML carries octo-doc's `data-odoc-aid` (re-publishing through
`/v1/docs` would also produce the same bytes — see
[ARCHITECTURE.md](./ARCHITECTURE.md#rendering-parity-byte-equivalent-output)).

## Step 4 — point clients at the new server

```bash
export OCTO_BASE_URL="https://docs.example.com"
export OCTO_TOKEN="<your write token>"
# Old ~/.tdoc/published.json (Cloudflare) is no longer used; the new CLI reads
# ~/.octo/config.json. You can delete published.json once you've verified.
```

Future `octo publish`, `octo pull`, `octo edit` flows all target the new
server with no further changes — the command surface is unchanged.

## What you can decommission

Once verified, you can tear down the Cloudflare side: delete the Worker, the
`tdoc-docs` R2 bucket, the `META` KV namespace, and the Durable Object. None of
it is referenced by octo-doc.
