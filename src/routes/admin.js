// Auth (GitHub Device Flow), admin bootstrap, and health-check routes.
import { Hono } from 'hono';
import {
  getSession, isOwnerSession, ghPost, ghUser, rand,
  setSessionCookie, clearSessionCookie,
} from './auth.js';

export function authRoutes(ctx) {
  const { config, metaStore } = ctx;
  const app = new Hono();

  // Health check — the identity marker downstream tooling greps for.
  app.get('/api/ping', (c) => c.json({ ok: true, service: 'tdoc' }));
  // Liveness/readiness for orchestrators (compose healthcheck, k8s).
  app.get('/healthz', (c) => c.json({ ok: true }));

  // ── Admin bootstrap: GET /api/admin/bootstrap ───────────────────────────────
  // Issues the FIRST write token when none has been provisioned and no static
  // WRITE_TOKEN is configured. Idempotent-ish: once any token exists (or
  // WRITE_TOKEN is set), this returns 409 so it can't be used to mint unlimited
  // tokens. Gated by ALLOW_BOOTSTRAP. This is what the short_test curls.
  app.get('/api/admin/bootstrap', async (c) => {
    if (!config.allowBootstrap) return c.json({ error: 'bootstrap_disabled' }, 403);
    if (config.writeToken) return c.json({ error: 'static_token_configured' }, 409);
    if (await metaStore.anyToken()) return c.json({ error: 'already_bootstrapped' }, 409);
    const token = rand(32);
    await metaStore.putToken(token, { token, created: new Date().toISOString(), label: 'bootstrap' });
    return c.json({ ok: true, token });
  });

  // ── Identity ────────────────────────────────────────────────────────────────
  app.get('/api/auth/me', async (c) => {
    const s = await getSession(c, metaStore);
    return c.json({
      identity: s ? { login: s.login, avatar_url: s.avatar_url, name: s.name } : null,
      isOwner: isOwnerSession(config, s),
      authConfigured: !!config.githubClientId,
    });
  });

  // ── GitHub Device Flow ────────────────────────────────────────────────────────
  app.post('/api/auth/device/start', async (c) => {
    if (!config.githubClientId) return c.json({ error: 'auth_not_configured' }, 400);
    try {
      const r = await ghPost('/login/device/code', { client_id: config.githubClientId, scope: 'read:user' });
      if (r.error) return c.json({ error: r.error, message: r.error_description }, 400);
      return c.json({
        device_code: r.device_code, user_code: r.user_code,
        verification_uri: r.verification_uri, expires_in: r.expires_in, interval: r.interval,
      });
    } catch (e) {
      return c.json({ error: 'github_unreachable', message: e.message }, 500);
    }
  });

  app.post('/api/auth/device/poll', async (c) => {
    if (!config.githubClientId) return c.json({ error: 'auth_not_configured' }, 400);
    let body = {};
    try { body = await c.req.json(); } catch {}
    if (!body.device_code) return c.json({ error: 'device_code required' }, 400);
    try {
      const r = await ghPost('/login/oauth/access_token', {
        client_id: config.githubClientId,
        device_code: body.device_code,
        grant_type: 'urn:ietf:params:oauth:grant-type:device_code',
      });
      if (r.error === 'authorization_pending' || r.error === 'slow_down') {
        return c.json({ pending: true, error: r.error, interval: Number(r.interval) || null });
      }
      if (r.error) return c.json({ error: r.error, message: r.error_description || r.error }, 400);
      if (!r.access_token) return c.json({ pending: true });
      const user = await ghUser(r.access_token);
      if (!user.login) return c.json({ error: 'no_user', message: user.message || 'no login' }, 500);
      const sid = rand(24);
      const session = {
        login: user.login, avatar_url: user.avatar_url,
        name: user.name || user.login, created: new Date().toISOString(),
      };
      const TTL = 60 * 60 * 24 * 30;
      await metaStore.putSession(sid, session, TTL);
      setSessionCookie(c, config, sid, TTL);
      return c.json({ ok: true, identity: { login: user.login, avatar_url: user.avatar_url, name: user.name || user.login } });
    } catch (e) {
      return c.json({ error: 'github_unreachable', message: e.message }, 500);
    }
  });

  app.post('/api/auth/logout', async (c) => {
    const s = await getSession(c, metaStore);
    if (s) await metaStore.deleteSession(s.id);
    clearSessionCookie(c, config);
    return c.json({ ok: true });
  });

  return app;
}
