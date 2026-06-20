/**
 * Contract tests: one suite run against every {@link MetadataStore} and
 * {@link BlobStore} implementation. Proves the adapters are behaviorally
 * interchangeable (the "adapter swap = zero code change" guarantee).
 *
 * SQLite + FS always run. Postgres/S3 run only when their env is present, so the
 * suite is green in a bare checkout and exhaustive in CI (service containers).
 */
import { describe, it, expect, afterAll } from 'vitest';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { makeSqliteMetadataStore } from '../../src/storage/sqlite.js';
import { makeFsBlobStore } from '../../src/storage/fs.js';
import type { BlobStore, MetadataStore } from '../../src/storage/types.js';
import type { Comment } from '../../src/core/comment.types.js';

const tmpDirs: string[] = [];
afterAll(() => tmpDirs.forEach((d) => rmSync(d, { recursive: true, force: true })));

function freshDir(prefix: string): string {
  const d = mkdtempSync(join(tmpdir(), prefix));
  tmpDirs.push(d);
  return d;
}

interface MetaCase {
  name: string;
  make: () => Promise<MetadataStore>;
}
interface BlobCase {
  name: string;
  make: () => Promise<BlobStore>;
}

const metaCases: MetaCase[] = [
  {
    name: 'sqlite',
    make: () =>
      Promise.resolve(
        makeSqliteMetadataStore({ sqlitePath: ':memory:', dataDir: freshDir('octo-sq-') }),
      ),
  },
];
const blobCases: BlobCase[] = [
  { name: 'fs', make: () => Promise.resolve(makeFsBlobStore({ dataDir: freshDir('octo-fs-') })) },
];

if (process.env.DATABASE_URL) {
  metaCases.push({
    name: 'postgres',
    make: async () => {
      const { makePostgresMetadataStore } = await import('../../src/storage/postgres.js');
      const { loadConfig } = await import('../../src/config.js');
      return makePostgresMetadataStore(loadConfig());
    },
  });
}
if (process.env.S3_ENDPOINT || process.env.S3_BUCKET) {
  blobCases.push({
    name: 's3',
    make: async () => {
      const { makeS3BlobStore } = await import('../../src/storage/s3.js');
      const { loadConfig } = await import('../../src/config.js');
      return makeS3BlobStore(loadConfig());
    },
  });
}

const sampleComment = (id: string): Comment => ({
  id,
  author: { login: 'a' },
  created: '2026-01-01T00:00:00Z',
  created_in: 1,
  events: [
    { kind: 'created', at_version: 1, at: '2026-01-01T00:00:00Z', anchor: null, text: 'hi' },
  ],
});

describe.each(metaCases)('MetadataStore contract: $name', ({ make }) => {
  it('round-trips meta, comments, sessions, tokens', async () => {
    const store = await make();
    try {
      expect(await store.getMeta('s')).toBeNull();
      await store.putMeta('s', { slug: 's', title: 'T', versions: [{ n: 1 }] });
      expect((await store.getMeta('s'))?.title).toBe('T');
      expect(await store.listMeta()).toHaveLength(1);

      expect(await store.getComments('s')).toStrictEqual([]);
      await store.putComments('s', [sampleComment('c1')]);
      expect((await store.getComments('s'))[0]?.id).toBe('c1');

      expect(await store.anyToken()).toBe(false);
      await store.putToken('tok', { token: 'tok', created: 'now' });
      expect(await store.anyToken()).toBe(true);
      expect((await store.getToken('tok'))?.token).toBe('tok');

      await store.putSession('sid', { login: 'me', created: 'now' }, 60);
      expect((await store.getSession('sid'))?.login).toBe('me');
      await store.deleteSession('sid');
      expect(await store.getSession('sid')).toBeNull();

      await store.deleteMeta('s');
      await store.deleteComments('s');
      expect(await store.getMeta('s')).toBeNull();
    } finally {
      await store.close();
    }
  });

  it('expires sessions past their TTL', async () => {
    const store = await make();
    try {
      await store.putSession('sid', { login: 'me', created: 'now' }, -1);
      expect(await store.getSession('sid')).toBeNull();
    } finally {
      await store.close();
    }
  });
});

describe.each(blobCases)('BlobStore contract: $name', ({ make }) => {
  it('writes immutable versions and lists them ascending', async () => {
    const store = await make();
    const slug = `ct-${Date.now()}-${Math.floor(performance.now())}`;
    await store.putDoc(slug, 1, '<h1>v1</h1>');
    await store.putDoc(slug, 2, '<h1>v2</h1>');
    expect(await store.getDoc(slug, 1)).toBe('<h1>v1</h1>');
    expect(await store.getDoc(slug, 2)).toBe('<h1>v2</h1>');
    expect(await store.listVersions(slug)).toStrictEqual([1, 2]);
    expect(await store.getDoc(slug, 9)).toBeNull();
    expect(await store.headDoc(slug, 1)).not.toBeNull();
    await store.deleteDoc(slug);
    expect(await store.getDoc(slug, 1)).toBeNull();
  });
});
