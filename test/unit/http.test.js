// HTTP-level tests against the Hono app via app.fetch (no socket). Covers the
// API contract: bootstrap, publish, view, versions, comments, auth, CSP.
import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createApp } from '../../src/app.js';

let app, close, dir, token;

before(async () => {
  dir = mkdtempSync(join(tmpdir(), 'octo-http-'));
  const built = await createApp({
    DATA_DIR: dir, STORAGE: 'sqlite+fs', LOG_LEVEL: 'silent',
    COOKIE_SECURE: 'false', ALLOW_BOOTSTRAP: 'true',
  }, { requestLog: false });
  app = built.app; close = built.close;
});

after(async () => { await close(); rmSync(dir, { recursive: true, force: true }); });

const req = (method, path, { body, headers, form } = {}) => {
  const init = { method, headers: { ...(headers || {}) } };
  if (form) { init.body = form; }
  else if (body !== undefined) { init.body = JSON.stringify(body); init.headers['content-type'] = 'application/json'; }
  return app.fetch(new Request(`http://local${path}`, init));
};

test('GET /api/ping returns the tdoc service marker', async () => {
  const r = await req('GET', '/api/ping');
  assert.equal(r.status, 200);
  assert.deepEqual(await r.json(), { ok: true, service: 'tdoc' });
});

test('bootstrap mints one token, then 409s', async () => {
  const r1 = await req('GET', '/api/admin/bootstrap');
  assert.equal(r1.status, 200);
  token = (await r1.json()).token;
  assert.ok(token && token.length >= 32);
  const r2 = await req('GET', '/api/admin/bootstrap');
  assert.equal(r2.status, 409);
});

test('publish requires auth', async () => {
  const form = new FormData();
  form.set('slug', 'x');
  form.set('file', new Blob(['<h1>x</h1>'], { type: 'text/html' }), 'x.html');
  const r = await req('POST', '/api/docs', { form });
  assert.equal(r.status, 401);
});

test('publish v1 + v2, version increments, both reachable', async () => {
  const publish = async (html) => {
    const form = new FormData();
    form.set('slug', 'hello');
    form.set('file', new Blob([html], { type: 'text/html' }), 'h.html');
    return req('POST', '/api/docs', { form, headers: { authorization: `Bearer ${token}` } });
  };
  const r1 = await (await publish('<h1>Hello</h1>')).json();
  assert.equal(r1.version, 1);
  assert.equal(r1.url, '/d/hello/v/1');
  const r2 = await (await publish('<h1>Hello v2</h1>')).json();
  assert.equal(r2.version, 2);

  const v1 = await req('GET', '/d/hello/v/1');
  assert.match(await v1.text(), /Hello/);
  assert.match(v1.headers.get('content-security-policy') || '', /frame-ancestors 'none'/);
  assert.equal(v1.headers.get('x-frame-options'), 'DENY');

  const versions = await (await req('GET', '/api/docs/hello/versions')).json();
  assert.deepEqual(versions.versions.map(v => v.n), [1, 2]);
});

test('rendered doc injects the overlay boot config', async () => {
  const html = await (await req('GET', '/d/hello/v/1')).text();
  assert.match(html, /window\.__TDOC__/);
  assert.match(html, /"slug":"hello"/);
});

test('comments: anonymous create + fold (auth not configured => local mode)', async () => {
  const created = await (await req('POST', '/api/comments', {
    body: { slug: 'hello', version: 1, text: 'a comment', anchor: { kind: 'text', text: 'Hello' } },
  })).json();
  assert.ok(created.id);
  const list = await (await req('GET', '/api/comments?slug=hello&version=1')).json();
  assert.equal(list.length, 1);
  assert.equal(list[0].text, 'a comment');
});

test('export/fork bundles a comments banner + JSON block', async () => {
  const fork = await (await req('GET', '/d/hello/v/1/fork')).text();
  assert.match(fork, /octo-doc fork export/);
  assert.match(fork, /id="tdoc-fork-comments"/);
  assert.match(fork, /"mode":"fork"/);
});

test('slug path-traversal is rejected at the route', async () => {
  const r = await req('GET', '/api/comments?slug=../../etc');
  assert.equal(r.status, 400);
});

test('html over the size cap is rejected (413)', async () => {
  const built = await createApp({
    DATA_DIR: dir, STORAGE: 'sqlite+fs', LOG_LEVEL: 'silent', MAX_HTML_BYTES: '50',
    WRITE_TOKEN: 'fixed', COOKIE_SECURE: 'false',
  }, { requestLog: false });
  const form = new FormData();
  form.set('slug', 'big');
  form.set('file', new Blob(['<h1>' + 'x'.repeat(200) + '</h1>'], { type: 'text/html' }), 'b.html');
  const r = await built.app.fetch(new Request('http://local/api/docs', {
    method: 'POST', body: form, headers: { authorization: 'Bearer fixed' },
  }));
  assert.equal(r.status, 413);
  await built.close();
});
