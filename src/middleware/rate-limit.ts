/**
 * Fixed-window rate limiter for write endpoints, keyed by token + client IP.
 * In-memory (correct for a single instance). Throws {@link RateLimitedError},
 * which the error middleware maps to 429 + Retry-After.
 */
import { createMiddleware } from 'hono/factory';
import { RateLimitedError } from '../errors.js';
import { bearer } from './auth.js';
import type { AppEnv } from '../http-context.js';

interface Window {
  count: number;
  resetAt: number;
}

/** The client IP, honoring a reverse proxy's forwarding headers. */
export function clientIp(headers: Headers): string {
  const xff = headers.get('x-forwarded-for');
  if (xff) return xff.split(',')[0]!.trim();
  return headers.get('x-real-ip') ?? 'unknown';
}

/**
 * Build a rate-limit middleware. `max <= 0` disables limiting (returns a
 * pass-through). The map is GC'd opportunistically so it can't grow unbounded.
 */
export function rateLimit(opts: { windowMs: number; max: number }) {
  const hits = new Map<string, Window>();
  return createMiddleware<AppEnv>(async (c, next) => {
    if (opts.max <= 0) return next();
    const token = (bearer(c.req.header('authorization')) ?? '').slice(0, 16);
    const key = `${token}|${clientIp(c.req.raw.headers)}`;
    const now = Date.now();
    let w = hits.get(key);
    if (!w || w.resetAt < now) {
      w = { count: 0, resetAt: now + opts.windowMs };
      hits.set(key, w);
    }
    w.count++;
    if (w.count > opts.max) {
      throw new RateLimitedError(Math.ceil((w.resetAt - now) / 1000));
    }
    if (hits.size > 10_000) {
      for (const [k, v] of hits) if (v.resetAt < now) hits.delete(k);
    }
    await next();
  });
}

/** Like {@link rateLimit} but skips GET requests (used for mixed read/write paths). */
export function rateLimitWrites(opts: { windowMs: number; max: number }) {
  const limiter = rateLimit(opts);
  return createMiddleware<AppEnv>((c, next) =>
    c.req.method === 'GET' ? next() : limiter(c, next),
  );
}
