import { describe, it, expect } from 'vitest';
import { Hono } from 'hono';
import { errorHandler } from '../../src/middleware/error.js';
import { ConflictError, RateLimitedError, UpstreamError } from '../../src/errors.js';
import type { AppEnv } from '../../src/http-context.js';
import { initLogger } from '../../src/logger.js';

initLogger('silent');

/** Mount a route that throws `err`, return the response. */
function appThrowing(err: unknown): Hono<AppEnv> {
  const app = new Hono<AppEnv>();
  app.get('/boom', () => {
    throw err;
  });
  app.onError(errorHandler);
  return app;
}

describe('errorHandler', () => {
  it('maps a 4xx AppError to its status + code', async () => {
    const res = await appThrowing(new ConflictError('nope', 'already_done')).request('/boom');
    expect(res.status).toBe(409);
    expect((await res.json()) as { error: string }).toMatchObject({
      error: 'already_done',
      message: 'nope',
    });
  });

  it('maps a 5xx AppError and does not leak the cause', async () => {
    const res = await appThrowing(
      new UpstreamError('db down', 'io_failed', new Error('secret')),
    ).request('/boom');
    expect(res.status).toBe(502);
    const body = (await res.json()) as { error: string; message: string };
    expect(body.error).toBe('io_failed');
    expect(JSON.stringify(body)).not.toContain('secret');
  });

  it('maps RateLimitedError with a Retry-After header', async () => {
    const res = await appThrowing(new RateLimitedError(42)).request('/boom');
    expect(res.status).toBe(429);
    expect(res.headers.get('Retry-After')).toBe('42');
    expect((await res.json()) as { retry_after: number }).toMatchObject({ retry_after: 42 });
  });

  it('maps an unknown thrown value to a generic 500', async () => {
    const res = await appThrowing(new Error('raw failure')).request('/boom');
    expect(res.status).toBe(500);
    const body = (await res.json()) as { error: string; message: string };
    expect(body.error).toBe('internal_error');
    expect(body.message).not.toContain('raw failure');
  });
});
