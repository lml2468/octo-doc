// SQLite MetadataStore using Node's built-in `node:sqlite` (Node 22+). The
// reference metadata adapter — zero native build step, so `npx octo-doc` and
// the Docker image stay dependency-light (see DESIGN.md §"Why node:sqlite").
//
// Tables (also in migrations/0001_init.sql for the Postgres-style migrate flow;
// applied here at open so a fresh ./data is usable with zero setup):
//   meta(slug PK, json, updated_at)
//   comments(slug PK, json, updated_at)
//   sessions(sid PK, json, expires_at)
//   tokens(token PK, json, created_at)
//
// No SQLite type escapes this module — every method returns plain JS values.
import { DatabaseSync } from 'node:sqlite';
import { mkdirSync } from 'node:fs';
import { dirname } from 'node:path';

const SCHEMA = `
CREATE TABLE IF NOT EXISTS meta (
  slug TEXT PRIMARY KEY,
  json TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS comments (
  slug TEXT PRIMARY KEY,
  json TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  sid TEXT PRIMARY KEY,
  json TEXT NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS tokens (
  token TEXT PRIMARY KEY,
  json TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
`;

export function makeSqliteMetadataStore(env) {
  const file = env.sqlitePath || env.SQLITE_PATH || `${env.dataDir || env.DATA_DIR || './data'}/octo-doc.db`;
  if (file !== ':memory:') mkdirSync(dirname(file), { recursive: true });
  const db = new DatabaseSync(file);
  db.exec('PRAGMA journal_mode = WAL');   // concurrent readers + single writer
  db.exec('PRAGMA busy_timeout = 5000');
  db.exec(SCHEMA);

  const now = () => Date.now();
  const parse = (raw, fallback) => {
    if (raw == null) return fallback;
    try { return JSON.parse(raw); } catch { return fallback; }
  };

  const stmt = {
    getMeta: db.prepare('SELECT json FROM meta WHERE slug = ?'),
    putMeta: db.prepare('INSERT INTO meta(slug,json,updated_at) VALUES(?,?,?) ON CONFLICT(slug) DO UPDATE SET json=excluded.json, updated_at=excluded.updated_at'),
    delMeta: db.prepare('DELETE FROM meta WHERE slug = ?'),
    listMeta: db.prepare('SELECT slug, json FROM meta ORDER BY slug'),
    getComments: db.prepare('SELECT json FROM comments WHERE slug = ?'),
    putComments: db.prepare('INSERT INTO comments(slug,json,updated_at) VALUES(?,?,?) ON CONFLICT(slug) DO UPDATE SET json=excluded.json, updated_at=excluded.updated_at'),
    delComments: db.prepare('DELETE FROM comments WHERE slug = ?'),
    getSession: db.prepare('SELECT json, expires_at FROM sessions WHERE sid = ?'),
    putSession: db.prepare('INSERT INTO sessions(sid,json,expires_at) VALUES(?,?,?) ON CONFLICT(sid) DO UPDATE SET json=excluded.json, expires_at=excluded.expires_at'),
    delSession: db.prepare('DELETE FROM sessions WHERE sid = ?'),
    gcSessions: db.prepare('DELETE FROM sessions WHERE expires_at < ?'),
    getToken: db.prepare('SELECT json FROM tokens WHERE token = ?'),
    putToken: db.prepare('INSERT INTO tokens(token,json,created_at) VALUES(?,?,?) ON CONFLICT(token) DO NOTHING'),
    countTokens: db.prepare('SELECT COUNT(*) AS n FROM tokens'),
  };

  return {
    async getMeta(slug) { return parse(stmt.getMeta.get(slug)?.json, null); },
    async putMeta(slug, meta) { stmt.putMeta.run(slug, JSON.stringify(meta), now()); },
    async deleteMeta(slug) { stmt.delMeta.run(slug); },
    async listMeta() {
      return stmt.listMeta.all().map(r => ({ slug: r.slug, meta: parse(r.json, {}) }));
    },

    async getComments(slug) {
      const v = parse(stmt.getComments.get(slug)?.json, []);
      return Array.isArray(v) ? v : [];
    },
    async putComments(slug, list) { stmt.putComments.run(slug, JSON.stringify(list), now()); },
    async deleteComments(slug) { stmt.delComments.run(slug); },

    async getSession(sid) {
      const row = stmt.getSession.get(sid);
      if (!row) return null;
      if (row.expires_at < now()) { stmt.delSession.run(sid); return null; }
      return parse(row.json, null);
    },
    async putSession(sid, data, ttlSeconds) {
      stmt.putSession.run(sid, JSON.stringify(data), now() + ttlSeconds * 1000);
      stmt.gcSessions.run(now());   // opportunistic GC of expired sessions
    },
    async deleteSession(sid) { stmt.delSession.run(sid); },

    async getToken(token) { return parse(stmt.getToken.get(token)?.json, null); },
    async putToken(token, record) { stmt.putToken.run(token, JSON.stringify(record), now()); },
    async anyToken() { return stmt.countTokens.get().n > 0; },

    async close() { db.close(); },
  };
}
