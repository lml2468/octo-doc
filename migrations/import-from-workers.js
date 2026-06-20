// Importer: Cloudflare KV + R2 export  →  octo-doc storage.
//
// The upstream Worker stores:
//   KV  meta:<slug>        → doc metadata { title, versions:[{n,created}] }
//   KV  comments:<slug>    → event-log comment array
//   R2  docs/<slug>/v<N>/index.html → immutable stamped HTML
//
// This script reads a directory produced by `wrangler kv:key list/get` +
// `wrangler r2 object get` (see MIGRATING_FROM_WORKERS.md for the exact dump
// commands) and writes it into whatever STORAGE octo-doc is configured for —
// so the same importer handles sqlite+fs AND postgres+s3 with no changes.
//
// Layout it expects under --in <dir>:
//   <dir>/kv/meta/<slug>.json
//   <dir>/kv/comments/<slug>.json        (optional; missing => no comments)
//   <dir>/r2/docs/<slug>/v<N>/index.html
//
// Usage:
//   STORAGE=sqlite+fs DATA_DIR=./data node migrations/import-from-workers.js --in ./cf-dump
import { readdirSync, readFileSync, existsSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { loadConfig } from '../src/config.js';
import { makeStores } from '../src/storage/index.js';
import { safeSlug } from '../src/config.js';

const args = process.argv.slice(2);
const inIdx = args.indexOf('--in');
const IN = inIdx >= 0 ? args[inIdx + 1] : null;
if (!IN || !existsSync(IN)) {
  console.error('usage: node migrations/import-from-workers.js --in <cf-dump-dir>');
  process.exit(1);
}

const config = loadConfig(process.env);
const { metaStore, blobStore, spec } = await makeStores(config);
console.log(`importing ${IN} → octo-doc storage (${spec})`);

let docs = 0, versions = 0, commentSets = 0, skipped = 0;

const r2Root = join(IN, 'r2', 'docs');
if (!existsSync(r2Root)) { console.error(`no ${r2Root} — nothing to import`); process.exit(1); }

for (const rawSlug of readdirSync(r2Root)) {
  const slug = safeSlug(rawSlug);
  if (!slug) { console.warn(`skip unsafe slug: ${rawSlug}`); skipped++; continue; }
  const slugDir = join(r2Root, rawSlug);
  if (!statSync(slugDir).isDirectory()) continue;

  // Blobs: docs/<slug>/v<N>/index.html (already aid-stamped on the Worker —
  // we re-store them as-is so rendering stays byte-identical).
  const vDirs = readdirSync(slugDir).filter(n => /^v\d+$/.test(n));
  const importedVersions = [];
  for (const vd of vDirs) {
    const n = Number(vd.slice(1));
    const html = join(slugDir, vd, 'index.html');
    if (!existsSync(html)) continue;
    await blobStore.putDoc(slug, n, readFileSync(html, 'utf8'));
    importedVersions.push(n);
    versions++;
  }
  if (!importedVersions.length) { skipped++; continue; }

  // Metadata: prefer the dumped KV meta; fall back to synthesizing from blobs.
  const metaFile = join(IN, 'kv', 'meta', `${rawSlug}.json`);
  let meta;
  if (existsSync(metaFile)) {
    try { meta = JSON.parse(readFileSync(metaFile, 'utf8')); } catch { meta = null; }
  }
  if (!meta) {
    meta = { slug, title: slug, versions: importedVersions.sort((a, b) => a - b).map(n => ({ n, created: null })) };
  }
  meta.slug = slug;
  if (!Array.isArray(meta.versions) || !meta.versions.length) {
    meta.versions = importedVersions.sort((a, b) => a - b).map(n => ({ n, created: null }));
  }
  await metaStore.putMeta(slug, meta);

  // Comments: the event log is portable verbatim.
  const commentsFile = join(IN, 'kv', 'comments', `${rawSlug}.json`);
  if (existsSync(commentsFile)) {
    try {
      const list = JSON.parse(readFileSync(commentsFile, 'utf8'));
      if (Array.isArray(list)) { await metaStore.putComments(slug, list); commentSets++; }
    } catch { /* leave empty */ }
  }
  docs++;
  console.log(`  ${slug}: ${importedVersions.length} version(s)`);
}

await metaStore.close?.();
console.log(`\nDone — ${docs} doc(s), ${versions} version(s), ${commentSets} comment set(s), ${skipped} skipped.`);
