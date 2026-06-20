// src/storage/postgres.ts
var SCHEMA = `
CREATE TABLE IF NOT EXISTS meta (slug TEXT PRIMARY KEY, json JSONB NOT NULL, updated_at BIGINT NOT NULL);
CREATE TABLE IF NOT EXISTS comments (slug TEXT PRIMARY KEY, json JSONB NOT NULL, updated_at BIGINT NOT NULL);
CREATE TABLE IF NOT EXISTS sessions (sid TEXT PRIMARY KEY, json JSONB NOT NULL, expires_at BIGINT NOT NULL);
CREATE TABLE IF NOT EXISTS tokens (token TEXT PRIMARY KEY, json JSONB NOT NULL, created_at BIGINT NOT NULL);
`;
var now = () => Date.now();
async function makePostgresMetadataStore(config) {
  const url = process.env.DATABASE_URL ?? process.env.PG_URL;
  if (!url) throw new Error("STORAGE=postgres requires DATABASE_URL");
  const pg = await import("pg");
  const pool = new pg.default.Pool({
    connectionString: url,
    max: Number(process.env.PG_POOL_MAX ?? 10)
  });
  await pool.query(SCHEMA);
  void config;
  return {
    async getMeta(slug) {
      const r = await pool.query("SELECT json FROM meta WHERE slug=$1", [slug]);
      return r.rows[0]?.json ?? null;
    },
    async putMeta(slug, meta) {
      await pool.query(
        "INSERT INTO meta(slug,json,updated_at) VALUES($1,$2,$3) ON CONFLICT(slug) DO UPDATE SET json=$2, updated_at=$3",
        [slug, JSON.stringify(meta), now()]
      );
    },
    async deleteMeta(slug) {
      await pool.query("DELETE FROM meta WHERE slug=$1", [slug]);
    },
    async listMeta() {
      const r = await pool.query(
        "SELECT slug, json FROM meta ORDER BY slug"
      );
      return r.rows.map((row) => ({ slug: row.slug, meta: row.json }));
    },
    async getComments(slug) {
      const r = await pool.query("SELECT json FROM comments WHERE slug=$1", [
        slug
      ]);
      const v = r.rows[0]?.json;
      return Array.isArray(v) ? v : [];
    },
    async putComments(slug, list) {
      await pool.query(
        "INSERT INTO comments(slug,json,updated_at) VALUES($1,$2,$3) ON CONFLICT(slug) DO UPDATE SET json=$2, updated_at=$3",
        [slug, JSON.stringify(list), now()]
      );
    },
    async deleteComments(slug) {
      await pool.query("DELETE FROM comments WHERE slug=$1", [slug]);
    },
    async getSession(sid) {
      const r = await pool.query(
        "SELECT json, expires_at FROM sessions WHERE sid=$1",
        [sid]
      );
      const row = r.rows[0];
      if (!row) return null;
      if (Number(row.expires_at) < now()) {
        await pool.query("DELETE FROM sessions WHERE sid=$1", [sid]);
        return null;
      }
      return row.json;
    },
    async putSession(sid, data, ttlSeconds) {
      await pool.query(
        "INSERT INTO sessions(sid,json,expires_at) VALUES($1,$2,$3) ON CONFLICT(sid) DO UPDATE SET json=$2, expires_at=$3",
        [sid, JSON.stringify(data), now() + ttlSeconds * 1e3]
      );
      await pool.query("DELETE FROM sessions WHERE expires_at < $1", [now()]);
    },
    async deleteSession(sid) {
      await pool.query("DELETE FROM sessions WHERE sid=$1", [sid]);
    },
    async getToken(token) {
      const r = await pool.query("SELECT json FROM tokens WHERE token=$1", [
        token
      ]);
      return r.rows[0]?.json ?? null;
    },
    async putToken(token, record) {
      await pool.query(
        "INSERT INTO tokens(token,json,created_at) VALUES($1,$2,$3) ON CONFLICT(token) DO NOTHING",
        [token, JSON.stringify(record), now()]
      );
    },
    async anyToken() {
      const r = await pool.query("SELECT COUNT(*)::int AS n FROM tokens");
      return Number(r.rows[0]?.n ?? 0) > 0;
    },
    async close() {
      await pool.end();
    }
  };
}
export {
  makePostgresMetadataStore
};
//# sourceMappingURL=postgres-2MA443FG.js.map