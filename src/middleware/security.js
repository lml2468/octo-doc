// Auth, rate limiting, and CSP middleware.
import { createMiddleware } from 'hono/factory';

// ── Write authentication ───────────────────────────────────────────────────
// Bearer token in the Authorization header (NEVER in the URL — failure mode
// "Token 放 URL 而非 Authorization header"). Accepts the static WRITE_TOKEN if
// configured, OR any token provisioned via /api/admin/bootstrap.
export function requireWriteAuth(config) {
  return createMiddleware(async (c, next) => {
    const auth = c.req.header('authorization') || '';
    const m = auth.match(/^Bearer\s+(.+)$/);
    if (!m) return c.json({ error: 'unauthorized' }, 401);
    const token = m[1];
    const ok = await isValidWriteToken(c, config, token);
    if (!ok) return c.json({ error: 'unauthorized' }, 401);
    c.set('writeToken', token);
    await next();
  });
}

export async function isValidWriteToken(c, config, token) {
  if (!token) return false;
  if (config.writeToken && timingSafeEqual(token, config.writeToken)) return true;
  // Bootstrap-provisioned tokens live in the metadata store.
  const store = c.get('metaStore');
  if (store) {
    const rec = await store.getToken(token);
    if (rec) return true;
  }
  return false;
}

// Constant-time string compare to avoid leaking the token via timing.
function timingSafeEqual(a, b) {
  if (typeof a !== 'string' || typeof b !== 'string') return false;
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}

// ── Read privacy ────────────────────────────────────────────────────────────
// When PRIVATE=1, public GET routes also require a valid Bearer token. Default
// is public reads (link-only sharing, like the upstream Worker).
export function maybeRequireReadAuth(config) {
  return createMiddleware(async (c, next) => {
    if (!config.private) return next();
    const auth = c.req.header('authorization') || '';
    const m = auth.match(/^Bearer\s+(.+)$/);
    if (m && await isValidWriteToken(c, config, m[1])) return next();
    return c.text('Not found', 404); // 404 not 401: don't confirm the doc exists
  });
}

// ── Rate limiting (writes) ───────────────────────────────────────────────────
// Fixed-window counter keyed by token + client IP (failure mode: missing write
// rate limit). In-memory — correct for a single instance; a shared limiter
// would use the metadata store. 0 max disables.
export function makeRateLimiter(config) {
  const hits = new Map(); // key -> { count, resetAt }
  const { rateLimitWindowMs: windowMs, rateLimitMax: max } = config;
  return createMiddleware(async (c, next) => {
    if (!max) return next();
    const token = (c.req.header('authorization') || '').replace(/^Bearer\s+/, '').slice(0, 16);
    const ip = clientIp(c);
    const key = `${token}|${ip}`;
    const now = Date.now();
    let e = hits.get(key);
    if (!e || e.resetAt < now) { e = { count: 0, resetAt: now + windowMs }; hits.set(key, e); }
    e.count++;
    if (e.count > max) {
      const retry = Math.ceil((e.resetAt - now) / 1000);
      return c.json({ error: 'rate_limited', retry_after: retry }, 429, { 'Retry-After': String(retry) });
    }
    // Opportunistic GC so the map can't grow without bound.
    if (hits.size > 10000) for (const [k, v] of hits) if (v.resetAt < now) hits.delete(k);
    await next();
  });
}

export function clientIp(c) {
  // Behind Caddy/nginx/Traefik the real IP is in X-Forwarded-For.
  const xff = c.req.header('x-forwarded-for');
  if (xff) return xff.split(',')[0].trim();
  return c.req.header('x-real-ip') || c.env?.remoteAddr || 'unknown';
}

// ── Content-Security-Policy for rendered docs ────────────────────────────────
// User HTML is served as a static blob; we never eval it. The CSP here is for
// the WRAPPER we control. The doc's own inline <script>/<style> need
// 'unsafe-inline'/'unsafe-eval' (single-page interactive HTML is the whole
// point), but we still pin:
//   - frame-ancestors: who may iframe this doc (clickjacking / parent-panel XSS,
//     failure mode "缺 CSP/X-Frame-Options → 父面板 XSS").
//   - base-uri 'self': stops a <base> injection from hijacking relative URLs.
// The anti-top-level-redirect protection ('sandbox' would break interactivity,
// so we rely on frame-ancestors + a separate doc subdomain — see DESIGN.md).
export function docSecurityHeaders(config) {
  const ancestors = config.frameAncestors || "'none'";
  return {
    'Content-Security-Policy':
      `default-src 'self' data: blob: https:; ` +
      `script-src 'self' 'unsafe-inline' 'unsafe-eval' data: blob: https:; ` +
      `style-src 'self' 'unsafe-inline' https:; ` +
      `img-src 'self' data: blob: https:; ` +
      `font-src 'self' data: https:; ` +
      `connect-src 'self' https:; ` +
      `base-uri 'self'; ` +
      `frame-ancestors ${ancestors}`,
    'X-Frame-Options': ancestors === "'none'" ? 'DENY' : 'SAMEORIGIN',
    'X-Content-Type-Options': 'nosniff',
    'Referrer-Policy': 'no-referrer',
  };
}
