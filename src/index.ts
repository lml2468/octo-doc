/**
 * Server entrypoint. Boots the app, starts the HTTP listener, and wires
 * graceful shutdown. All configuration comes from the environment.
 */
import { serve } from '@hono/node-server';
import { createApp } from './app.js';
import { initLogger } from './logger.js';

const { app, config, stores } = await createApp(process.env);
const log = initLogger(config.logLevel);

const server = serve({ fetch: app.fetch, port: config.port, hostname: config.host }, (info) => {
  log.info(
    {
      addr: `http://${config.host}:${info.port}`,
      storage: stores.spec,
      private: config.private,
      auth: config.githubClientId ? 'github-device-flow' : 'anonymous',
      writeToken: config.writeToken ? 'static' : config.allowBootstrap ? 'bootstrap' : 'none',
    },
    'octo-doc listening',
  );
});

function shutdown(signal: string): void {
  log.info({ signal }, 'shutting down');
  server.close(() => {
    void stores.metaStore.close().then(() => process.exit(0));
  });
  setTimeout(() => process.exit(1), 5000).unref();
}

process.on('SIGTERM', () => shutdown('SIGTERM'));
process.on('SIGINT', () => shutdown('SIGINT'));
