-- octo-doc schema. Applied automatically by the SQLite/Postgres adapters at
-- open; this file is the canonical reference + the input to `npm run migrate`
-- for Postgres deployments. SQLite uses TEXT json columns; Postgres uses JSONB
-- (the adapter substitutes the type). Keep the two in sync.

CREATE TABLE IF NOT EXISTS meta (
  slug        TEXT PRIMARY KEY,
  json        TEXT NOT NULL,        -- doc metadata: { title, slug, versions:[{n,created}] }
  updated_at  BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS comments (
  slug        TEXT PRIMARY KEY,
  json        TEXT NOT NULL,        -- the full event-log comment array for the slug
  updated_at  BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  sid         TEXT PRIMARY KEY,
  json        TEXT NOT NULL,        -- { login, avatar_url, name, created }
  expires_at  BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS tokens (
  token       TEXT PRIMARY KEY,
  json        TEXT NOT NULL,        -- { token, created, label }
  created_at  BIGINT NOT NULL
);
