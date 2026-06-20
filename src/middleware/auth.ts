/**
 * Auth middleware: write-token enforcement and optional private-read gating.
 * Both delegate credential checks to {@link AuthService}; routes stay declarative.
 */
import { createMiddleware } from 'hono/factory';
import { NotFoundError, UnauthorizedError } from '../errors.js';
import type { AppEnv } from '../http-context.js';

/** Extract a Bearer token from the Authorization header, or null. */
export function bearer(header: string | undefined): string | null {
  const m = /^Bearer\s+(.+)$/.exec(header ?? '');
  return m ? m[1]! : null;
}

/** Require a valid write token; sets `c.var.writeToken` on success. */
export const requireWriteAuth = createMiddleware<AppEnv>(async (c, next) => {
  const token = bearer(c.req.header('authorization'));
  if (!token || !(await c.var.auth.isValidWriteToken(token))) {
    throw new UnauthorizedError();
  }
  c.set('writeToken', token);
  await next();
});

/**
 * When `PRIVATE=1`, gate public GET routes behind the write token too. A failed
 * check returns 404 (not 401) so a private server never confirms a doc exists.
 */
export const maybeRequireReadAuth = createMiddleware<AppEnv>(async (c, next) => {
  if (!c.var.config.private) return next();
  const token = bearer(c.req.header('authorization'));
  if (token && (await c.var.auth.isValidWriteToken(token))) return next();
  throw new NotFoundError('Not found');
});
