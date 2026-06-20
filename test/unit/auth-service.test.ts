import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { AuthService } from '../../src/services/auth-service.js';
import { makeSqliteMetadataStore } from '../../src/storage/sqlite.js';
import { loadConfig } from '../../src/config.js';
import type { MetadataStore } from '../../src/storage/types.js';

let meta: MetadataStore;

beforeEach(() => {
  meta = makeSqliteMetadataStore({ sqlitePath: ':memory:', dataDir: '.' });
});
afterEach(async () => {
  await meta.close();
  vi.restoreAllMocks();
});

function svc(env: Record<string, string> = {}): AuthService {
  return new AuthService(meta, loadConfig({ STORAGE: 'sqlite+fs', ...env }));
}

describe('write tokens', () => {
  it('validates a static WRITE_TOKEN in constant time', async () => {
    const s = svc({ WRITE_TOKEN: 'secret' });
    expect(await s.isValidWriteToken('secret')).toBe(true);
    expect(await s.isValidWriteToken('wrong')).toBe(false);
    expect(await s.isValidWriteToken('')).toBe(false);
  });

  it('bootstraps once, then conflicts', async () => {
    const s = svc();
    const { token } = await s.bootstrap();
    expect(token).toHaveLength(64);
    expect(await s.isValidWriteToken(token)).toBe(true);
    await expect(s.bootstrap()).rejects.toMatchObject({ status: 409 });
  });

  it('refuses bootstrap when a static token is configured', async () => {
    await expect(svc({ WRITE_TOKEN: 'x' }).bootstrap()).rejects.toMatchObject({
      code: 'static_token_configured',
    });
  });

  it('refuses bootstrap when disabled', async () => {
    await expect(svc({ ALLOW_BOOTSTRAP: 'false' }).bootstrap()).rejects.toMatchObject({
      code: 'bootstrap_disabled',
    });
  });
});

describe('owner detection', () => {
  it('matches the configured owner case-insensitively', () => {
    const s = svc({ OWNER: 'Alice' });
    expect(s.isOwner({ login: 'alice', created: 'x' })).toBe(true);
    expect(s.isOwner({ login: 'bob', created: 'x' })).toBe(false);
    expect(s.isOwner(null)).toBe(false);
  });

  it('nobody is owner when OWNER is unset', () => {
    expect(svc().isOwner({ login: 'alice', created: 'x' })).toBe(false);
  });
});

describe('GitHub device flow', () => {
  it('errors clearly when auth is not configured', async () => {
    await expect(svc().startDeviceFlow()).rejects.toMatchObject({ code: 'auth_not_configured' });
  });

  it('starts the device flow', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          device_code: 'd',
          user_code: 'U-C',
          verification_uri: 'https://gh',
          expires_in: 900,
          interval: 5,
        }),
        {
          headers: { 'content-type': 'application/json' },
        },
      ),
    );
    const r = await svc({ GITHUB_CLIENT_ID: 'cid' }).startDeviceFlow();
    expect(r.user_code).toBe('U-C');
  });

  it('returns pending while authorization is outstanding', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ error: 'authorization_pending' }), {
        headers: { 'content-type': 'application/json' },
      }),
    );
    const r = await svc({ GITHUB_CLIENT_ID: 'cid' }).pollDeviceFlow('d');
    expect(r.pending).toBe(true);
  });

  it('creates a session on successful poll', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch');
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ access_token: 'tok' }), {
        headers: { 'content-type': 'application/json' },
      }),
    );
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ login: 'alice', name: 'Alice', avatar_url: 'http://a' }), {
        headers: { 'content-type': 'application/json' },
      }),
    );
    const s = svc({ GITHUB_CLIENT_ID: 'cid' });
    const r = await s.pollDeviceFlow('d');
    expect(r.pending).toBe(false);
    if (!r.pending) {
      expect(r.identity.login).toBe('alice');
      expect(await s.getSession(r.sid)).toMatchObject({ login: 'alice' });
      await s.logout(r.sid);
      expect(await s.getSession(r.sid)).toBeNull();
    }
  });
});
