import {
  logger
} from "./chunk-TEU6VA76.js";
import {
  loadConfig
} from "./chunk-4DEK7H4H.js";

// src/storage/migrate.ts
import { readFileSync, readdirSync, existsSync } from "fs";
import { fileURLToPath } from "url";
import { dirname, join } from "path";
var here = dirname(fileURLToPath(import.meta.url));
var migrationsDir = [join(here, "..", "..", "migrations"), join(process.cwd(), "migrations")].find(
  (d) => existsSync(d)
) ?? join(process.cwd(), "migrations");
var log = logger();
async function run() {
  const config = loadConfig(process.env);
  const [metaKind] = config.storage.split("+");
  const files = readdirSync(migrationsDir).filter((f) => /^\d+.*\.sql$/.test(f)).sort();
  if (metaKind === "postgres") {
    const url = process.env.DATABASE_URL ?? process.env.PG_URL;
    if (!url) throw new Error("STORAGE=postgres requires DATABASE_URL");
    const pg = await import("pg");
    const pool = new pg.default.Pool({ connectionString: url });
    for (const f of files) {
      const sql = readFileSync(join(migrationsDir, f), "utf8").replace(
        /\bjson\s+TEXT\b/gi,
        "json JSONB"
      );
      log.info({ file: f }, "applying migration");
      await pool.query(sql);
    }
    await pool.end();
    log.info({ count: files.length }, "postgres migrated");
  } else {
    const { makeSqliteMetadataStore } = await import("./sqlite-R6A6VDI5.js");
    const store = makeSqliteMetadataStore(config);
    await store.anyToken();
    await store.close();
    log.info({ count: files.length }, "sqlite schema ensured");
  }
}
void run();
//# sourceMappingURL=migrate-PNFCYTAZ.js.map