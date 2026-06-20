/**
 * Health, admin bootstrap, identity, and GitHub auth routes. Thin handlers over
 * {@link AuthService}.
 */
import { Hono, type Context } from 'hono';
import { getCookie, setCookie } from 'hono/cookie';
import type { AppEnv } from '../http-context.js';
import { ValidationError } from '../errors.js';

/** Set the session cookie consistently across login. */
function setSessionCookie(c: Context<AppEnv>, sid: string, maxAge: number): void {
  setCookie(c, 'tdoc_sid', sid, {
    path: '/',
    httpOnly: true,
    secure: c.var.config.cookieSecure,
    sameSite: 'Lax',
    maxAge,
  });
}

/** Build health/admin/auth routes. */
export function adminRoutes(): Hono<AppEnv> {
  const app = new Hono<AppEnv>();

  app.get('/api/ping', (c) => c.json({ ok: true, service: 'tdoc' }));
  app.get('/healthz', (c) => c.json({ ok: true }));

  app.get('/api/admin/bootstrap', async (c) => {
    const result = await c.var.auth.bootstrap();
    return c.json({ ok: true, ...result });
  });

  app.get('/api/auth/me', async (c) => {
    const session = await c.var.auth.getSession(getCookie(c, 'tdoc_sid'));
    return c.json({
      identity: session
        ? { login: session.login, avatar_url: session.avatar_url ?? null, name: session.name }
        : null,
      isOwner: c.var.auth.isOwner(session),
      authConfigured: !!c.var.config.githubClientId,
    });
  });

  app.post('/api/auth/device/start', async (c) => c.json(await c.var.auth.startDeviceFlow()));

  app.post('/api/auth/device/poll', async (c) => {
    const body = (await c.req.json().catch(() => ({}))) as { device_code?: unknown };
    if (typeof body.device_code !== 'string')
      throw new ValidationError('device_code required', 'device_code_required');
    const result = await c.var.auth.pollDeviceFlow(body.device_code);
    if (result.pending) return c.json({ pending: true });
    setSessionCookie(c, result.sid, c.var.auth.sessionTtlSeconds);
    return c.json({ ok: true, identity: result.identity });
  });

  app.post('/api/auth/logout', async (c) => {
    await c.var.auth.logout(getCookie(c, 'tdoc_sid'));
    setCookie(c, 'tdoc_sid', '', { path: '/', maxAge: 0, secure: c.var.config.cookieSecure });
    return c.json({ ok: true });
  });

  return app;
}
