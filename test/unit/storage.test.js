// Storage + security unit tests: slug validation, path-traversal defense,
// adapter round-trips against the SQLite+FS reference implementation.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, existsSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { safeSlug } from '../../src/config.js';
import { makeSqliteMetadataStore } from '../../src/storage/sqlite.js';
import { makeFsBlobStore } from '../../src/storage/fs.js';

test('safeSlug: accepts safe slugs, rejects traversal', () => {
  assert.equal(safeSlug('hello-world_1'), 'hello-world_1');
  assert.equal(safeSlug('../../etc/passwd'), null);
  assert.equal(safeSlug('a/b'), null);
  assert.equal(safeSlug('a.b'), null);
  assert.equal(safeSlug(''), null);
  assert.equal(safeSlug('x'.repeat(65)), null);
});

test('FS blob store: traversal-proof keys, version round-trip', async () => {
  const dir = mkdtempSync(join(tmpdir(), 'octo-fs-'));
  try {
    const store = makeFsBlobStore({ DATA_DIR: dir });
    await store.putDoc('hello', 1, '<h1>v1</h1>');
    await store.putDoc('hello', 2, '<h1>v2</h1>');
    assert.equal(await store.getDoc('hello', 1), '<h1>v1</h1>');
    assert.equal(await store.getDoc('hello', 2), '<h1>v2</h1>');
    assert.deepEqual(await store.listVersions('hello'), [1, 2]);
    assert.equal(await store.getDoc('hello', 9), null);

    // On-disk directory is the slug HASH, not the slug — no path-shaped names.
    const blobs = readdirSync(join(dir, 'blobs'));
    assert.ok(blobs.every(n => /^[a-f0-9]{32}$/.test(n)), 'blob dirs are hex hashes');

    await store.deleteDoc('hello');
    assert.equal(await store.getDoc('hello', 1), null);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test('SQLite meta store: meta/comments/session/token round-trips', async () => {
  const dir = mkdtempSync(join(tmpdir(), 'octo-sq-'));
  try {
    const store = makeSqliteMetadataStore({ SQLITE_PATH: join(dir, 'db.sqlite') });
    await store.putMeta('s', { title: 'T', versions: [{ n: 1 }] });
    assert.equal((await store.getMeta('s')).title, 'T');
    assert.deepEqual(await store.getComments('s'), []);
    await store.putComments('s', [{ id: 'c1' }]);
    assert.equal((await store.getComments('s'))[0].id, 'c1');

    assert.equal(await store.anyToken(), false);
    await store.putToken('tok', { token: 'tok', created: 'now' });
    assert.equal(await store.anyToken(), true);

    await store.putSession('sid', { login: 'me' }, 60);
    assert.equal((await store.getSession('sid')).login, 'me');
    await store.deleteSession('sid');
    assert.equal(await store.getSession('sid'), null);

    const all = await store.listMeta();
    assert.equal(all.length, 1);
    await store.close();
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test('SQLite session TTL: expired sessions return null', async () => {
  const store = makeSqliteMetadataStore({ SQLITE_PATH: ':memory:' });
  await store.putSession('sid', { login: 'me' }, -1); // already expired
  assert.equal(await store.getSession('sid'), null);
  await store.close();
});

void existsSync;
