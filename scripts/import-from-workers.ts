/**
 * Importer: Cloudflare KV + R2 export → octo-doc storage.
 *
 * Reads a dump directory (see MIGRATING_FROM_WORKERS.md for the wrangler
 * commands that produce it) and writes it into whatever STORAGE octo-doc is
 * configured for — so the same importer handles sqlite+fs and postgres+s3.
 *
 * Layout under --in <dir>:
 *   <dir>/kv/meta/<slug>.json
 *   <dir>/kv/comments/<slug>.json     (optional)
 *   <dir>/r2/docs/<slug>/v<N>/index.html
 *
 * Usage: tsx scripts/import-from-workers.ts --in ./cf-dump
 */
import { readdirSync, readFileSync, existsSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { loadConfig, safeSlug } from '../src/config.js';
import { makeStores } from '../src/storage/index.js';
import type { Comment } from '../src/core/comment.types.js';
import type { DocMeta } from '../src/storage/types.js';
import { logger } from '../src/logger.js';

const log = logger();

function parseArgs(): { in: string } {
  const i = process.argv.indexOf('--in');
  const dir = i >= 0 ? process.argv[i + 1] : undefined;
  if (!dir || !existsSync(dir)) {
    log.error('usage: tsx scripts/import-from-workers.ts --in <cf-dump-dir>');
    process.exit(1);
  }
  return { in: dir };
}

function readJson<T>(path: string, fallback: T): T {
  try {
    return JSON.parse(readFileSync(path, 'utf8')) as T;
  } catch {
    return fallback;
  }
}

async function run(): Promise<void> {
  const args = parseArgs();
  const config = loadConfig(process.env);
  const { metaStore, blobStore, spec } = await makeStores(config);
  log.info({ in: args.in, storage: spec }, 'importing Cloudflare dump');

  const r2Root = join(args.in, 'r2', 'docs');
  if (!existsSync(r2Root)) {
    log.error({ r2Root }, 'no r2/docs in dump — nothing to import');
    process.exit(1);
  }

  let docs = 0;
  let versions = 0;
  for (const rawSlug of readdirSync(r2Root)) {
    const slug = safeSlug(rawSlug);
    if (!slug || !statSync(join(r2Root, rawSlug)).isDirectory()) continue;
    const imported = await importSlug(args.in, rawSlug, slug, metaStore, blobStore);
    if (imported > 0) {
      docs++;
      versions += imported;
      log.info({ slug, versions: imported }, 'imported doc');
    }
  }

  await metaStore.close();
  log.info({ docs, versions }, 'import complete');
}

async function importSlug(
  inDir: string,
  rawSlug: string,
  slug: string,
  metaStore: Awaited<ReturnType<typeof makeStores>>['metaStore'],
  blobStore: Awaited<ReturnType<typeof makeStores>>['blobStore'],
): Promise<number> {
  const slugDir = join(inDir, 'r2', 'docs', rawSlug);
  const vDirs = readdirSync(slugDir).filter((n) => /^v\d+$/.test(n));
  const numbers: number[] = [];
  for (const vd of vDirs) {
    const html = join(slugDir, vd, 'index.html');
    if (!existsSync(html)) continue;
    const n = Number(vd.slice(1));
    await blobStore.putDoc(slug, n, readFileSync(html, 'utf8'));
    numbers.push(n);
  }
  if (numbers.length === 0) return 0;

  const metaFile = join(inDir, 'kv', 'meta', `${rawSlug}.json`);
  const meta = readJson<DocMeta | null>(metaFile, null) ?? {
    slug,
    title: slug,
    versions: numbers.sort((a, b) => a - b).map((n) => ({ n, created: null })),
  };
  meta.slug = slug;
  await metaStore.putMeta(slug, meta);

  const commentsFile = join(inDir, 'kv', 'comments', `${rawSlug}.json`);
  const comments = readJson<Comment[]>(commentsFile, []);
  if (Array.isArray(comments) && comments.length) await metaStore.putComments(slug, comments);

  return numbers.length;
}

void run();
