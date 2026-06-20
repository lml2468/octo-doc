// App factory — wires config + stores + routes into a single Hono app.
// Exported separately from the server entry so tests can mount it on an
// ephemeral port without a real network listener.
import { Hono } from 'hono';
import { logger as honoLogger } from 'hono/logger';
import { loadConfig } from './config.js';
import { makeStores } from './storage/index.js';
import { makeCommentStore } from './core/store.js';
import { makeRateLimiter, docSecurityHeaders } from './middleware/security.js';
import { docRoutes, pageRoutes } from './routes/docs.js';
import { commentRoutes } from './routes/comments.js';
import { authRoutes } from './routes/admin.js';

export async function createApp(env = process.env, deps = {}) {
  const config = loadConfig(env);
  config.repoUrl = env.REPO_URL || 'https://github.com/lml2468/octo-doc';

  // makeStores needs both the parsed config (config.storage, dataDir, …) and
  // raw env (DATABASE_URL, S3_* — adapter-specific knobs not in config). Merge
  // so an adapter can read either; config values win on key collisions.
  const storeEnv = { ...env, ...config };
  const { metaStore, blobStore, spec } = deps.stores || await makeStores(storeEnv);
  const commentStore = makeCommentStore(metaStore);
  const ctx = { config, metaStore, blobStore, commentStore };

  const app = new Hono();

  // Make stores reachable from middleware (token validation).
  app.use('*', async (c, next) => {
    c.set('metaStore', metaStore);
    c.set('config', config);
    await next();
  });

  // CORS for the API (the overlay is same-origin, but CLI/agents are not).
  app.use('/api/*', async (c, next) => {
    c.header('Access-Control-Allow-Origin', '*');
    c.header('Access-Control-Allow-Methods', 'GET,POST,PATCH,DELETE,OPTIONS');
    c.header('Access-Control-Allow-Headers', 'Content-Type,Authorization');
    if (c.req.method === 'OPTIONS') return c.body(null, 204);
    await next();
  });

  // Security headers on rendered docs (CSP / anti-clickjacking).
  app.use('/d/*', async (c, next) => {
    await next();
    const headers = docSecurityHeaders(config);
    for (const [k, v] of Object.entries(headers)) c.header(k, v);
  });

  // Write rate limiting on mutating API routes.
  const limiter = makeRateLimiter(config);
  app.use('/api/docs', limiter);
  app.use('/api/upload', limiter);
  app.use('/api/comments', async (c, next) => (c.req.method === 'GET' ? next() : limiter(c, next)));
  app.use('/api/reactions', limiter);
  app.use('/api/agent/*', limiter);

  if (config.logLevel !== 'silent' && deps.requestLog !== false) app.use('*', honoLogger());

  // Mount route groups.
  app.route('/', authRoutes(ctx));
  app.route('/', docRoutes(ctx));
  app.route('/', commentRoutes(ctx));
  app.route('/', pageRoutes(ctx));

  app.notFound((c) => c.text('Not found', 404));
  app.onError((err, c) => {
    return c.json({ error: 'internal_error', message: err?.message || String(err) }, 500);
  });

  return { app, config, ctx, spec, async close() { await metaStore.close?.(); } };
}
