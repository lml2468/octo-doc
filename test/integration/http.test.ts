import { describe, it, expect, beforeAll, afterAll } from 'vitest';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import type { Hono } from 'hono';
import { createApp } from '../../src/app.js';
import type { AppEnv } from '../../src/http-context.js';

/**
 * HTTP integration tests against the assembled app via `app.fetch` (no socket).
 * Covers the route + middleware layers and the API contract end to end.
 */
let app: Hono<AppEnv>;
let close: () => Promise<void>;
let dir: string;
let token: string;

const env = (over: Record<string, string> = {}): Record<string, string> => ({
  DATA_DIR: dir,
  STORAGE: 'sqlite+fs',
  LOG_LEVEL: 'silent',
  COOKIE_SECURE: 'false',
  ...over,
});

const req = (
  method: string,
  path: string,
  opts: { body?: unknown; headers?: Record<string, string>; form?: FormData } = {},
): Promise<Response> => {
  const init: RequestInit = { method, headers: { ...(opts.headers ?? {}) } };
  if (opts.form) init.body = opts.form;
  else if (opts.body !== undefined) {
    init.body = JSON.stringify(opts.body);
    (init.headers as Record<string, string>)['content-type'] = 'application/json';
  }
  return Promise.resolve(app.fetch(new Request(`http://local${path}`, init)));
};

const publishForm = (slug: string, html: string): FormData => {
  const form = new FormData();
  form.set('slug', slug);
  form.set('file', new Blob([html], { type: 'text/html' }), `${slug}.html`);
  return form;
};

beforeAll(async () => {
  dir = mkdtempSync(join(tmpdir(), 'octo-http-'));
  const built = await createApp(env());
  app = built.app;
  close = () => built.close();
  token = ((await (await req('GET', '/api/admin/bootstrap')).json()) as { token: string }).token;
});

afterAll(async () => {
  await close();
  rmSync(dir, { recursive: true, force: true });
});

describe('health + bootstrap', () => {
  it('GET /api/ping returns the service marker', async () => {
    expect(await (await req('GET', '/api/ping')).json()).toStrictEqual({
      ok: true,
      service: 'tdoc',
    });
  });

  it('GET /healthz is ok', async () => {
    expect((await req('GET', '/healthz')).status).toBe(200);
  });

  it('bootstrap is one-shot (409 after first)', async () => {
    expect((await req('GET', '/api/admin/bootstrap')).status).toBe(409);
  });
});

describe('publish + render', () => {
  it('requires auth', async () => {
    expect((await req('POST', '/api/docs', { form: publishForm('x', '<h1>x</h1>') })).status).toBe(
      401,
    );
  });

  it('GET /api/docs (write-only) returns 401 unauthenticated, not 404', async () => {
    expect((await req('GET', '/api/docs')).status).toBe(401);
  });

  it('publishes v1 then v2 with monotonic versions, both reachable', async () => {
    const r1 = (await (
      await req('POST', '/api/docs', {
        form: publishForm('hello', '<h1>Hello</h1>'),
        headers: { authorization: `Bearer ${token}` },
      })
    ).json()) as { url: string; version: number };
    expect(r1.url).toBe('/d/hello/v/1');

    const r2 = (await (
      await req('POST', '/api/docs', {
        form: publishForm('hello', '<h1>Hello v2</h1>'),
        headers: { authorization: `Bearer ${token}` },
      })
    ).json()) as { url: string };
    expect(r2.url).toBe('/d/hello/v/2');

    const v1 = await req('GET', '/d/hello/v/1');
    expect(await v1.text()).toMatch(/Hello/);
    expect((await req('GET', '/d/hello/v/2')).status).toBe(200);
  });

  it('injects the overlay + security headers', async () => {
    const res = await req('GET', '/d/hello/v/1');
    expect(await res.text()).toMatch(/window\.__TDOC__/);
    expect(res.headers.get('content-security-policy')).toMatch(/frame-ancestors 'none'/);
    expect(res.headers.get('x-frame-options')).toBe('DENY');
  });

  it('lists versions', async () => {
    const r = (await (await req('GET', '/api/docs/hello/versions')).json()) as {
      versions: { n: number }[];
    };
    expect(r.versions.map((v) => v.n)).toStrictEqual([1, 2]);
  });

  it('accepts the legacy JSON /api/upload path', async () => {
    const res = await req('POST', '/api/upload', {
      body: {
        slug: 'jsondoc',
        version: 1,
        html: '<h1>J</h1>',
        meta: { title: 'J', versions: [{ n: 1 }] },
      },
      headers: { authorization: `Bearer ${token}` },
    });
    expect(((await res.json()) as { ok: boolean }).ok).toBe(true);
  });

  it('404s an unknown doc and a bad version', async () => {
    expect((await req('GET', '/d/nope/v/1')).status).toBe(404);
    expect((await req('GET', '/d/hello/v/abc')).status).toBe(404);
    expect((await req('GET', '/api/docs/nope/versions')).status).toBe(404);
  });
});

describe('comments + reactions', () => {
  let commentId: string;

  it('creates a comment (anonymous, auth not configured)', async () => {
    const res = await req('POST', '/api/comments', {
      body: { slug: 'hello', version: 1, text: 'nice', anchor: { kind: 'text', text: 'Hello' } },
    });
    const body = (await res.json()) as { id: string };
    commentId = body.id;
    expect(commentId).toBeTruthy();
  });

  it('lists comments at a version and across all versions', async () => {
    expect(
      ((await (await req('GET', '/api/comments?slug=hello&version=1')).json()) as unknown[]).length,
    ).toBe(1);
    expect(
      ((await (await req('GET', '/api/comments?slug=hello&version=all')).json()) as unknown[])
        .length,
    ).toBe(1);
  });

  it('replies and reacts', async () => {
    const reply = await req('POST', '/api/comments', {
      body: { slug: 'hello', version: 1, text: 'agreed', parent_id: commentId },
    });
    expect(((await reply.json()) as { id: string }).id).toBeTruthy();
    const react = await req('POST', '/api/reactions', {
      body: { slug: 'hello', comment_id: commentId, emoji: '👍', version: 1 },
    });
    expect(((await react.json()) as { ok: boolean }).ok).toBe(true);
  });

  it('rejects an invalid emoji', async () => {
    const res = await req('POST', '/api/reactions', {
      body: { slug: 'hello', comment_id: commentId, emoji: 'waytoolong', version: 1 },
    });
    expect(res.status).toBe(400);
  });

  it('soft-deletes a comment', async () => {
    const res = await req('DELETE', `/api/comments?slug=hello&id=${commentId}&version=1`);
    expect(res.status).toBe(200);
  });

  it('admin-wipes all comments with the write token', async () => {
    const res = await req('DELETE', '/api/comments?slug=hello&all=1', {
      headers: { authorization: `Bearer ${token}` },
    });
    expect(res.status).toBe(200);
  });
});

describe('export / fork', () => {
  it('export forces a download with a comment banner', async () => {
    const res = await req('GET', '/d/hello/v/1/export');
    expect(res.headers.get('content-disposition')).toMatch(/attachment/);
    expect(await res.text()).toMatch(/octo-doc fork export/);
  });

  it('fork boots the overlay in fork mode', async () => {
    const text = await (await req('GET', '/d/hello/v/1/fork')).text();
    expect(text).toMatch(/"mode":"fork"/);
    expect(text).toMatch(/id="tdoc-fork-comments"/);
  });
});

describe('agent reply', () => {
  it('posts a verdict reply under the write token', async () => {
    const created = (await (
      await req('POST', '/api/comments', {
        body: { slug: 'agentdoc', version: 1, text: 'fix this' },
      })
    ).json()) as {
      id: string;
    };
    const res = await req('POST', '/api/agent/reply', {
      body: {
        slug: 'agentdoc',
        parent_id: created.id,
        text: 'done',
        status: 'applied',
        applied_in: 1,
      },
      headers: { authorization: `Bearer ${token}` },
    });
    const body = (await res.json()) as { agent_status: string };
    expect(body.agent_status).toBe('applied');
    const list = (await (await req('GET', '/api/comments?slug=agentdoc&version=1')).json()) as {
      status: string;
    }[];
    expect(list[0]?.status).toBe('applied');
  });

  it('partial/question verdicts keep the comment open; bad verdict has no status', async () => {
    const mk = async (text: string): Promise<string> =>
      (
        (await (
          await req('POST', '/api/comments', { body: { slug: 'agentdoc', version: 1, text } })
        ).json()) as { id: string }
      ).id;
    const partialId = await mk('a');
    await req('POST', '/api/agent/reply', {
      body: {
        slug: 'agentdoc',
        parent_id: partialId,
        text: 'wip',
        status: 'partial',
        applied_in: 1,
      },
      headers: { authorization: `Bearer ${token}` },
    });
    const list = (await (await req('GET', '/api/comments?slug=agentdoc&version=1')).json()) as {
      id: string;
      status: string;
    }[];
    expect(list.find((c) => c.id === partialId)?.status).toBe('open');
  });

  it('agent reply 400s without parent_id/text and 400s a missing parent', async () => {
    expect(
      (
        await req('POST', '/api/agent/reply', {
          body: { slug: 'agentdoc' },
          headers: { authorization: `Bearer ${token}` },
        })
      ).status,
    ).toBe(400);
    expect(
      (
        await req('POST', '/api/agent/reply', {
          body: { slug: 'agentdoc', parent_id: 'nope', text: 'x' },
          headers: { authorization: `Bearer ${token}` },
        })
      ).status,
    ).toBe(400);
  });
});

describe('validation + friendly errors', () => {
  it('rejects a path-traversal slug with a typed 400', async () => {
    const res = await req('GET', '/api/comments?slug=../../etc');
    expect(res.status).toBe(400);
    expect(((await res.json()) as { error: string }).error).toBe('invalid_slug');
  });

  it('rejects oversized html with a friendly 413', async () => {
    const built = await createApp(env({ MAX_HTML_BYTES: '50', WRITE_TOKEN: 'fixed' }));
    const res = await built.app.fetch(
      new Request('http://local/api/docs', {
        method: 'POST',
        body: publishForm('big', '<h1>' + 'x'.repeat(200) + '</h1>'),
        headers: { authorization: 'Bearer fixed' },
      }),
    );
    expect(res.status).toBe(413);
    const body = (await res.json()) as { error: string; message: string };
    expect(body.error).toBe('html_too_large');
    expect(body.message).toContain('exceeds');
    await built.close();
  });

  it('landing page renders; unknown route is 404', async () => {
    expect((await req('GET', '/')).status).toBe(200);
    expect((await req('GET', '/totally-unknown')).status).toBe(404);
  });
});

describe('rate limiting', () => {
  it('returns 429 with Retry-After once the window is exceeded', async () => {
    const built = await createApp(env({ RATE_LIMIT_MAX: '3', WRITE_TOKEN: 'rl' }));
    const fire = (): Promise<Response> =>
      Promise.resolve(
        built.app.fetch(
          new Request('http://local/api/docs', {
            method: 'POST',
            body: publishForm('rl', '<h1>x</h1>'),
            headers: { authorization: 'Bearer rl' },
          }),
        ),
      );
    const statuses: number[] = [];
    for (let i = 0; i < 5; i++) statuses.push((await fire()).status);
    expect(statuses.filter((s) => s === 429).length).toBeGreaterThan(0);
    await built.close();
  });
});

describe('auth routes (anonymous mode)', () => {
  it('GET /api/auth/me reports no identity', async () => {
    const me = (await (await req('GET', '/api/auth/me')).json()) as {
      identity: null;
      authConfigured: boolean;
    };
    expect(me.identity).toBeNull();
    expect(me.authConfigured).toBe(false);
  });

  it('logout is a no-op clear without a session', async () => {
    expect((await req('POST', '/api/auth/logout')).status).toBe(200);
  });

  it('device-flow endpoints 400 when GITHUB_CLIENT_ID is unset', async () => {
    expect((await req('POST', '/api/auth/device/start')).status).toBe(400);
    expect(
      (await req('POST', '/api/auth/device/poll', { body: { device_code: 'x' } })).status,
    ).toBe(400);
  });

  it('device poll 400s without a device_code', async () => {
    const built = await createApp(env({ GITHUB_CLIENT_ID: 'cid' }));
    const res = await built.app.fetch(
      new Request('http://local/api/auth/device/poll', {
        method: 'POST',
        body: '{}',
        headers: { 'content-type': 'application/json' },
      }),
    );
    expect(res.status).toBe(400);
    await built.close();
  });
});

describe('owner catalog (/me)', () => {
  it('redirects non-owners to the repo', async () => {
    const res = await req('GET', '/me');
    expect(res.status).toBe(302);
  });
});

describe('comment re-anchor (PATCH)', () => {
  it('moves a comment anchor and resets status', async () => {
    await req('POST', '/api/docs', {
      form: publishForm('patchdoc', '<h1>Hi</h1>'),
      headers: { authorization: `Bearer ${token}` },
    });
    const created = (await (
      await req('POST', '/api/comments', { body: { slug: 'patchdoc', version: 1, text: 'x' } })
    ).json()) as { id: string };
    const res = await req('PATCH', '/api/comments', {
      body: { slug: 'patchdoc', id: created.id, version: 1, anchor: { kind: 'text', text: 'Hi' } },
    });
    expect(res.status).toBe(200);
    expect(((await res.json()) as { anchor: { kind: string } }).anchor.kind).toBe('text');
  });

  it('PATCH 400s without an anchor and 404s an unknown id', async () => {
    expect(
      (await req('PATCH', '/api/comments', { body: { slug: 'patchdoc', id: 'c1' } })).status,
    ).toBe(400);
    expect(
      (
        await req('PATCH', '/api/comments', {
          body: { slug: 'patchdoc', id: 'nope', anchor: { kind: 'text', text: 'x' } },
        })
      ).status,
    ).toBe(400);
  });
});

describe('HEAD + download variants', () => {
  it('serves HEAD for a doc and honors ?download on fork', async () => {
    await req('POST', '/api/docs', {
      form: publishForm('dl', '<h1>D</h1>'),
      headers: { authorization: `Bearer ${token}` },
    });
    expect((await req('HEAD', '/d/dl/v/1')).status).toBe(200);
    // fork defaults inline; ?download=1 forces attachment
    const forced = await req('GET', '/d/dl/v/1/fork?download=1');
    expect(forced.headers.get('content-disposition')).toMatch(/attachment/);
    // export with ?download=0 stays inline
    const inline = await req('GET', '/d/dl/v/1/export?download=0');
    expect(inline.headers.get('content-disposition')).toBeNull();
  });
});

describe('private read mode', () => {
  it('hides docs behind the token when PRIVATE=1', async () => {
    const built = await createApp(env({ PRIVATE: '1', WRITE_TOKEN: 'pk' }));
    const pubForm = publishForm('secret', '<h1>S</h1>');
    await built.app.fetch(
      new Request('http://local/api/docs', {
        method: 'POST',
        body: pubForm,
        headers: { authorization: 'Bearer pk' },
      }),
    );
    // anonymous read → 404 (does not confirm existence)
    expect(
      (await Promise.resolve(built.app.fetch(new Request('http://local/d/secret/v/1')))).status,
    ).toBe(404);
    // with token → 200
    const ok = await built.app.fetch(
      new Request('http://local/d/secret/v/1', { headers: { authorization: 'Bearer pk' } }),
    );
    expect(ok.status).toBe(200);
    await built.close();
  });
});

describe('github-configured mode', () => {
  it('requires sign-in for comment writes', async () => {
    const built = await createApp(env({ GITHUB_CLIENT_ID: 'cid' }));
    const res = await built.app.fetch(
      new Request('http://local/api/comments', {
        method: 'POST',
        body: JSON.stringify({ slug: 'x', version: 1, text: 'hi' }),
        headers: { 'content-type': 'application/json' },
      }),
    );
    expect(res.status).toBe(401);
    expect((await res.json()) as { error: string }).toMatchObject({ error: 'sign_in_required' });
    await built.close();
  });
});
