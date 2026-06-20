#!/usr/bin/env node
// octo-doc CLI — `npx octo-doc` / `octo-doc start`.
//
// Lightweight self-host fallback: starts the server with SQLite + ./data and
// zero external services. Just `octo-doc` (or `octo-doc start`) is enough.
//   octo-doc start            run the server (default)
//   octo-doc migrate          ensure/apply the DB schema
//   octo-doc bootstrap        print a write token (after the server is up)
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const root = join(__dirname, '..');
const cmd = process.argv[2] || 'start';

function run(file) {
  const child = spawn(process.execPath, [join(root, file)], { stdio: 'inherit', env: process.env });
  child.on('exit', (code) => process.exit(code ?? 0));
}

switch (cmd) {
  case 'start':
    run('src/index.js');
    break;
  case 'migrate':
    run('migrations/migrate.js');
    break;
  case 'bootstrap': {
    const base = (process.env.BASE_URL || `http://127.0.0.1:${process.env.PORT || 8080}`).replace(/\/$/, '');
    const r = await fetch(`${base}/api/admin/bootstrap`);
    const body = await r.json();
    if (body.token) { console.log(body.token); }
    else { console.error(JSON.stringify(body)); process.exit(1); }
    break;
  }
  default:
    console.error(`octo-doc: unknown command "${cmd}"\nusage: octo-doc [start|migrate|bootstrap]`);
    process.exit(1);
}
