/**
 * Schema migration runner. SQLite applies its schema at open (so this confirms
 * it); Postgres runs the SQL files in order against `DATABASE_URL` (idempotent —
 * all DDL uses `IF NOT EXISTS`).
 */
import { readFileSync, readdirSync, existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { loadConfig } from '../config.js';
import { logger } from '../logger.js';

const here = dirname(fileURLToPath(import.meta.url));
// In dev `here` is src/storage (→ ../../migrations); in the built image the
// compiled file sits in dist/ alongside a sibling migrations/ at the app root.
// Try both so the runner works in either layout.
const migrationsDir =
  [join(here, '..', '..', 'migrations'), join(process.cwd(), 'migrations')].find((d) =>
    existsSync(d),
  ) ?? join(process.cwd(), 'migrations');
const log = logger();

async function run(): Promise<void> {
  const config = loadConfig(process.env);
  const [metaKind] = config.storage.split('+');
  const files = readdirSync(migrationsDir)
    .filter((f) => /^\d+.*\.sql$/.test(f))
    .sort();

  if (metaKind === 'postgres') {
    const url = process.env.DATABASE_URL ?? process.env.PG_URL;
    if (!url) throw new Error('STORAGE=postgres requires DATABASE_URL');
    const pg = await import('pg');
    const pool = new pg.default.Pool({ connectionString: url }) as {
      query(text: string): Promise<unknown>;
      end(): Promise<void>;
    };
    for (const f of files) {
      const sql = readFileSync(join(migrationsDir, f), 'utf8').replace(
        /\bjson\s+TEXT\b/gi,
        'json JSONB',
      );
      log.info({ file: f }, 'applying migration');
      await pool.query(sql);
    }
    await pool.end();
    log.info({ count: files.length }, 'postgres migrated');
  } else {
    const { makeSqliteMetadataStore } = await import('./sqlite.js');
    const store = makeSqliteMetadataStore(config);
    await store.anyToken();
    await store.close();
    log.info({ count: files.length }, 'sqlite schema ensured');
  }
}

void run();
