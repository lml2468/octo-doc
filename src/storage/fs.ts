/**
 * Filesystem {@link BlobStore}. Immutable HTML at
 * `<dataDir>/blobs/<hash(slug)>/v<version>/index.html`.
 *
 * Path-traversal defense: the on-disk directory is the slug's SHA-256 (no `.`,
 * `/`, or `..` can survive), so even a slug that bypassed validation cannot
 * escape the blobs root. A `slug.txt` sidecar records the human-readable slug.
 *
 * Chaos-safety: writes go to a temp file then `rename` (atomic on POSIX), so a
 * crash mid-publish never leaves a half-written `index.html`.
 */
import { randomBytes } from 'node:crypto';
import {
  mkdirSync,
  writeFileSync,
  readFileSync,
  statSync,
  existsSync,
  readdirSync,
  rmSync,
  renameSync,
  unlinkSync,
} from 'node:fs';
import { join } from 'node:path';
import type { Config } from '../config.js';
import type { BlobStore } from './types.js';
import { hashSlug } from './keys.js';

/** Open the filesystem blob store rooted under the configured data directory. */
export function makeFsBlobStore(config: Pick<Config, 'dataDir'>): BlobStore {
  const root = join(config.dataDir, 'blobs');
  mkdirSync(root, { recursive: true });

  const dirFor = (slug: string): string => join(root, hashSlug(slug));
  const fileFor = (slug: string, version: number): string =>
    join(dirFor(slug), `v${version}`, 'index.html');

  return {
    putDoc: (slug, version, html) => {
      const dir = join(dirFor(slug), `v${version}`);
      mkdirSync(dir, { recursive: true });
      try {
        writeFileSync(join(dirFor(slug), 'slug.txt'), slug);
      } catch {
        // sidecar is best-effort; not required for correctness
      }
      const final = join(dir, 'index.html');
      const tmp = join(dir, `.index.html.${randomBytes(6).toString('hex')}.tmp`);
      try {
        writeFileSync(tmp, html);
        renameSync(tmp, final); // atomic publish
      } catch (err) {
        try {
          if (existsSync(tmp)) unlinkSync(tmp);
        } catch {
          // ignore cleanup failure
        }
        throw err;
      }
      return Promise.resolve({ size: Buffer.byteLength(html) });
    },

    getDoc: (slug, version) => {
      const file = fileFor(slug, version);
      return Promise.resolve(existsSync(file) ? readFileSync(file, 'utf8') : null);
    },

    headDoc: (slug, version) => {
      const file = fileFor(slug, version);
      return Promise.resolve(existsSync(file) ? { size: statSync(file).size } : null);
    },

    listVersions: (slug) => {
      const dir = dirFor(slug);
      if (!existsSync(dir)) return Promise.resolve([]);
      const versions = readdirSync(dir)
        .map((n) => /^v(\d+)$/.exec(n))
        .filter((m): m is RegExpExecArray => m !== null)
        // a v<N> dir only counts once its index.html exists (ignores in-flight tmp dirs)
        .filter((m) => existsSync(join(dir, m[0], 'index.html')))
        .map((m) => Number(m[1]))
        .sort((a, b) => a - b);
      return Promise.resolve(versions);
    },

    deleteDoc: (slug) => {
      const dir = dirFor(slug);
      if (existsSync(dir)) rmSync(dir, { recursive: true, force: true });
      return Promise.resolve();
    },
  };
}
