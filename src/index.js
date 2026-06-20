// Server entry point. 12-factor: all config via env. pino structured logs.
import { serve } from '@hono/node-server';
import { pino } from 'pino';
import { createApp } from './app.js';

const log = pino({ level: process.env.LOG_LEVEL || 'info' });

const { app, config, spec } = await createApp(process.env);

const server = serve({ fetch: app.fetch, port: config.port, hostname: config.host }, (info) => {
  log.info({
    addr: `http://${config.host}:${info.port}`,
    storage: spec,
    private: config.private,
    auth: config.githubClientId ? 'github-device-flow' : 'anonymous',
    writeToken: config.writeToken ? 'static' : (config.allowBootstrap ? 'bootstrap' : 'none'),
  }, 'octo-doc listening');
});

const shutdown = (sig) => {
  log.info({ sig }, 'shutting down');
  server.close(() => process.exit(0));
  setTimeout(() => process.exit(1), 5000).unref();
};
process.on('SIGTERM', () => shutdown('SIGTERM'));
process.on('SIGINT', () => shutdown('SIGINT'));
