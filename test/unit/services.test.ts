import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { DocService } from '../../src/services/doc-service.js';
import { CommentService } from '../../src/services/comment-service.js';
import { makeSqliteMetadataStore } from '../../src/storage/sqlite.js';
import { makeFsBlobStore } from '../../src/storage/fs.js';
import type { MetadataStore, BlobStore } from '../../src/storage/types.js';

let meta: MetadataStore;
let blobs: BlobStore;
let comments: CommentService;
let docs: DocService;
let dir: string;

beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), 'octo-svc-'));
  meta = makeSqliteMetadataStore({ sqlitePath: ':memory:', dataDir: dir });
  blobs = makeFsBlobStore({ dataDir: dir });
  comments = new CommentService(meta);
  docs = new DocService(blobs, meta, comments, { baseUrl: '', maxHtmlBytes: 5_000_000 });
});

afterEach(async () => {
  await meta.close();
  rmSync(dir, { recursive: true, force: true });
});

describe('DocService.publish', () => {
  it('auto-increments versions and stamps artifacts', async () => {
    const r1 = await docs.publish({
      slug: 'hello',
      html: '<figure><svg viewBox="0 0 1 1"></svg></figure>',
    });
    expect(r1.version).toBe(1);
    expect(r1.url).toBe('/d/hello/v/1');
    expect(r1.aids).toBe(2);

    const r2 = await docs.publish({ slug: 'hello', html: '<h1>v2</h1>' });
    expect(r2.version).toBe(2);

    const render = await docs.render('hello', 1);
    expect(render?.html).toContain('data-tdoc-aid');
    expect((await docs.listVersions('hello'))?.versions.map((v) => v.n)).toStrictEqual([1, 2]);
  });

  it('rejects empty html and oversized html', async () => {
    await expect(docs.publish({ slug: 'x', html: '' })).rejects.toMatchObject({
      code: 'html_required',
    });
    const tiny = new DocService(blobs, meta, comments, { baseUrl: '', maxHtmlBytes: 10 });
    await expect(tiny.publish({ slug: 'x', html: '<h1>way too long</h1>' })).rejects.toMatchObject({
      status: 413,
      code: 'html_too_large',
    });
  });

  it('remove() deletes blobs, meta, and comments', async () => {
    await docs.publish({ slug: 'gone', html: '<h1>x</h1>' });
    await docs.remove('gone');
    expect(await docs.render('gone', 1)).toBeNull();
    expect(await docs.listVersions('gone')).toBeNull();
  });
});

describe('CommentService serialization', () => {
  it('does not lose updates under concurrent same-slug writes', async () => {
    // 50 concurrent creates must all land (the mutex prevents read-modify-write loss).
    await Promise.all(
      Array.from({ length: 50 }, (_, i) =>
        comments.create('race', { author: { login: `u${i}` }, text: `c${i}`, version: 1 }),
      ),
    );
    const list = await comments.list('race', 1);
    expect(list).toHaveLength(50);
  });

  it('folds create → reply → list at version', async () => {
    const created = await comments.create('s', { author: { login: 'a' }, text: 'hi', version: 1 });
    const id = (created.body as { id: string }).id;
    await comments.reply('s', { parentId: id, author: { login: 'b' }, text: 'yo', version: 1 });
    const list = await comments.list('s', 1);
    expect(list[0]?.replies[0]?.text).toBe('yo');
  });
});

describe('DocService.listAllForOwner', () => {
  it('returns docs with a reachable latest version, skipping ghosts', async () => {
    await docs.publish({ slug: 'real', html: '<h1>r</h1>' });
    // A meta entry whose blob never landed must not appear in the catalog.
    await meta.putMeta('ghost', { slug: 'ghost', title: 'Ghost', versions: [{ n: 1 }] });
    const all = await docs.listAllForOwner();
    expect(all.map((d) => d.slug)).toContain('real');
    expect(all.map((d) => d.slug)).not.toContain('ghost');
  });
});
