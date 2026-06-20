// src/config.ts
var truthy = (v, dflt) => v == null ? dflt : /^(1|true|yes|on)$/i.test(v);
var num = (v, dflt) => {
  const n = Number(v);
  return Number.isFinite(n) ? n : dflt;
};
var str = (v, dflt) => v ?? dflt;
function parseStorage(raw) {
  const storage = str(raw, "sqlite+fs").toLowerCase();
  if (!/^(sqlite|postgres)\+(fs|s3)$/.test(storage)) {
    throw new Error(`invalid STORAGE "${storage}" (expected sqlite|postgres + fs|s3)`);
  }
  return storage;
}
function loadConfig(env = process.env) {
  return Object.freeze({
    port: num(env.PORT, 8080),
    host: str(env.HOST, "0.0.0.0"),
    baseUrl: str(env.BASE_URL, "").replace(/\/$/, ""),
    repoUrl: str(env.REPO_URL, "https://github.com/lml2468/octo-doc"),
    storage: parseStorage(env.STORAGE),
    dataDir: str(env.DATA_DIR, "./data"),
    sqlitePath: env.SQLITE_PATH,
    writeToken: str(env.WRITE_TOKEN, ""),
    allowBootstrap: truthy(env.ALLOW_BOOTSTRAP, true),
    private: truthy(env.PRIVATE, false),
    owner: str(env.OWNER, "").trim(),
    frameAncestors: str(env.FRAME_ANCESTORS, "'none'").trim(),
    githubClientId: str(env.GITHUB_CLIENT_ID, ""),
    rateLimitWindowMs: num(env.RATE_LIMIT_WINDOW_MS, 6e4),
    rateLimitMax: num(env.RATE_LIMIT_MAX, 60),
    maxHtmlBytes: num(env.MAX_HTML_BYTES, 5 * 1024 * 1024),
    logLevel: str(env.LOG_LEVEL, "info"),
    cookieSecure: truthy(env.COOKIE_SECURE, true),
    ioTimeoutMs: num(env.IO_TIMEOUT_MS, 5e3),
    ioRetries: num(env.IO_RETRIES, 2)
  });
}
function safeSlug(slug) {
  return typeof slug === "string" && /^[a-zA-Z0-9_-]{1,64}$/.test(slug) ? slug : null;
}

export {
  loadConfig,
  safeSlug
};
//# sourceMappingURL=chunk-4DEK7H4H.js.map