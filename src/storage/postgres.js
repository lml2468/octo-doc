// PostgreSQL MetadataStore (node-postgres). Same interface as the SQLite
// adapter — no pg type escapes this module. Selected with STORAGE=postgres+...
//
// Concurrency note: the in-process per-slug mutex (core/mutex.js) serializes
// comment writes within ONE app instance. For multi-instance deployments,
// flip on advisory locks (withLock below wraps the read-modify-write in a
// pg_advisory_xact_lock keyed by the slug hash) — documented in DESIGN.md.
import { createHash } from 'node:crypto';

const SCHEMA = `
CREATE TABLE IF NOT EXISTS meta (
  slug TEXT PRIMARY KEY,
  json JSONB NOT NULL,
  updated_at BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS comments (
  slug TEXT PRIMARY KEY,
  json JSONB NOT NULL,
  updated_at BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  sid TEXT PRIMARY KEY,
  json JSONB NOT NULL,
  expires_at BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS tokens (
  token TEXT PRIMARY KEY,
  json JSONB NOT NULL,
  created_at BIGINT NOT NULL
);
`;

export async function makePostgresMetadataStore(env) {
  const { default: pg } = await import('pg');
  const pool = new pg.Pool({
    connectionString: env.DATABASE_URL || env.PG_URL,
    max: Number(env.PG_POOL_MAX || 10),
  });
  await pool.query(SCHEMA);
  const now = () => Date.now();

  return {
    async getMeta(slug) {
      const r = await pool.query('SELECT json FROM meta WHERE slug=$1', [slug]);
      return r.rows[0]?.json ?? null;
    },
    async putMeta(slug, meta) {
      await pool.query(
        'INSERT INTO meta(slug,json,updated_at) VALUES($1,$2,$3) ON CONFLICT(slug) DO UPDATE SET json=$2, updated_at=$3',
        [slug, JSON.stringify(meta), now()]);
    },
    async deleteMeta(slug) { await pool.query('DELETE FROM meta WHERE slug=$1', [slug]); },
    async listMeta() {
      const r = await pool.query('SELECT slug, json FROM meta ORDER BY slug');
      return r.rows.map(row => ({ slug: row.slug, meta: row.json || {} }));
    },

    async getComments(slug) {
      const r = await pool.query('SELECT json FROM comments WHERE slug=$1', [slug]);
      const v = r.rows[0]?.json;
      return Array.isArray(v) ? v : [];
    },
    async putComments(slug, list) {
      await pool.query(
        'INSERT INTO comments(slug,json,updated_at) VALUES($1,$2,$3) ON CONFLICT(slug) DO UPDATE SET json=$2, updated_at=$3',
        [slug, JSON.stringify(list), now()]);
    },
    async deleteComments(slug) { await pool.query('DELETE FROM comments WHERE slug=$1', [slug]); },

    async getSession(sid) {
      const r = await pool.query('SELECT json, expires_at FROM sessions WHERE sid=$1', [sid]);
      const row = r.rows[0];
      if (!row) return null;
      if (Number(row.expires_at) < now()) { await pool.query('DELETE FROM sessions WHERE sid=$1', [sid]); return null; }
      return row.json;
    },
    async putSession(sid, data, ttlSeconds) {
      await pool.query(
        'INSERT INTO sessions(sid,json,expires_at) VALUES($1,$2,$3) ON CONFLICT(sid) DO UPDATE SET json=$2, expires_at=$3',
        [sid, JSON.stringify(data), now() + ttlSeconds * 1000]);
      await pool.query('DELETE FROM sessions WHERE expires_at < $1', [now()]);
    },
    async deleteSession(sid) { await pool.query('DELETE FROM sessions WHERE sid=$1', [sid]); },

    async getToken(token) {
      const r = await pool.query('SELECT json FROM tokens WHERE token=$1', [token]);
      return r.rows[0]?.json ?? null;
    },
    async putToken(token, record) {
      await pool.query(
        'INSERT INTO tokens(token,json,created_at) VALUES($1,$2,$3) ON CONFLICT(token) DO NOTHING',
        [token, JSON.stringify(record), now()]);
    },
    async anyToken() {
      const r = await pool.query('SELECT COUNT(*)::int AS n FROM tokens');
      return r.rows[0].n > 0;
    },

    // Optional cross-instance lock (opt-in via the mutex layer in future).
    advisoryKey(slug) {
      const h = createHash('sha256').update(slug).digest();
      return h.readInt32BE(0); // 32-bit key for pg_advisory_xact_lock
    },

    async close() { await pool.end(); },
  };
}
