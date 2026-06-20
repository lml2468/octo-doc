/**
 * 12-factor configuration. Every knob is an env var (see `.env.example`). Parsed
 * once at boot into a frozen, fully-typed {@link Config}; no other module reads
 * `process.env` for app settings.
 */

/** Storage selector: `<metadata>+<blob>`, e.g. `sqlite+fs` or `postgres+s3`. */
export type StorageSpec = `${'sqlite' | 'postgres'}+${'fs' | 's3'}`;

/** Fully-resolved, immutable application configuration. */
export interface Config {
  readonly port: number;
  readonly host: string;
  readonly baseUrl: string;
  readonly repoUrl: string;
  readonly storage: StorageSpec;
  readonly dataDir: string;
  readonly sqlitePath: string | undefined;
  readonly writeToken: string;
  readonly allowBootstrap: boolean;
  readonly private: boolean;
  readonly owner: string;
  readonly frameAncestors: string;
  readonly githubClientId: string;
  readonly rateLimitWindowMs: number;
  readonly rateLimitMax: number;
  readonly maxHtmlBytes: number;
  readonly logLevel: string;
  readonly cookieSecure: boolean;
  /** I/O timeout (ms) applied at storage boundaries. */
  readonly ioTimeoutMs: number;
  /** Retry attempts for transient storage failures. */
  readonly ioRetries: number;
}

type Env = Record<string, string | undefined>;

const truthy = (v: string | undefined, dflt: boolean): boolean =>
  v == null ? dflt : /^(1|true|yes|on)$/i.test(v);

const num = (v: string | undefined, dflt: number): number => {
  const n = Number(v);
  return Number.isFinite(n) ? n : dflt;
};

const str = (v: string | undefined, dflt: string): string => v ?? dflt;

/** Validate + normalize the STORAGE spec. */
function parseStorage(raw: string | undefined): StorageSpec {
  const storage = str(raw, 'sqlite+fs').toLowerCase();
  if (!/^(sqlite|postgres)\+(fs|s3)$/.test(storage)) {
    throw new Error(`invalid STORAGE "${storage}" (expected sqlite|postgres + fs|s3)`);
  }
  return storage as StorageSpec;
}

/** Parse and validate configuration from an environment object. */
export function loadConfig(env: Env = process.env): Config {
  return Object.freeze({
    port: num(env.PORT, 8080),
    host: str(env.HOST, '0.0.0.0'),
    baseUrl: str(env.BASE_URL, '').replace(/\/$/, ''),
    repoUrl: str(env.REPO_URL, 'https://github.com/lml2468/octo-doc'),
    storage: parseStorage(env.STORAGE),
    dataDir: str(env.DATA_DIR, './data'),
    sqlitePath: env.SQLITE_PATH,
    writeToken: str(env.WRITE_TOKEN, ''),
    allowBootstrap: truthy(env.ALLOW_BOOTSTRAP, true),
    private: truthy(env.PRIVATE, false),
    owner: str(env.OWNER, '').trim(),
    frameAncestors: str(env.FRAME_ANCESTORS, "'none'").trim(),
    githubClientId: str(env.GITHUB_CLIENT_ID, ''),
    rateLimitWindowMs: num(env.RATE_LIMIT_WINDOW_MS, 60_000),
    rateLimitMax: num(env.RATE_LIMIT_MAX, 60),
    maxHtmlBytes: num(env.MAX_HTML_BYTES, 5 * 1024 * 1024),
    logLevel: str(env.LOG_LEVEL, 'info'),
    cookieSecure: truthy(env.COOKIE_SECURE, true),
    ioTimeoutMs: num(env.IO_TIMEOUT_MS, 5000),
    ioRetries: num(env.IO_RETRIES, 2),
  });
}

/** Single source of truth for slug validation. */
export function safeSlug(slug: unknown): string | null {
  return typeof slug === 'string' && /^[a-zA-Z0-9_-]{1,64}$/.test(slug) ? slug : null;
}
