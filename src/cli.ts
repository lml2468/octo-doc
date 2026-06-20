#!/usr/bin/env node
/**
 * `octo-doc` CLI. Lightweight self-host entrypoint.
 *
 *   octo-doc [start]    run the server (SQLite + ./data by default)
 *   octo-doc migrate    ensure/apply the database schema
 *   octo-doc bootstrap  print a write token (server must be running)
 */
import { loadConfig } from './config.js';

const cmd = process.argv[2] ?? 'start';

async function main(): Promise<void> {
  switch (cmd) {
    case 'start':
      await import('./index.js');
      break;
    case 'migrate':
      await import('./storage/migrate.js');
      break;
    case 'bootstrap': {
      const config = loadConfig(process.env);
      const base = (config.baseUrl || `http://127.0.0.1:${config.port}`).replace(/\/$/, '');
      const res = await fetch(`${base}/api/admin/bootstrap`);
      const body = (await res.json()) as { token?: string };
      if (body.token) {
        process.stdout.write(body.token + '\n');
      } else {
        process.stderr.write(JSON.stringify(body) + '\n');
        process.exit(1);
      }
      break;
    }
    default:
      process.stderr.write(
        `octo-doc: unknown command "${cmd}"\nusage: octo-doc [start|migrate|bootstrap]\n`,
      );
      process.exit(1);
  }
}

void main();
