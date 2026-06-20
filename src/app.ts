/**
 * Application assembly. Wires config → stores → services → routes into a Hono
 * app. Exported separately from the process entrypoint so tests mount it via
 * `app.fetch` with no socket.
 */
import { Hono } from 'hono';
import { cors } from 'hono/cors';
import type { Config } from './config.js';
import { loadConfig } from './config.js';
import type { AppEnv } from './http-context.js';
import { makeStores, type Stores } from './storage/index.js';
import { AuthService, CommentService, DocService } from './services/index.js';
import { errorHandler } from './middleware/error.js';
import { rateLimit, rateLimitWrites } from './middleware/rate-limit.js';
import { docSecurityHeaders } from './middleware/security.js';
import { docRoutes } from './routes/docs.js';
import { commentRoutes } from './routes/comments.js';
import { adminRoutes } from './routes/admin.js';
import { pageRoutes } from './routes/pages.js';

/** A constructed app plus the handles tests/entrypoints need. */
export interface BuiltApp {
  app: Hono<AppEnv>;
  config: Config;
  stores: Stores;
  close(): Promise<void>;
}

/** Build the application. Optionally inject pre-made stores (tests). */
export async function createApp(
  env: NodeJS.ProcessEnv = process.env,
  deps: { stores?: Stores } = {},
): Promise<BuiltApp> {
  const config = loadConfig(env);
  const stores = deps.stores ?? (await makeStores(config));
  const comments = new CommentService(stores.metaStore);
  const docs = new DocService(stores.blobStore, stores.metaStore, comments, {
    baseUrl: config.baseUrl,
    maxHtmlBytes: config.maxHtmlBytes,
  });
  const auth = new AuthService(stores.metaStore, config);

  const app = new Hono<AppEnv>();

  // Inject services + config onto every request context.
  app.use('*', async (c, next) => {
    c.set('config', config);
    c.set('docs', docs);
    c.set('comments', comments);
    c.set('auth', auth);
    await next();
  });

  // CORS for the API (overlay is same-origin; CLI/agents are not).
  app.use(
    '/api/*',
    cors({
      origin: '*',
      allowMethods: ['GET', 'POST', 'PATCH', 'DELETE', 'OPTIONS'],
      allowHeaders: ['Content-Type', 'Authorization'],
    }),
  );

  // Security headers on rendered docs.
  const secHeaders = docSecurityHeaders(config.frameAncestors);
  app.use('/d/*', async (c, next) => {
    await next();
    for (const [k, v] of Object.entries(secHeaders)) c.header(k, v);
  });

  // Rate-limit mutating routes (reads on /api/comments pass through).
  const limiter = rateLimit({ windowMs: config.rateLimitWindowMs, max: config.rateLimitMax });
  app.use('/api/docs', limiter);
  app.use('/api/upload', limiter);
  app.use(
    '/api/comments',
    rateLimitWrites({ windowMs: config.rateLimitWindowMs, max: config.rateLimitMax }),
  );
  app.use('/api/reactions', limiter);
  app.use('/api/agent/*', limiter);

  app.route('/', adminRoutes());
  app.route('/', docRoutes());
  app.route('/', commentRoutes());
  app.route('/', pageRoutes());

  app.notFound((c) => c.text('Not found', 404));
  app.onError(errorHandler);

  return {
    app,
    config,
    stores,
    close: () => stores.metaStore.close(),
  };
}
