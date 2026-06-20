// Filesystem BlobStore. Immutable HTML docs at:
//   <DATA_DIR>/blobs/<safeKey(slug)>/v<version>/index.html
//
// PATH-TRAVERSAL DEFENSE (failure mode "blob 路径穿越：slug 须哈希/转义"):
// the slug is validated upstream by safeSlug(), but defense-in-depth here makes
// traversal structurally impossible — the on-disk directory is the slug's hex
// SHA-256 (no `.`, `/`, or `..` can survive), and a `slug.txt` sidecar records
// the human-readable slug for operators. Even a slug that somehow bypassed
// validation cannot escape <DATA_DIR>/blobs.
import { createHash } from 'node:crypto';
import {
  mkdirSync, writeFileSync, readFileSync, statSync, existsSync,
  readdirSync, rmSync,
} from 'node:fs';
import { join } from 'node:path';

function safeKey(slug) {
  return createHash('sha256').update(String(slug)).digest('hex').slice(0, 32);
}

export function makeFsBlobStore(env) {
  const root = join(env.dataDir || env.DATA_DIR || './data', 'blobs');
  mkdirSync(root, { recursive: true });

  const dirFor = (slug) => join(root, safeKey(slug));
  const fileFor = (slug, version) => join(dirFor(slug), `v${Number(version)}`, 'index.html');

  return {
    async putDoc(slug, version, html) {
      const dir = dirFor(slug);
      mkdirSync(join(dir, `v${Number(version)}`), { recursive: true });
      // sidecar so an operator can map hashed dirs back to slugs
      try { writeFileSync(join(dir, 'slug.txt'), String(slug)); } catch {}
      const file = fileFor(slug, version);
      writeFileSync(file, html);
      return { size: Buffer.byteLength(html) };
    },
    async getDoc(slug, version) {
      const file = fileFor(slug, version);
      if (!existsSync(file)) return null;
      return readFileSync(file, 'utf8');
    },
    async headDoc(slug, version) {
      const file = fileFor(slug, version);
      if (!existsSync(file)) return null;
      return { size: statSync(file).size };
    },
    async listVersions(slug) {
      const dir = dirFor(slug);
      if (!existsSync(dir)) return [];
      return readdirSync(dir)
        .map(n => /^v(\d+)$/.exec(n))
        .filter(Boolean)
        .map(m => Number(m[1]))
        .sort((a, b) => a - b);
    },
    async deleteDoc(slug) {
      const dir = dirFor(slug);
      if (existsSync(dir)) rmSync(dir, { recursive: true, force: true });
    },
  };
}
