// src/storage/sqlite.ts
import { createRequire } from "module";
import { mkdirSync } from "fs";
import { dirname } from "path";
var { DatabaseSync } = createRequire(import.meta.url)("node:sqlite");
var SCHEMA = `
CREATE TABLE IF NOT EXISTS meta (slug TEXT PRIMARY KEY, json TEXT NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS comments (slug TEXT PRIMARY KEY, json TEXT NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS sessions (sid TEXT PRIMARY KEY, json TEXT NOT NULL, expires_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS tokens (token TEXT PRIMARY KEY, json TEXT NOT NULL, created_at INTEGER NOT NULL);
`;
var now = () => Date.now();
function parse(raw, fallback) {
  if (raw == null) return fallback;
  try {
    return JSON.parse(raw);
  } catch {
    return fallback;
  }
}
function makeSqliteMetadataStore(config) {
  const file = config.sqlitePath ?? `${config.dataDir}/octo-doc.db`;
  if (file !== ":memory:") mkdirSync(dirname(file), { recursive: true });
  const db = new DatabaseSync(file);
  db.exec("PRAGMA journal_mode = WAL");
  db.exec("PRAGMA busy_timeout = 5000");
  db.exec(SCHEMA);
  const q = {
    getMeta: db.prepare("SELECT json FROM meta WHERE slug = ?"),
    putMeta: db.prepare(
      "INSERT INTO meta(slug,json,updated_at) VALUES(?,?,?) ON CONFLICT(slug) DO UPDATE SET json=excluded.json, updated_at=excluded.updated_at"
    ),
    delMeta: db.prepare("DELETE FROM meta WHERE slug = ?"),
    listMeta: db.prepare("SELECT slug, json FROM meta ORDER BY slug"),
    getComments: db.prepare("SELECT json FROM comments WHERE slug = ?"),
    putComments: db.prepare(
      "INSERT INTO comments(slug,json,updated_at) VALUES(?,?,?) ON CONFLICT(slug) DO UPDATE SET json=excluded.json, updated_at=excluded.updated_at"
    ),
    delComments: db.prepare("DELETE FROM comments WHERE slug = ?"),
    getSession: db.prepare("SELECT json, expires_at FROM sessions WHERE sid = ?"),
    putSession: db.prepare(
      "INSERT INTO sessions(sid,json,expires_at) VALUES(?,?,?) ON CONFLICT(sid) DO UPDATE SET json=excluded.json, expires_at=excluded.expires_at"
    ),
    delSession: db.prepare("DELETE FROM sessions WHERE sid = ?"),
    gcSessions: db.prepare("DELETE FROM sessions WHERE expires_at < ?"),
    getToken: db.prepare("SELECT json FROM tokens WHERE token = ?"),
    putToken: db.prepare(
      "INSERT INTO tokens(token,json,created_at) VALUES(?,?,?) ON CONFLICT(token) DO NOTHING"
    ),
    countTokens: db.prepare("SELECT COUNT(*) AS n FROM tokens")
  };
  return {
    getMeta: (slug) => Promise.resolve(
      parse(q.getMeta.get(slug)?.json, null)
    ),
    putMeta: (slug, meta) => {
      q.putMeta.run(slug, JSON.stringify(meta), now());
      return Promise.resolve();
    },
    deleteMeta: (slug) => {
      q.delMeta.run(slug);
      return Promise.resolve();
    },
    listMeta: () => Promise.resolve(
      q.listMeta.all().map((r) => ({
        slug: r.slug,
        meta: parse(r.json, { slug: r.slug, title: r.slug, versions: [] })
      }))
    ),
    getComments: (slug) => {
      const v = parse(q.getComments.get(slug)?.json, []);
      return Promise.resolve(Array.isArray(v) ? v : []);
    },
    putComments: (slug, list) => {
      q.putComments.run(slug, JSON.stringify(list), now());
      return Promise.resolve();
    },
    deleteComments: (slug) => {
      q.delComments.run(slug);
      return Promise.resolve();
    },
    getSession: (sid) => {
      const row = q.getSession.get(sid);
      if (!row) return Promise.resolve(null);
      if (row.expires_at < now()) {
        q.delSession.run(sid);
        return Promise.resolve(null);
      }
      return Promise.resolve(parse(row.json, null));
    },
    putSession: (sid, data, ttlSeconds) => {
      q.putSession.run(sid, JSON.stringify(data), now() + ttlSeconds * 1e3);
      q.gcSessions.run(now());
      return Promise.resolve();
    },
    deleteSession: (sid) => {
      q.delSession.run(sid);
      return Promise.resolve();
    },
    getToken: (token) => Promise.resolve(
      parse(q.getToken.get(token)?.json, null)
    ),
    putToken: (token, record) => {
      q.putToken.run(token, JSON.stringify(record), now());
      return Promise.resolve();
    },
    anyToken: () => Promise.resolve(q.countTokens.get().n > 0),
    close: () => {
      db.close();
      return Promise.resolve();
    }
  };
}

export {
  makeSqliteMetadataStore
};
//# sourceMappingURL=chunk-MC5VWS5B.js.map