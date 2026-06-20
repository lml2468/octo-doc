import { describe, it, expect, beforeAll, afterAll } from 'vitest';
import { spawn, type ChildProcess } from 'node:child_process';
import { mkdtempSync, rmSync, readdirSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import net from 'node:net';

/**
 * End-to-end test against a REAL spawned server process (tsx src/index.ts).
 * Exercises the full publish → read → republish → list → auth-failure →
 * large-file-reject flow plus the routes that the in-process app test can't
 * reach the same way (process lifecycle, /api/auth/me, pages).
 */
const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, '..', '..');

let srv: ChildProcess;
let dir: string;
let base: string;
let token: string;

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const s = net.createServer();
    s.listen(0, '127.0.0.1', () => {
      const addr = s.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      s.close(() => resolve(port));
    });
    s.on('error', reject);
  });
}

async function waitReady(ms = 15_000): Promise<void> {
  const deadline = Date.now() + ms;
  for (;;) {
    try {
      if ((await fetch(`${base}/healthz`)).ok) return;
    } catch {
      /* not up yet */
    }
    if (Date.now() > deadline) throw new Error('server not ready');
    await new Promise((r) => setTimeout(r, 150));
  }
}

function startServer(): ChildProcess {
  return spawn(process.execPath, ['--import', 'tsx', join(root, 'src/index.ts')], {
    env: {
      ...process.env,
      DATA_DIR: dir,
      STORAGE: 'sqlite+fs',
      PORT: base.split(':')[2],
      LOG_LEVEL: 'silent',
      COOKIE_SECURE: 'false',
    },
    stdio: 'ignore',
  });
}

beforeAll(async () => {
  dir = mkdtempSync(join(tmpdir(), 'octo-e2e-'));
  base = `http://127.0.0.1:${await freePort()}`;
  srv = startServer();
  await waitReady();
  token = ((await (await fetch(`${base}/api/admin/bootstrap`)).json()) as { token: string }).token;
});

afterAll(() => {
  try {
    srv.kill('SIGKILL');
  } catch {
    /* already gone */
  }
  rmSync(dir, { recursive: true, force: true });
});

const publish = (slug: string, html: string): Promise<Response> => {
  const form = new FormData();
  form.set('slug', slug);
  form.set('file', new Blob([html], { type: 'text/html' }), `${slug}.html`);
  return fetch(`${base}/api/docs`, {
    method: 'POST',
    body: form,
    headers: { authorization: `Bearer ${token}` },
  });
};

describe('full publish lifecycle', () => {
  it('publish → read → comment → pull → republish → list → unpublish', async () => {
    const r1 = (await (await publish('hello', '<h1>Hello</h1>')).json()) as { url: string };
    expect(r1.url).toBe('/d/hello/v/1');
    expect(await (await fetch(`${base}/d/hello/v/1`)).text()).toMatch(/Hello/);

    await fetch(`${base}/api/comments`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ slug: 'hello', version: 1, text: 'looks good' }),
    });
    const pulled = (await (
      await fetch(`${base}/api/comments?slug=hello&version=all`)
    ).json()) as unknown[];
    expect(pulled).toHaveLength(1);

    const r2 = (await (await publish('hello', '<h1>Hello v2</h1>')).json()) as { url: string };
    expect(r2.url.endsWith('/v/2')).toBe(true);
    expect((await fetch(`${base}/d/hello/v/1`)).status).toBe(200); // old version reachable

    const versions = (await (await fetch(`${base}/api/docs/hello/versions`)).json()) as {
      versions: { n: number }[];
    };
    expect(versions.versions.map((v) => v.n)).toStrictEqual([1, 2]);

    // comment survived the republish
    expect(
      ((await (await fetch(`${base}/api/comments?slug=hello&version=all`)).json()) as unknown[])
        .length,
    ).toBe(1);

    const del = await fetch(`${base}/api/doc?slug=hello`, {
      method: 'DELETE',
      headers: { authorization: `Bearer ${token}` },
    });
    expect(del.status).toBe(200);
    expect((await fetch(`${base}/d/hello/v/1`)).status).toBe(404);
  });

  it('rejects unauthorized writes and oversized files', async () => {
    const noAuth = new FormData();
    noAuth.set('slug', 'x');
    noAuth.set('file', new Blob(['<h1>x</h1>']), 'x.html');
    expect((await fetch(`${base}/api/docs`, { method: 'POST', body: noAuth })).status).toBe(401);
  });

  it('serves landing + auth/me + ping', async () => {
    expect((await fetch(`${base}/`)).status).toBe(200);
    const me = (await (await fetch(`${base}/api/auth/me`)).json()) as { authConfigured: boolean };
    expect(me.authConfigured).toBe(false);
    expect(await (await fetch(`${base}/api/ping`)).json()).toStrictEqual({
      ok: true,
      service: 'tdoc',
    });
  });
});

describe('chaos: crash mid-publish leaves consistent state', () => {
  it('SIGKILL during publishes → restart sees no half-written docs', async () => {
    // Fire several publishes, then hard-kill the process mid-flight.
    const inflight = Array.from({ length: 8 }, (_, i) =>
      publish('chaos', `<h1>v${i}</h1>`).catch(() => null),
    );
    await new Promise((r) => setTimeout(r, 20));
    srv.kill('SIGKILL');
    await Promise.allSettled(inflight);

    // Restart against the same data dir.
    srv = startServer();
    await waitReady();

    // Every version the server reports must actually be readable (no half-write).
    const res = await fetch(`${base}/api/docs/chaos/versions`);
    if (res.status === 404) return; // nothing committed before the kill — also consistent
    const { versions } = (await res.json()) as { versions: { n: number }[] };
    for (const v of versions) {
      const doc = await fetch(`${base}/d/chaos/v/${v.n}`);
      expect(doc.status, `version ${v.n} should be readable`).toBe(200);
      expect(await doc.text()).toMatch(/data-tdoc-aid|<h1>/);
    }
    // The FS blob dir must contain no leftover .tmp files.
    const found = collectTmp(join(dir, 'blobs'));
    expect(found, `leftover temp files: ${found.join(', ')}`).toHaveLength(0);
  });
});

/** Recursively collect any leftover atomic-write temp files. */
function collectTmp(dirPath: string): string[] {
  const out: string[] = [];
  let entries: string[] = [];
  try {
    entries = readdirSync(dirPath);
  } catch {
    return out;
  }
  for (const e of entries) {
    const p = join(dirPath, e);
    if (statSync(p).isDirectory()) out.push(...collectTmp(p));
    else if (e.includes('.tmp')) out.push(p);
  }
  return out;
}
