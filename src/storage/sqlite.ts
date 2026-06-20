/**
 * SQLite {@link MetadataStore} using Node's built-in `node:sqlite` (Node 22+).
 * The reference metadata adapter — zero native build step. No SQLite row type
 * escapes this module; every method returns plain domain values.
 */
import { createRequire } from 'node:module';
import { mkdirSync } from 'node:fs';
import { dirname } from 'node:path';
import type { Comment } from '../core/comment.types.js';
import type { Config } from '../config.js';
import type { DocMeta, MetadataStore, Session, TokenRecord } from './types.js';

// Load via createRequire so bundlers (esbuild/tsup) don't rewrite the
// `node:sqlite` specifier to a bare `sqlite` import. `DatabaseSync` is the
// synchronous SQLite class added in Node 22.
interface SqliteModule {
  DatabaseSync: new (path: string) => {
    exec(sql: string): void;
    prepare(sql: string): {
      get(...p: unknown[]): unknown;
      all(...p: unknown[]): unknown[];
      run(...p: unknown[]): unknown;
    };
    close(): void;
  };
}
const { DatabaseSync } = createRequire(import.meta.url)('node:sqlite') as SqliteModule;

const SCHEMA = `
CREATE TABLE IF NOT EXISTS meta (slug TEXT PRIMARY KEY, json TEXT NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS comments (slug TEXT PRIMARY KEY, json TEXT NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS sessions (sid TEXT PRIMARY KEY, json TEXT NOT NULL, expires_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS tokens (token TEXT PRIMARY KEY, json TEXT NOT NULL, created_at INTEGER NOT NULL);
`;

const now = (): number => Date.now();

function parse<T>(raw: string | undefined, fallback: T): T {
  if (raw == null) return fallback;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

/** Open (creating if needed) the SQLite metadata store at the configured path. */
export function makeSqliteMetadataStore(
  config: Pick<Config, 'sqlitePath' | 'dataDir'>,
): MetadataStore {
  const file = config.sqlitePath ?? `${config.dataDir}/octo-doc.db`;
  if (file !== ':memory:') mkdirSync(dirname(file), { recursive: true });
  const db = new DatabaseSync(file);
  db.exec('PRAGMA journal_mode = WAL');
  db.exec('PRAGMA busy_timeout = 5000');
  db.exec(SCHEMA);

  const q = {
    getMeta: db.prepare('SELECT json FROM meta WHERE slug = ?'),
    putMeta: db.prepare(
      'INSERT INTO meta(slug,json,updated_at) VALUES(?,?,?) ON CONFLICT(slug) DO UPDATE SET json=excluded.json, updated_at=excluded.updated_at',
    ),
    delMeta: db.prepare('DELETE FROM meta WHERE slug = ?'),
    listMeta: db.prepare('SELECT slug, json FROM meta ORDER BY slug'),
    getComments: db.prepare('SELECT json FROM comments WHERE slug = ?'),
    putComments: db.prepare(
      'INSERT INTO comments(slug,json,updated_at) VALUES(?,?,?) ON CONFLICT(slug) DO UPDATE SET json=excluded.json, updated_at=excluded.updated_at',
    ),
    delComments: db.prepare('DELETE FROM comments WHERE slug = ?'),
    getSession: db.prepare('SELECT json, expires_at FROM sessions WHERE sid = ?'),
    putSession: db.prepare(
      'INSERT INTO sessions(sid,json,expires_at) VALUES(?,?,?) ON CONFLICT(sid) DO UPDATE SET json=excluded.json, expires_at=excluded.expires_at',
    ),
    delSession: db.prepare('DELETE FROM sessions WHERE sid = ?'),
    gcSessions: db.prepare('DELETE FROM sessions WHERE expires_at < ?'),
    getToken: db.prepare('SELECT json FROM tokens WHERE token = ?'),
    putToken: db.prepare(
      'INSERT INTO tokens(token,json,created_at) VALUES(?,?,?) ON CONFLICT(token) DO NOTHING',
    ),
    countTokens: db.prepare('SELECT COUNT(*) AS n FROM tokens'),
  };

  return {
    getMeta: (slug) =>
      Promise.resolve(
        parse<DocMeta | null>((q.getMeta.get(slug) as { json?: string })?.json, null),
      ),
    putMeta: (slug, meta) => {
      q.putMeta.run(slug, JSON.stringify(meta), now());
      return Promise.resolve();
    },
    deleteMeta: (slug) => {
      q.delMeta.run(slug);
      return Promise.resolve();
    },
    listMeta: () =>
      Promise.resolve(
        (q.listMeta.all() as { slug: string; json: string }[]).map((r) => ({
          slug: r.slug,
          meta: parse<DocMeta>(r.json, { slug: r.slug, title: r.slug, versions: [] }),
        })),
      ),

    getComments: (slug) => {
      const v = parse<Comment[]>((q.getComments.get(slug) as { json?: string })?.json, []);
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
      const row = q.getSession.get(sid) as { json: string; expires_at: number } | undefined;
      if (!row) return Promise.resolve(null);
      if (row.expires_at < now()) {
        q.delSession.run(sid);
        return Promise.resolve(null);
      }
      return Promise.resolve(parse<Session | null>(row.json, null));
    },
    putSession: (sid, data, ttlSeconds) => {
      q.putSession.run(sid, JSON.stringify(data), now() + ttlSeconds * 1000);
      q.gcSessions.run(now());
      return Promise.resolve();
    },
    deleteSession: (sid) => {
      q.delSession.run(sid);
      return Promise.resolve();
    },

    getToken: (token) =>
      Promise.resolve(
        parse<TokenRecord | null>((q.getToken.get(token) as { json?: string })?.json, null),
      ),
    putToken: (token, record) => {
      q.putToken.run(token, JSON.stringify(record), now());
      return Promise.resolve();
    },
    anyToken: () => Promise.resolve((q.countTokens.get() as { n: number }).n > 0),

    close: () => {
      db.close();
      return Promise.resolve();
    },
  };
}
