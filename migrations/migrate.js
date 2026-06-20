// Migration runner. For SQLite the adapter applies the schema at open, so this
// is mostly a no-op confirmation. For Postgres it runs every migrations/*.sql
// in order against DATABASE_URL (idempotent — all statements use IF NOT EXISTS).
import { readdirSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { loadConfig } from '../src/config.js';

const __dirname = dirname(fileURLToPath(import.meta.url));

const config = loadConfig(process.env);
const [, blobKind] = config.storage.toLowerCase().split('+');
const metaKind = config.storage.toLowerCase().split('+')[0];

const files = readdirSync(__dirname).filter(f => /^\d+.*\.sql$/.test(f)).sort();

if (metaKind === 'postgres' || metaKind === 'pg') {
  const { default: pg } = await import('pg');
  const pool = new pg.Pool({ connectionString: process.env.DATABASE_URL || process.env.PG_URL });
  for (const f of files) {
    // Postgres wants JSONB; the .sql declares TEXT for SQLite compatibility.
    const sql = readFileSync(join(__dirname, f), 'utf8').replace(/\bjson\s+TEXT\b/gi, 'json JSONB');
    process.stdout.write(`applying ${f}... `);
    await pool.query(sql);
    console.log('ok');
  }
  await pool.end();
  console.log(`migrated postgres (${files.length} file(s))`);
} else {
  // SQLite: the adapter creates tables at open. Touch it to confirm.
  const { makeSqliteMetadataStore } = await import('../src/storage/sqlite.js');
  const store = makeSqliteMetadataStore(config);
  await store.anyToken();
  await store.close();
  console.log(`sqlite schema ensured at ${config.sqlitePath || (config.dataDir + '/octo-doc.db')} (${files.length} migration file(s) tracked)`);
}
void blobKind;
