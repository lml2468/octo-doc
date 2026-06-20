// 12-factor config — every knob is an env var, documented in .env.example.
// No config files, no Cloudflare bindings. Defaults make `npx`/`node start`
// work with zero setup (SQLite + ./data, public reads, ephemeral admin token).

export function loadConfig(env = process.env) {
  const truthy = (v, d = false) => (v == null ? d : /^(1|true|yes|on)$/i.test(v));
  return {
    port: Number(env.PORT || 8080),
    host: env.HOST || '0.0.0.0',
    // Public base URL used to build absolute doc links in API responses.
    // Falls back to a relative path when unset (the CLI/overlay both accept it).
    baseUrl: (env.BASE_URL || '').replace(/\/$/, ''),
    storage: env.STORAGE || 'sqlite+fs',
    dataDir: env.DATA_DIR || './data',
    sqlitePath: env.SQLITE_PATH || undefined,
    // Write auth. WRITE_TOKEN, if set, is the canonical Bearer token. Otherwise
    // tokens provisioned via /api/admin/bootstrap (persisted in the meta store)
    // are accepted. ADMIN_BOOTSTRAP gates whether bootstrap is allowed.
    writeToken: env.WRITE_TOKEN || '',
    allowBootstrap: truthy(env.ALLOW_BOOTSTRAP, true),
    // Read privacy. When PRIVATE=1, GET routes also require the Bearer token.
    private: truthy(env.PRIVATE, false),
    // Owner login for the (otherwise hidden) catalog at /me.
    owner: (env.OWNER || '').trim(),
    // CSP frame-ancestors for rendered docs. Default 'none' (no embedding).
    // Set to e.g. "'self' https://panel.example.com" to allow a parent panel.
    frameAncestors: (env.FRAME_ANCESTORS || "'none'").trim(),
    // GitHub Device Flow (optional — enables sign-in + per-user comments).
    githubClientId: env.GITHUB_CLIENT_ID || '',
    // Rate limiting (writes). 0 disables.
    rateLimitWindowMs: Number(env.RATE_LIMIT_WINDOW_MS || 60000),
    rateLimitMax: Number(env.RATE_LIMIT_MAX || 60),
    // Single-document HTML size cap (failure mode: "HTML 无大小上限").
    maxHtmlBytes: Number(env.MAX_HTML_BYTES || 5 * 1024 * 1024),
    // Per-slug version quota (failure mode: "无版本 GC/配额"). 0 disables.
    maxVersionsPerSlug: Number(env.MAX_VERSIONS_PER_SLUG || 0),
    logLevel: env.LOG_LEVEL || 'info',
    // Cookie Secure flag — on behind TLS (Caddy/nginx). Auto-off for plain HTTP
    // local dev unless forced.
    cookieSecure: truthy(env.COOKIE_SECURE, true),
  };
}

// Single source of truth for slug validation. Every route that turns a slug
// into a storage key MUST run it through here first.
export function safeSlug(slug) {
  return (typeof slug === 'string' && /^[a-zA-Z0-9_-]{1,64}$/.test(slug)) ? slug : null;
}
