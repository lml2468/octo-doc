import { describe, it, expect, vi, afterEach } from 'vitest';
import { ghStartDeviceFlow, ghPollAccessToken, ghFetchUser } from '../../src/services/github.js';
import { UpstreamError, ValidationError } from '../../src/errors.js';

afterEach(() => vi.restoreAllMocks());

const jsonRes = (obj: unknown): Response =>
  new Response(JSON.stringify(obj), { headers: { 'content-type': 'application/json' } });
const formRes = (s: string): Response =>
  new Response(s, { headers: { 'content-type': 'application/x-www-form-urlencoded' } });

describe('ghStartDeviceFlow', () => {
  it('parses a JSON response', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonRes({
        device_code: 'd',
        user_code: 'U',
        verification_uri: 'https://gh',
        expires_in: 900,
        interval: 5,
      }),
    );
    expect((await ghStartDeviceFlow('cid')).user_code).toBe('U');
  });

  it('throws ValidationError on a GitHub error', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonRes({ error: 'unsupported_grant_type' }));
    await expect(ghStartDeviceFlow('cid')).rejects.toBeInstanceOf(ValidationError);
  });

  it('wraps network failures as UpstreamError', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('ECONNRESET'));
    await expect(ghStartDeviceFlow('cid')).rejects.toBeInstanceOf(UpstreamError);
  });
});

describe('ghPollAccessToken', () => {
  it('treats authorization_pending and slow_down as pending', async () => {
    const spy = vi.spyOn(globalThis, 'fetch');
    spy.mockResolvedValueOnce(formRes('error=authorization_pending'));
    expect((await ghPollAccessToken('cid', 'd')).pending).toBe(true);
    spy.mockResolvedValueOnce(formRes('error=slow_down'));
    expect((await ghPollAccessToken('cid', 'd')).pending).toBe(true);
  });

  it('returns the access token from a form-encoded reply', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      formRes('access_token=abc123&token_type=bearer'),
    );
    const r = await ghPollAccessToken('cid', 'd');
    expect(r.pending).toBe(false);
    if (!r.pending) expect(r.accessToken).toBe('abc123');
  });

  it('pends when no token and no error', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonRes({}));
    expect((await ghPollAccessToken('cid', 'd')).pending).toBe(true);
  });

  it('throws ValidationError on a hard error', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonRes({ error: 'access_denied' }));
    await expect(ghPollAccessToken('cid', 'd')).rejects.toBeInstanceOf(ValidationError);
  });
});

describe('ghFetchUser', () => {
  it('returns the parsed user', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonRes({ login: 'alice', name: 'Alice' }));
    expect((await ghFetchUser('tok')).login).toBe('alice');
  });

  it('wraps network failures as UpstreamError', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('down'));
    await expect(ghFetchUser('tok')).rejects.toBeInstanceOf(UpstreamError);
  });
});
