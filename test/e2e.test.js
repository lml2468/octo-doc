// End-to-end test: spawns a real octo-doc server, runs the publish → pull →
// v2 → list-versions flow over HTTP, and exits non-zero on any failure. This
// is the CI gate for the functional success criteria. Designed to run in
// well under 30s. Uses only the SQLite+FS reference stack (no Docker).
import { spawn } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import net from 'node:net';
import assert from 'node:assert/strict';

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = join(__dirname, '..');

function freePort() {
  return new Promise((resolve, reject) => {
    const s = net.createServer();
    s.listen(0, '127.0.0.1', () => { const p = s.address().port; s.close(() => resolve(p)); });
    s.on('error', reject);
  });
}
async function waitReady(base, ms = 8000) {
  const deadline = Date.now() + ms;
  for (;;) {
    try { const r = await fetch(`${base}/healthz`); if (r.ok) return; } catch { /* retry */ }
    if (Date.now() > deadline) throw new Error('server not ready');
    await new Promise(r => setTimeout(r, 100));
  }
}

const HELLO = '<!doctype html><html><body><h1>Hello</h1><figure><svg viewBox="0 0 4 4"><rect/></svg></figure></body></html>';
const HELLO2 = '<!doctype html><html><body><h1>Hello</h1><p>Version two.</p></body></html>';

let pass = 0;
const ok = (name) => { console.log(`  ✓ ${name}`); pass++; };

async function main() {
  const dir = mkdtempSync(join(tmpdir(), 'octo-e2e-'));
  const port = await freePort();
  const base = `http://127.0.0.1:${port}`;
  const srv = spawn(process.execPath, [join(ROOT, 'src/index.js')], {
    env: { ...process.env, DATA_DIR: dir, STORAGE: 'sqlite+fs', PORT: String(port), LOG_LEVEL: 'silent', COOKIE_SECURE: 'false' },
    stdio: 'ignore',
  });
  const cleanup = () => { try { srv.kill('SIGKILL'); } catch {} try { rmSync(dir, { recursive: true, force: true }); } catch {} };
  process.on('exit', cleanup);

  try {
    await waitReady(base);
    const t0 = Date.now();

    // ping
    const ping = await (await fetch(`${base}/api/ping`)).json();
    assert.deepEqual(ping, { ok: true, service: 'tdoc' });
    ok('ping returns the service marker');

    // bootstrap a write token
    const token = (await (await fetch(`${base}/api/admin/bootstrap`)).json()).token;
    assert.ok(token);
    ok('bootstrap mints a write token');

    const publish = async (html) => {
      const form = new FormData();
      form.set('slug', 'hello');
      form.set('file', new Blob([html], { type: 'text/html' }), 'hello.html');
      return (await fetch(`${base}/api/docs`, { method: 'POST', body: form, headers: { authorization: `Bearer ${token}` } })).json();
    };

    // publish v1 → /d/hello/v/1
    const r1 = await publish(HELLO);
    assert.equal(r1.url, '/d/hello/v/1');
    ok('publish v1 returns /d/hello/v/1');

    // fetch v1 contains Hello + stamped aid + overlay
    const v1 = await (await fetch(`${base}/d/hello/v/1`)).text();
    assert.match(v1, /Hello/);
    assert.match(v1, /data-tdoc-aid=/);
    assert.match(v1, /window\.__TDOC__/);
    ok('rendered v1 has Hello, stamped aids, and the overlay');

    // add a comment, then "pull" it back via the API (?version=all)
    const created = await (await fetch(`${base}/api/comments`, {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ slug: 'hello', version: 1, text: 'looks good', anchor: { kind: 'text', text: 'Hello' } }),
    })).json();
    assert.ok(created.id);
    const pulled = await (await fetch(`${base}/api/comments?slug=hello&version=all`)).json();
    assert.equal(pulled.length, 1);
    assert.equal(pulled[0].text, 'looks good');
    ok('comment create + cross-version pull round-trips');

    // publish v2 → version increments, comment survives
    const r2 = await publish(HELLO2);
    assert.ok(r2.url.endsWith('/v/2'));
    ok('publish v2 increments the version');

    // old version still reachable
    const stillV1 = await fetch(`${base}/d/hello/v/1`);
    assert.equal(stillV1.status, 200);
    ok('older version v1 still reachable after v2');

    // list versions
    const versions = await (await fetch(`${base}/api/docs/hello/versions`)).json();
    assert.deepEqual(versions.versions.map(v => v.n), [1, 2]);
    ok('version listing shows [1, 2]');

    // comment survived the republish (pull at all)
    const afterPub = await (await fetch(`${base}/api/comments?slug=hello&version=all`)).json();
    assert.equal(afterPub.length, 1);
    ok('comment survives republish (non-destructive merge)');

    // unpublish
    const del = await fetch(`${base}/api/doc?slug=hello`, { method: 'DELETE', headers: { authorization: `Bearer ${token}` } });
    assert.equal(del.status, 200);
    const gone = await fetch(`${base}/d/hello/v/1`);
    assert.equal(gone.status, 404);
    ok('unpublish removes all versions');

    const elapsed = Date.now() - t0;
    console.log(`\nE2E PASS — ${pass} checks in ${elapsed}ms`);
    cleanup();
    process.exit(0);
  } catch (e) {
    console.error(`\nE2E FAIL: ${e.message}`);
    cleanup();
    process.exit(1);
  }
}
main();
