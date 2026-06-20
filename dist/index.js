import {
  makeSqliteMetadataStore
} from "./chunk-MC5VWS5B.js";
import {
  initLogger,
  logger
} from "./chunk-D5FVZ23H.js";
import {
  loadConfig,
  safeSlug
} from "./chunk-4DEK7H4H.js";

// src/index.ts
import { serve } from "@hono/node-server";

// src/app.ts
import { Hono as Hono5 } from "hono";
import { cors } from "hono/cors";

// src/errors.ts
var AppError = class extends Error {
  constructor(status, code, message, options) {
    super(message, options);
    this.status = status;
    this.code = code;
    this.name = new.target.name;
  }
  status;
  code;
};
var ValidationError = class extends AppError {
  constructor(message, code = "invalid_request") {
    super(400, code, message);
  }
};
var UnauthorizedError = class extends AppError {
  constructor(message = "unauthorized", code = "unauthorized") {
    super(401, code, message);
  }
};
var ForbiddenError = class extends AppError {
  constructor(message = "forbidden", code = "forbidden") {
    super(403, code, message);
  }
};
var NotFoundError = class extends AppError {
  constructor(message = "not found", code = "not_found") {
    super(404, code, message);
  }
};
var ConflictError = class extends AppError {
  constructor(message, code = "conflict") {
    super(409, code, message);
  }
};
var PayloadTooLargeError = class extends AppError {
  constructor(message, code = "payload_too_large") {
    super(413, code, message);
  }
};
var RateLimitedError = class extends AppError {
  constructor(retryAfterSeconds) {
    super(429, "rate_limited", "rate limit exceeded");
    this.retryAfterSeconds = retryAfterSeconds;
  }
  retryAfterSeconds;
};
var UpstreamError = class extends AppError {
  constructor(message, code = "upstream_error", cause) {
    super(502, code, message, { cause });
  }
};

// src/storage/io.ts
async function withTimeout(promise, ms, label) {
  let timer;
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(
      () => reject(new UpstreamError(`${label} timed out after ${ms}ms`, "io_timeout")),
      ms
    );
  });
  try {
    return await Promise.race([promise, timeout]);
  } finally {
    clearTimeout(timer);
  }
}
async function withRetry(fn, opts) {
  const { retries, timeoutMs, label, retryable = () => true } = opts;
  let lastErr;
  for (let attempt = 0; attempt <= retries; attempt++) {
    try {
      return await withTimeout(fn(), timeoutMs, label);
    } catch (err) {
      lastErr = err;
      if (attempt === retries || !retryable(err)) break;
      await delay(50 * 2 ** attempt);
    }
  }
  throw lastErr instanceof Error ? new UpstreamError(
    `${label} failed after ${retries + 1} attempt(s): ${lastErr.message}`,
    "io_failed",
    lastErr
  ) : new UpstreamError(`${label} failed`, "io_failed", lastErr);
}
function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// src/storage/fs.ts
import { createHash, randomBytes } from "crypto";
import {
  mkdirSync,
  writeFileSync,
  readFileSync,
  statSync,
  existsSync,
  readdirSync,
  rmSync,
  renameSync,
  unlinkSync
} from "fs";
import { join } from "path";
function hashSlug(slug) {
  return createHash("sha256").update(slug).digest("hex").slice(0, 32);
}
function makeFsBlobStore(config2) {
  const root = join(config2.dataDir, "blobs");
  mkdirSync(root, { recursive: true });
  const dirFor = (slug) => join(root, hashSlug(slug));
  const fileFor = (slug, version) => join(dirFor(slug), `v${version}`, "index.html");
  return {
    putDoc: (slug, version, html) => {
      const dir = join(dirFor(slug), `v${version}`);
      mkdirSync(dir, { recursive: true });
      try {
        writeFileSync(join(dirFor(slug), "slug.txt"), slug);
      } catch {
      }
      const final = join(dir, "index.html");
      const tmp = join(dir, `.index.html.${randomBytes(6).toString("hex")}.tmp`);
      try {
        writeFileSync(tmp, html);
        renameSync(tmp, final);
      } catch (err) {
        try {
          if (existsSync(tmp)) unlinkSync(tmp);
        } catch {
        }
        throw err;
      }
      return Promise.resolve({ size: Buffer.byteLength(html) });
    },
    getDoc: (slug, version) => {
      const file = fileFor(slug, version);
      return Promise.resolve(existsSync(file) ? readFileSync(file, "utf8") : null);
    },
    headDoc: (slug, version) => {
      const file = fileFor(slug, version);
      return Promise.resolve(existsSync(file) ? { size: statSync(file).size } : null);
    },
    listVersions: (slug) => {
      const dir = dirFor(slug);
      if (!existsSync(dir)) return Promise.resolve([]);
      const versions = readdirSync(dir).map((n) => /^v(\d+)$/.exec(n)).filter((m) => m !== null).filter((m) => existsSync(join(dir, m[0], "index.html"))).map((m) => Number(m[1])).sort((a, b) => a - b);
      return Promise.resolve(versions);
    },
    deleteDoc: (slug) => {
      const dir = dirFor(slug);
      if (existsSync(dir)) rmSync(dir, { recursive: true, force: true });
      return Promise.resolve();
    }
  };
}

// src/storage/index.ts
function resilient(obj, config2) {
  const wrapped = {};
  for (const key of Object.keys(obj)) {
    const fn = obj[key];
    if (typeof fn !== "function") {
      wrapped[key] = fn;
      continue;
    }
    wrapped[key] = (...args) => withRetry(() => fn.apply(obj, args), {
      retries: config2.ioRetries,
      timeoutMs: config2.ioTimeoutMs,
      label: `storage.${String(key)}`
    });
  }
  return wrapped;
}
async function makeStores(config2) {
  const [metaKind, blobKind] = config2.storage.split("+");
  const rawMeta = metaKind === "postgres" ? await (await import("./postgres-2MA443FG.js")).makePostgresMetadataStore(config2) : makeSqliteMetadataStore(config2);
  const rawBlob = blobKind === "s3" ? await (await import("./s3-6IJPACL7.js")).makeS3BlobStore(config2) : makeFsBlobStore(config2);
  return {
    metaStore: resilient(rawMeta, config2),
    blobStore: resilient(rawBlob, config2),
    spec: `${metaKind}+${blobKind}`
  };
}

// src/core/stamp.ts
var STAMPABLE_TAGS = [
  "img",
  "svg",
  "canvas",
  "video",
  "pre",
  "figure",
  "iframe",
  "section",
  "aside",
  "blockquote",
  "table",
  "details"
];
var RAW_TEXT_TAGS = ["script", "style", "textarea", "title"];
var INTRINSIC_ATTRS = ["viewBox", "src", "alt", "aria-label", "title"];
function cyrb53(str, seed = 0) {
  let h1 = 3735928559 ^ seed;
  let h2 = 1103547991 ^ seed;
  for (let i = 0; i < str.length; i++) {
    const ch = str.charCodeAt(i);
    h1 = Math.imul(h1 ^ ch, 2654435761);
    h2 = Math.imul(h2 ^ ch, 1597334677);
  }
  h1 = Math.imul(h1 ^ h1 >>> 16, 2246822507) ^ Math.imul(h2 ^ h2 >>> 13, 3266489909);
  h2 = Math.imul(h2 ^ h2 >>> 16, 2246822507) ^ Math.imul(h1 ^ h1 >>> 13, 3266489909);
  return (4294967296 * (2097151 & h2) + (h1 >>> 0)).toString(36);
}
function aidFor(tag, innerHtml, openAttrs) {
  const intrinsics = INTRINSIC_ATTRS.map((a) => {
    const m = new RegExp("\\b" + a + '\\s*=\\s*"([^"]*)"', "i").exec(openAttrs);
    return m ? a + "=" + m[1] : "";
  }).filter(Boolean).join("|");
  const norm = innerHtml.replace(/<!--[\s\S]*?-->/g, "").replace(/\sdata-tdoc-[\w-]+\s*=\s*"[^"]*"/gi, "").replace(/\s+/g, " ").trim();
  return cyrb53(tag + "|" + intrinsics + "|" + norm);
}
function attrAwareOpenTagEnd(html, lt) {
  let quote = null;
  for (let i = lt + 1; i < html.length; i++) {
    const ch = html[i];
    if (quote) {
      if (ch === quote) quote = null;
      continue;
    }
    if (ch === '"' || ch === "'") quote = ch;
    else if (ch === ">") return i + 1;
  }
  return -1;
}
function skipRawTextBodyAt(html, openTag, attrs, openEnd) {
  if (/\/\s*$/.test(attrs)) return openEnd;
  const closeRe = new RegExp(`</${openTag}\\s*>`, "i");
  const m = closeRe.exec(html.slice(openEnd));
  return m ? openEnd + m.index + m[0].length : html.length;
}
function collectHeadings(html) {
  const headRe = /<h([1-3])\b[^>]*>([\s\S]*?)<\/h\1>/gi;
  const headings = [];
  let m;
  while (m = headRe.exec(html)) {
    headings.push({
      end: m.index + m[0].length,
      text: (m[2] ?? "").replace(/<[^>]+>/g, "").replace(/\s+/g, " ").trim()
    });
  }
  return headings;
}
function findCloseEnd(html, tag, openEnd) {
  const closeRe = new RegExp(`</${tag}\\s*>`, "gi");
  const openRe = new RegExp(`<${tag}\\b`, "gi");
  const rawRe = new RegExp(`<(${RAW_TEXT_TAGS.join("|")})\\b`, "gi");
  let depth = 1;
  let scan = openEnd;
  while (scan < html.length) {
    closeRe.lastIndex = scan;
    openRe.lastIndex = scan;
    rawRe.lastIndex = scan;
    const close = closeRe.exec(html);
    const open = openRe.exec(html);
    const raw = rawRe.exec(html);
    const candidates = [close, open, raw].filter((x) => x !== null);
    if (candidates.length === 0) break;
    const next = candidates.sort((a, b) => a.index - b.index)[0];
    if (next === raw) {
      const rEnd = attrAwareOpenTagEnd(html, next.index);
      if (rEnd < 0) break;
      scan = skipRawTextBodyAt(
        html,
        (next[1] ?? "").toLowerCase(),
        html.slice(next.index, rEnd),
        rEnd
      );
    } else if (next === close) {
      if (--depth === 0) return next.index + next[0].length;
      scan = next.index + next[0].length;
    } else {
      depth++;
      const oEnd = attrAwareOpenTagEnd(html, next.index);
      scan = oEnd < 0 ? next.index + next[0].length : oEnd;
    }
  }
  return openEnd;
}
function harvest(html, openStart, openEnd, tag, attrs, seen, elements) {
  if (seen.has(openStart)) return;
  const isVoid = /^(img|iframe)$/i.test(tag) || /\/\s*$/.test(attrs);
  let closeEnd = openEnd;
  let innerHtml = "";
  if (!isVoid) {
    closeEnd = findCloseEnd(html, tag, openEnd);
    innerHtml = html.slice(openEnd, closeEnd - `</${tag}>`.length);
  }
  seen.add(openStart);
  elements.push({ openStart, openEnd, closeEnd, tag, attrs, innerHtml, isVoid });
}
function harvestStampableTags(html, seen, elements) {
  for (const tag of STAMPABLE_TAGS) {
    const openRe = new RegExp(`<${tag}\\b`, "gi");
    let m;
    while (m = openRe.exec(html)) {
      const end = attrAwareOpenTagEnd(html, m.index);
      if (end < 0) continue;
      harvest(
        html,
        m.index,
        end,
        tag,
        html.slice(m.index + 1 + tag.length, end - 1),
        seen,
        elements
      );
    }
  }
}
function harvestOptInMarkers(html, seen, elements) {
  const probe = /<([a-z][\w-]*)\b/gi;
  let m;
  while (m = probe.exec(html)) {
    const tag = (m[1] ?? "").toLowerCase();
    const end = attrAwareOpenTagEnd(html, m.index);
    if (end < 0) continue;
    const attrs = html.slice(m.index + 1 + (m[1] ?? "").length, end - 1);
    if (/\bdata-tdoc-artifact\b/i.test(attrs) || /class\s*=\s*"[^"]*\btdoc-artifact\b[^"]*"/i.test(attrs)) {
      harvest(html, m.index, end, tag, attrs, seen, elements);
    }
  }
}
function stampAids(rawHtml) {
  const headings = collectHeadings(rawHtml);
  const nearestHeadingAt = (idx) => {
    let best = null;
    for (const h of headings) {
      if (h.end <= idx) best = h.text;
      else break;
    }
    return best;
  };
  const seen = /* @__PURE__ */ new Set();
  const harvested = [];
  harvestStampableTags(rawHtml, seen, harvested);
  harvestOptInMarkers(rawHtml, seen, harvested);
  const aids = [];
  const elements = harvested.map((e) => {
    const cleanedAttrs = e.attrs.replace(/\s+data-tdoc-aid\s*=\s*"[^"]*"/gi, "");
    const cleanedInner = e.innerHtml.replace(/\sdata-tdoc-aid\s*=\s*"[^"]*"/gi, "");
    const aid = aidFor(e.tag, cleanedInner, cleanedAttrs);
    aids.push({
      aid,
      tag: e.tag,
      head: e.innerHtml.slice(0, 80),
      heading: nearestHeadingAt(e.openStart)
    });
    return { ...e, cleanedAttrs, aid };
  });
  elements.sort((a, b) => b.openStart - a.openStart);
  let out = rawHtml;
  for (const e of elements) {
    const selfClose = /\/\s*$/.test(e.attrs) ? "/" : "";
    const stampedOpen = e.isVoid ? `<${e.tag}${e.cleanedAttrs} data-tdoc-aid="${e.aid}"${selfClose}>` : `<${e.tag}${e.cleanedAttrs} data-tdoc-aid="${e.aid}">`;
    out = out.slice(0, e.openStart) + stampedOpen + out.slice(e.openEnd);
  }
  return { html: out, aids };
}

// src/services/doc-service.ts
var DocService = class {
  constructor(blobs, meta, comments, opts) {
    this.blobs = blobs;
    this.meta = meta;
    this.comments = comments;
    this.opts = opts;
  }
  blobs;
  meta;
  comments;
  opts;
  /**
   * Publish a new (or explicitly-versioned) document.
   *
   * @throws {ValidationError} if the HTML is empty
   * @throws {PayloadTooLargeError} if the HTML exceeds the configured cap
   */
  async publish(input) {
    if (typeof input.html !== "string" || input.html.length === 0) {
      throw new ValidationError("html (file) required", "html_required");
    }
    if (Buffer.byteLength(input.html) > this.opts.maxHtmlBytes) {
      throw new PayloadTooLargeError(
        `document exceeds ${this.opts.maxHtmlBytes} bytes`,
        "html_too_large"
      );
    }
    const version = await this.resolveVersion(input.slug, input.version);
    const { html: stamped, aids } = stampAids(input.html);
    const put = await this.blobs.putDoc(input.slug, version, stamped);
    if (!await this.blobs.headDoc(input.slug, version)) {
      throw new ValidationError("blob write did not persist", "blob_write_lost");
    }
    await this.upsertMeta(input, version);
    const merge = await this.comments.publishMerge(input.slug, {
      localComments: input.localComments ?? [],
      aids,
      version
    });
    const mergedComments = merge.body.mergedComments ?? 0;
    return {
      slug: input.slug,
      version,
      url: `${this.opts.baseUrl}/d/${input.slug}/v/${version}`,
      size: put.size,
      aids: aids.length,
      mergedComments
    };
  }
  /** Fetch raw stored HTML + the version list for rendering, or null if absent. */
  async render(slug, version) {
    const html = await this.blobs.getDoc(slug, version);
    if (html == null) return null;
    const meta = await this.meta.getMeta(slug);
    const versions = meta && Array.isArray(meta.versions) ? meta.versions.map((v) => ({ n: v.n, created: v.created ?? null })) : null;
    return { html, versions };
  }
  /** List versions for a slug (meta-derived, falling back to blob scan). */
  async listVersions(slug) {
    const meta = await this.meta.getMeta(slug);
    const blobVersions = await this.blobs.listVersions(slug);
    if (!meta && blobVersions.length === 0) return null;
    const versions = meta && Array.isArray(meta.versions) && meta.versions.length ? meta.versions.map((v) => ({ n: v.n, created: v.created ?? null })) : blobVersions.map((n) => ({ n, created: null }));
    return { slug, title: meta?.title ?? slug, versions };
  }
  /** Delete all versions, metadata, and comments for a slug. */
  async remove(slug) {
    await this.blobs.deleteDoc(slug);
    await this.meta.deleteMeta(slug);
    await this.comments.wipe(slug);
  }
  /**
   * List all docs with a reachable latest version, for the owner catalog. A doc
   * whose latest blob is missing is skipped so the catalog never links to a 404.
   */
  async listAllForOwner() {
    const all = await this.meta.listMeta();
    const out = [];
    for (const { slug, meta } of all) {
      const latest = meta.versions?.[meta.versions.length - 1]?.n ?? 1;
      if (await this.blobs.headDoc(slug, latest)) {
        out.push({ slug, title: meta.title ?? slug, latest });
      }
    }
    return out;
  }
  /** Next version = max existing + 1, unless an explicit version was given. */
  async resolveVersion(slug, explicit) {
    if (explicit && Number.isFinite(explicit)) return Number(explicit);
    const existing = await this.blobs.listVersions(slug);
    return (existing.length ? Math.max(...existing) : 0) + 1;
  }
  /** Merge the new version into the slug's monotonic version list + metadata. */
  async upsertMeta(input, version) {
    const prev = await this.meta.getMeta(input.slug) ?? {
      slug: input.slug,
      title: input.slug,
      versions: []
    };
    const versions = Array.isArray(prev.versions) ? prev.versions.slice() : [];
    if (!versions.some((v) => v.n === version))
      versions.push({ n: version, created: (/* @__PURE__ */ new Date()).toISOString() });
    versions.sort((a, b) => a.n - b.n);
    await this.meta.putMeta(input.slug, {
      ...prev,
      ...input.meta,
      slug: input.slug,
      title: input.title ?? input.meta?.title ?? prev.title ?? input.slug,
      versions
    });
  }
};

// src/core/comment-events.ts
var eidCounter = 0;
function eventEid(e) {
  switch (e.kind) {
    case "reaction_added":
    case "reaction_removed":
      return `${e.kind}:${e.emoji}:${e.by}`;
    case "marked_applied":
    case "marked_open":
    case "deleted":
      return `${e.kind}:${e.at_version}`;
    default: {
      const nonce = (eidCounter++).toString(36);
      const hi = Math.floor(
        (typeof performance !== "undefined" ? performance.now() : 0) * 1e3
      ).toString(36);
      return `${e.kind}:${e.at}:${nonce}_${hi}`;
    }
  }
}
function backfillEids(events) {
  if (!Array.isArray(events)) return false;
  let changed = false;
  for (const e of events) {
    if (e && !e.eid) {
      e.eid = eventEid(e);
      changed = true;
    }
  }
  return changed;
}
function appendEvent(c, event) {
  if (!Array.isArray(c.events)) c.events = [];
  if (!event.eid) event.eid = eventEid(event);
  c.events.push(event);
}
function dedupEvents(events) {
  if (!Array.isArray(events)) return [];
  const lastByEid = /* @__PURE__ */ new Map();
  for (const e of events) if (e?.eid) lastByEid.set(e.eid, e);
  const out = [];
  const emitted = /* @__PURE__ */ new Set();
  for (const e of events) {
    if (!e) continue;
    if (!e.eid) {
      out.push(e);
      continue;
    }
    if (emitted.has(e.eid)) continue;
    emitted.add(e.eid);
    out.push(lastByEid.get(e.eid));
  }
  return out;
}
function legacyToEvents(c) {
  const events = [];
  const at = c.created || (/* @__PURE__ */ new Date()).toISOString();
  const v = Number(c.version) || 1;
  events.push({ kind: "created", at_version: v, at, anchor: c.anchor ?? null, text: c.text ?? "" });
  if (c.status === "applied") {
    events.push({
      kind: "marked_applied",
      at_version: Number(c.applied_in) || v,
      at,
      applied_in: Number(c.applied_in) || v,
      by: "tdoc-agent",
      agent_status: "applied"
    });
  }
  appendLegacyReactions(events, c.reactions, v, at);
  appendLegacyReplies(events, c.replies, v, at);
  return events;
}
function appendLegacyReactions(events, reactions, v, at) {
  for (const ev of legacyReactionEvents(reactions, v, at)) {
    events.push({ ...ev, kind: "reaction_added" });
  }
}
function appendLegacyReply(events, r, v, at) {
  const rv = Number(r.version) || v;
  const when = r.created || at;
  events.push({
    kind: "reply_added",
    at_version: rv,
    at: when,
    reply: {
      id: r.id,
      author: r.author ?? null,
      text: r.text ?? "",
      agent_status: r.agent_status ?? null
    }
  });
  for (const ev of legacyReactionEvents(r.reactions, rv, when)) {
    events.push({ ...ev, kind: "reply_reaction_added", reply_id: r.id });
  }
}
function legacyReactionEvents(reactions, v, at) {
  if (!reactions || typeof reactions !== "object") return [];
  const out = [];
  for (const emoji of Object.keys(reactions)) {
    for (const by of reactions[emoji] ?? []) out.push({ at_version: v, at, emoji, by });
  }
  return out;
}
function appendLegacyReplies(events, replies, v, at) {
  if (!Array.isArray(replies)) return;
  for (const raw of replies) appendLegacyReply(events, raw, v, at);
}
function ensureEventLog(c) {
  if (Array.isArray(c.events)) return backfillEids(c.events);
  if (!c.id) return false;
  const events = legacyToEvents(c);
  backfillEids(events);
  c.events = events;
  const first = events[0];
  c.created_in = first?.at_version || Number(c.version) || 1;
  c.author = c.author ?? null;
  c.created = c.created || first?.at || (/* @__PURE__ */ new Date()).toISOString();
  return true;
}
function ensureMigrated(list) {
  let dirty = false;
  for (const c of list) if (ensureEventLog(c)) dirty = true;
  return dirty;
}
function compactComments(comments) {
  let changed = false;
  for (const c of comments) {
    if (!c || !Array.isArray(c.events)) continue;
    backfillEids(c.events);
    const compacted = dedupEvents(c.events);
    if (compacted.length !== c.events.length) {
      c.events = compacted;
      changed = true;
    }
  }
  return changed;
}
function safeParseList(raw) {
  if (!raw) return [];
  try {
    const v = typeof raw === "string" ? JSON.parse(raw) : raw;
    return Array.isArray(v) ? v : [];
  } catch {
    return [];
  }
}

// src/core/comment-fold.ts
var AGENT_STATUS_EMOJI = {
  applied: "\u2705",
  partial: "\u{1F7E1}",
  question: "\u2753"
};
function isFiniteVersion(v) {
  return Number.isFinite(v) && v >= 0;
}
function applyReaction(reactions, emoji, by, add) {
  const users = reactions[emoji] ?? [];
  const idx = users.indexOf(by);
  if (add) {
    if (idx < 0) users.push(by);
    reactions[emoji] = users;
  } else {
    if (idx >= 0) users.splice(idx, 1);
    if (users.length) reactions[emoji] = users;
    else delete reactions[emoji];
  }
}
function applyContentEvent(st, e) {
  const { snap } = st;
  switch (e.kind) {
    case "created":
      snap.anchor = e.anchor ?? null;
      snap.text = e.text || "";
      return true;
    case "text_edited":
      snap.text = e.text || "";
      return true;
    case "anchor_changed":
      snap.anchor = e.anchor ?? null;
      if (e.reset_status) {
        snap.status = "open";
        snap.applied_in = void 0;
      }
      return true;
    default:
      return false;
  }
}
function applyStatusEvent(st, e) {
  const { snap } = st;
  switch (e.kind) {
    case "marked_applied":
      snap.status = "applied";
      snap.applied_in = e.applied_in ?? e.at_version;
      st.agentVerdict = e.agent_status ?? "applied";
      return true;
    case "marked_open":
      snap.status = "open";
      snap.applied_in = void 0;
      st.agentVerdict = e.agent_status ?? null;
      return true;
    case "deleted":
      snap.deleted = true;
      return true;
    default:
      return false;
  }
}
function applyParentReaction(st, e) {
  if (e.kind === "reaction_added" && e.emoji && e.by) {
    applyReaction(st.snap.reactions, e.emoji, e.by, true);
    return true;
  }
  if (e.kind === "reaction_removed" && e.emoji && e.by) {
    applyReaction(st.snap.reactions, e.emoji, e.by, false);
    return true;
  }
  return false;
}
function applyCommentEvent(st, e) {
  if (applyContentEvent(st, e)) return;
  if (applyStatusEvent(st, e)) return;
  if (applyParentReaction(st, e)) return;
  applyReplyEvent(st, e);
}
function addReply(st, e) {
  if (!e.reply?.id) return;
  st.replyOrder.push(e.reply.id);
  st.replyById.set(e.reply.id, {
    id: e.reply.id,
    parent_id: st.snap.id,
    author: e.reply.author ?? null,
    text: e.reply.text || "",
    agent_status: e.reply.agent_status ?? null,
    created: e.at,
    reactions: {},
    deleted: false
  });
}
function applyReplyReaction(st, e) {
  const r = st.replyById.get(e.reply_id);
  if (r && e.emoji && e.by)
    applyReaction(r.reactions, e.emoji, e.by, e.kind === "reply_reaction_added");
}
function applyReplyEvent(st, e) {
  switch (e.kind) {
    case "reply_added":
      return addReply(st, e);
    case "reply_text_edited": {
      const r = st.replyById.get(e.reply_id);
      if (r) r.text = e.text || "";
      return;
    }
    case "reply_deleted": {
      const r = st.replyById.get(e.reply_id);
      if (r) r.deleted = true;
      return;
    }
    case "reply_reaction_added":
    case "reply_reaction_removed":
      return applyReplyReaction(st, e);
    default:
      return;
  }
}
function orderedEvents(events) {
  return dedupEvents(events).map((e, i) => ({ e, i })).sort((a, b) => (a.e.at_version || 0) - (b.e.at_version || 0) || a.i - b.i).map((x) => x.e);
}
function emptySnapshot(c) {
  return {
    id: c.id,
    author: c.author,
    created: c.created,
    created_in: c.created_in,
    version: c.created_in,
    anchor: null,
    text: "",
    status: "open",
    applied_in: void 0,
    replies: [],
    reactions: {},
    deleted: false
  };
}
function replay(c, at) {
  const st = {
    snap: emptySnapshot(c),
    replyOrder: [],
    replyById: /* @__PURE__ */ new Map(),
    agentVerdict: null
  };
  for (const e of orderedEvents(c.events)) {
    const v = e.at_version;
    if (isFiniteVersion(v) && v <= at) applyCommentEvent(st, e);
  }
  return st;
}
function finalize(st) {
  const verdict = st.agentVerdict;
  if (verdict && AGENT_STATUS_EMOJI[verdict]) {
    applyReaction(st.snap.reactions, AGENT_STATUS_EMOJI[verdict], "tdoc-agent", true);
  }
  st.snap.replies = st.replyOrder.map((id) => st.replyById.get(id)).filter((r) => !!r && !r.deleted);
  return st.snap;
}
function snapshotAt(c, v) {
  ensureEventLog(c);
  if (!Array.isArray(c.events) || c.events.length === 0) return null;
  const at = isFiniteVersion(v) ? v : Infinity;
  if (c.created_in != null && c.created_in > at) return null;
  return finalize(replay(c, at));
}
function snapshotList(list, v) {
  const out = [];
  for (const c of list) {
    const s = snapshotAt(c, v);
    if (s && !s.deleted) out.push(s);
  }
  return out;
}
function historyList(list) {
  return snapshotList(list, Infinity);
}

// src/core/reconcile.ts
function knownAid(a) {
  if (a.kind === "element" && a.aid) return a.aid;
  if (a.kind === "element" && a.selector) {
    return /\[data-tdoc-aid="([\w]+)"\]/.exec(a.selector)?.[1] ?? null;
  }
  return null;
}
function findRebindAid(a, aids) {
  const wantTag = a.fingerprint?.tag ?? a.label?.toLowerCase() ?? "";
  const wantHead = a.fallback?.nearestHeading?.text;
  const matches = aids.filter(
    (x) => (!wantTag || x.tag === wantTag) && (!wantHead || (x.heading ?? "").toLowerCase() === wantHead.toLowerCase())
  );
  if (matches.length === 1) return matches[0].aid;
  if (matches.length === 0) {
    const tagOnly = aids.filter((x) => !wantTag || x.tag === wantTag);
    if (tagOnly.length === 1) return tagOnly[0].aid;
  }
  return null;
}
function carry(a) {
  return {
    ...a.fingerprint ? { fingerprint: a.fingerprint } : {},
    ...a.fallback ? { fallback: a.fallback } : {}
  };
}
function nextAnchor(a, aids) {
  const newAid = findRebindAid(a, aids);
  if (newAid) {
    return {
      kind: "element",
      aid: newAid,
      selector: `[data-tdoc-aid="${newAid}"]`,
      label: a.label ?? a.fingerprint?.tag ?? "element",
      ...carry(a)
    };
  }
  if (a.kind === "lost") return null;
  return {
    kind: "lost",
    reason: "no_candidate",
    ...a.label ? { label: a.label } : {},
    ...carry(a)
  };
}
function reconcileEvent(anchor, version, at) {
  return {
    kind: "anchor_changed",
    at_version: version,
    at,
    by: "reconcile",
    reset_status: false,
    anchor
  };
}
function reconcileComment(c, aids, byAid, version, at) {
  const snap = snapshotAt(c, version);
  if (!snap || snap.deleted) return;
  const a = snap.anchor;
  if (!a || a.kind !== "element" && a.kind !== "lost") return;
  const aid = knownAid(a);
  if (aid && byAid.has(aid)) return;
  const anchor = nextAnchor(a, aids);
  if (anchor) appendEvent(c, reconcileEvent(anchor, version, at));
}
function reconcileAnchors(comments, aidsInVersion, v) {
  ensureMigrated(comments);
  const byAid = new Set(aidsInVersion.map((a) => a.aid));
  const version = Number(v) || 1;
  const at = (/* @__PURE__ */ new Date()).toISOString();
  for (const c of comments) reconcileComment(c, aidsInVersion, byAid, version, at);
  return comments;
}

// src/core/ops.ts
function findReactionHost(list, id) {
  const top = list.find((c) => c.id === id);
  if (top) return { host: top, replyId: null };
  for (const c of list) {
    const added = (c.events ?? []).find((e) => e.kind === "reply_added" && e.reply?.id === id);
    if (added) return { host: c, replyId: id };
  }
  return null;
}
function reactionsFor(reactions, by, emoji) {
  return (reactions[emoji] ?? []).includes(by);
}
function opCreate(list, op, now) {
  const entry = {
    id: op.id,
    author: op.author,
    created: now,
    created_in: op.version,
    events: [
      {
        kind: "created",
        at_version: op.version,
        at: now,
        anchor: op.anchor ?? null,
        text: op.text
      }
    ]
  };
  backfillEids(entry.events);
  list.push(entry);
  return { status: 200, body: snapshotAt(entry, op.version) };
}
function opReply(list, op, now) {
  const parent = list.find((c) => c.id === op.parent_id);
  if (!parent) return { status: 404, body: { error: "parent_not_found" } };
  appendEvent(parent, {
    kind: "reply_added",
    at_version: op.version,
    at: now,
    reply: { id: op.reply_id, author: op.author, text: op.text, agent_status: null }
  });
  return {
    status: 200,
    body: {
      id: op.reply_id,
      parent_id: op.parent_id,
      author: op.author,
      text: op.text,
      created: now,
      version: op.version
    }
  };
}
function opPatchAnchor(list, op, now) {
  const target = list.find((c) => c.id === op.id);
  if (!target) return { status: 404, body: { error: "not_found" } };
  appendEvent(target, {
    kind: "anchor_changed",
    at_version: op.version,
    at: now,
    reset_status: op.reset_status,
    anchor: op.anchor,
    by: op.actor.login
  });
  return { status: 200, body: snapshotAt(target, op.version) };
}
function reactionsOf(snap, replyId) {
  if (!replyId) return snap.reactions;
  return snap.replies.find((r) => r.id === replyId)?.reactions ?? {};
}
function opReact(list, op, now) {
  const found = findReactionHost(list, op.comment_id);
  if (!found) return { status: 404, body: { error: "not_found" } };
  const { host, replyId } = found;
  const snap = snapshotAt(host, op.version);
  if (!snap) return { status: 404, body: { error: "not_visible_at_version" } };
  const had = reactionsFor(reactionsOf(snap, replyId), op.by, op.emoji);
  const base = { at_version: op.version, at: now, emoji: op.emoji, by: op.by };
  appendEvent(
    host,
    replyId ? {
      ...base,
      kind: had ? "reply_reaction_removed" : "reply_reaction_added",
      reply_id: replyId
    } : { ...base, kind: had ? "reaction_removed" : "reaction_added" }
  );
  const fresh = snapshotAt(host, op.version);
  return { status: 200, body: { ok: true, reactions: reactionsOf(fresh, replyId) } };
}
function opDelete(list, op, now) {
  const top = list.find((c) => c.id === op.id);
  if (top) {
    appendEvent(top, { kind: "deleted", at_version: op.version, at: now, by: op.actor.login });
    return { status: 200, body: { ok: true } };
  }
  for (const c of list) {
    ensureEventLog(c);
    const added = (c.events ?? []).find((e) => e.kind === "reply_added" && e.reply?.id === op.id);
    if (added) {
      appendEvent(c, {
        kind: "reply_deleted",
        at_version: op.version,
        at: now,
        reply_id: op.id,
        by: op.actor.login
      });
      return { status: 200, body: { ok: true } };
    }
  }
  return { status: 404, body: { error: "not_found" } };
}
function opRawEvents(list, op) {
  const target = list.find((c) => c.id === op.id);
  if (!target) return { status: 404, body: { error: "not_found" } };
  for (const ev of op.events) appendEvent(target, ev);
  return { status: 200, body: op.responseBody ?? { ok: true } };
}
function opPublishMerge(list, op) {
  let merged = 0;
  if (Array.isArray(op.localComments) && op.localComments.length) {
    const have = new Set(list.map((c) => c?.id).filter(Boolean));
    for (const lc of op.localComments) {
      if (!lc?.id || have.has(lc.id)) continue;
      ensureEventLog(lc);
      list.push(lc);
      have.add(lc.id);
      merged++;
    }
  }
  if (list.length) {
    reconcileAnchors(list, op.aids ?? [], op.version);
    compactComments(list);
  }
  return { status: 200, body: { mergedComments: merged } };
}
function applyCommentOp(list, op) {
  ensureMigrated(list);
  const now = op.at ?? (/* @__PURE__ */ new Date()).toISOString();
  switch (op.kind) {
    case "create":
      return opCreate(list, op, now);
    case "reply":
      return opReply(list, op, now);
    case "patch_anchor":
      return opPatchAnchor(list, op, now);
    case "react":
      return opReact(list, op, now);
    case "delete":
      return opDelete(list, op, now);
    case "raw_events":
      return opRawEvents(list, op);
    case "wipe":
      return { status: 200, body: { ok: true, deleted: list.length }, wipe: true };
    case "publish_merge":
      return opPublishMerge(list, op);
  }
}

// src/core/mutex.ts
function makeKeyedMutex() {
  const tails = /* @__PURE__ */ new Map();
  return function withLock(key, fn) {
    const prev = tails.get(key) ?? Promise.resolve();
    const run = prev.then(fn, fn);
    const tail = run.then(
      () => void 0,
      () => void 0
    );
    tails.set(key, tail);
    void tail.then(() => {
      if (tails.get(key) === tail) tails.delete(key);
    });
    return run;
  };
}

// src/services/ids.ts
import { randomBytes as randomBytes2 } from "crypto";
function rand(bytes) {
  return randomBytes2(bytes).toString("hex");
}
function newToken() {
  return rand(32);
}
function newSessionId() {
  return rand(24);
}

// src/services/comment-service.ts
var CommentService = class {
  constructor(meta) {
    this.meta = meta;
  }
  meta;
  lock = makeKeyedMutex();
  /** Fold a slug's comments to a version snapshot, or the full cross-version history. */
  async list(slug, scope) {
    const list = await this.read(slug);
    return scope === "all" ? historyList(list) : snapshotList(list, scope);
  }
  /** Read + migrate the raw comment list for a slug (callers fold it). */
  async read(slug) {
    const list = safeParseList(await this.meta.getComments(slug));
    ensureMigrated(list);
    return list;
  }
  /** Create a top-level comment. */
  create(slug, input) {
    return this.mutate(slug, {
      kind: "create",
      id: `c_${Date.now()}_${rand(4)}`,
      author: input.author,
      text: input.text,
      anchor: input.anchor ?? null,
      version: input.version
    });
  }
  /** Add a reply to a parent comment. */
  reply(slug, input) {
    return this.mutate(slug, {
      kind: "reply",
      parent_id: input.parentId,
      reply_id: `r_${Date.now()}_${rand(4)}`,
      author: input.author,
      text: input.text,
      version: input.version
    });
  }
  /** Toggle an emoji reaction on a comment or reply. */
  react(slug, input) {
    return this.mutate(slug, {
      kind: "react",
      comment_id: input.commentId,
      emoji: input.emoji,
      by: input.by,
      version: input.version
    });
  }
  /** Re-anchor a comment (resets its agent verdict). */
  reanchor(slug, input) {
    return this.mutate(slug, {
      kind: "patch_anchor",
      id: input.id,
      anchor: input.anchor,
      reset_status: true,
      version: input.version,
      actor: { login: input.actor }
    });
  }
  /** Soft-delete a comment or reply at a version. */
  remove(slug, input) {
    return this.mutate(slug, {
      kind: "delete",
      id: input.id,
      version: input.version,
      actor: { login: input.actor }
    });
  }
  /** Append pre-built events to a comment (agent reply path). */
  appendRaw(slug, op) {
    return this.mutate(slug, op);
  }
  /** Wipe all comments for a slug. */
  wipe(slug) {
    return this.mutate(slug, { kind: "wipe" });
  }
  /** Publish-time non-destructive merge + anchor reconcile. */
  publishMerge(slug, op) {
    return this.mutate(slug, { kind: "publish_merge", ...op });
  }
  /** Run a comment op under the per-slug lock, persisting on success. */
  mutate(slug, op) {
    return this.lock(slug, async () => {
      const list = safeParseList(await this.meta.getComments(slug));
      const res = applyCommentOp(list, op);
      if (res.status === 200) {
        if (res.wipe) await this.meta.deleteComments(slug);
        else await this.meta.putComments(slug, list);
      }
      return { status: res.status, body: res.body };
    });
  }
};

// src/services/auth-service.ts
import { timingSafeEqual as nodeTimingSafeEqual } from "crypto";

// src/services/github.ts
var GH_USER_AGENT = "octo-doc";
async function ghPostForm(path, form) {
  let res;
  try {
    res = await fetch(`https://github.com${path}`, {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/x-www-form-urlencoded",
        "User-Agent": GH_USER_AGENT
      },
      body: new URLSearchParams(form).toString()
    });
  } catch (err) {
    throw new UpstreamError("github unreachable", "github_unreachable", err);
  }
  const ct = res.headers.get("content-type") ?? "";
  const raw = await res.text();
  if (ct.includes("application/json")) {
    try {
      return JSON.parse(raw);
    } catch {
      throw new UpstreamError("github returned unparseable JSON", "gh_parse");
    }
  }
  return Object.fromEntries(new URLSearchParams(raw));
}
async function ghStartDeviceFlow(clientId) {
  const r = await ghPostForm("/login/device/code", { client_id: clientId, scope: "read:user" });
  if (r.error) throw new ValidationError(r.error_description ?? r.error, r.error);
  return {
    device_code: r.device_code,
    user_code: r.user_code,
    verification_uri: r.verification_uri,
    expires_in: Number(r.expires_in),
    interval: Number(r.interval)
  };
}
async function ghPollAccessToken(clientId, deviceCode) {
  const r = await ghPostForm("/login/oauth/access_token", {
    client_id: clientId,
    device_code: deviceCode,
    grant_type: "urn:ietf:params:oauth:grant-type:device_code"
  });
  if (r.error === "authorization_pending" || r.error === "slow_down") return { pending: true };
  if (r.error) throw new ValidationError(r.error_description ?? r.error, r.error);
  if (!r.access_token) return { pending: true };
  return { pending: false, accessToken: r.access_token };
}
async function ghFetchUser(token) {
  let res;
  try {
    res = await fetch("https://api.github.com/user", {
      headers: {
        Accept: "application/vnd.github+json",
        Authorization: `Bearer ${token}`,
        "User-Agent": GH_USER_AGENT
      }
    });
  } catch (err) {
    throw new UpstreamError("github unreachable", "github_unreachable", err);
  }
  return await res.json();
}

// src/services/auth-service.ts
var SESSION_TTL_SECONDS = 60 * 60 * 24 * 30;
var AuthService = class {
  constructor(meta, config2) {
    this.meta = meta;
    this.config = config2;
  }
  meta;
  config;
  /** Constant-time check that `token` is the static or a provisioned write token. */
  async isValidWriteToken(token) {
    if (!token) return false;
    if (this.config.writeToken && constantTimeEqual(token, this.config.writeToken)) return true;
    return await this.meta.getToken(token) !== null;
  }
  /**
   * Mint the first write token. One-shot: 409s once any token exists or a static
   * token is configured.
   *
   * @throws {ForbiddenError} if bootstrap is disabled
   * @throws {ConflictError} if already bootstrapped
   */
  async bootstrap() {
    if (!this.config.allowBootstrap)
      throw new ForbiddenError("bootstrap disabled", "bootstrap_disabled");
    if (this.config.writeToken)
      throw new ConflictError("a static WRITE_TOKEN is configured", "static_token_configured");
    if (await this.meta.anyToken())
      throw new ConflictError("already bootstrapped", "already_bootstrapped");
    const token = newToken();
    await this.meta.putToken(token, {
      token,
      created: (/* @__PURE__ */ new Date()).toISOString(),
      label: "bootstrap"
    });
    return { token };
  }
  /** Resolve a session from its id, or null. */
  getSession(sid) {
    if (!sid) return Promise.resolve(null);
    return this.meta.getSession(sid);
  }
  /** Whether a session belongs to the configured owner (for the /me catalog). */
  isOwner(session) {
    const owner = this.config.owner.toLowerCase();
    return !!owner && !!session?.login && session.login.toLowerCase() === owner;
  }
  /** Start the GitHub Device Flow. @throws {ValidationError} if auth is unconfigured. */
  startDeviceFlow() {
    this.requireGithub();
    return ghStartDeviceFlow(this.config.githubClientId);
  }
  /**
   * Poll the device flow; on success creates a session and returns the identity
   * plus the new session id. Returns `{ pending: true }` while authorization is
   * outstanding.
   */
  async pollDeviceFlow(deviceCode) {
    this.requireGithub();
    if (!deviceCode) throw new ValidationError("device_code required", "device_code_required");
    const poll = await ghPollAccessToken(this.config.githubClientId, deviceCode);
    if (poll.pending) return { pending: true };
    const user = await ghFetchUser(poll.accessToken);
    if (!user.login) throw new UpstreamError("GitHub returned no login", "no_user");
    const sid = newSessionId();
    const session = {
      login: user.login,
      avatar_url: user.avatar_url ?? null,
      name: user.name ?? user.login,
      created: (/* @__PURE__ */ new Date()).toISOString()
    };
    await this.meta.putSession(sid, session, SESSION_TTL_SECONDS);
    const identity = { login: user.login };
    if (session.avatar_url != null) identity.avatar_url = session.avatar_url;
    if (session.name != null) identity.name = session.name;
    return { pending: false, sid, identity };
  }
  /** Destroy a session. */
  async logout(sid) {
    if (sid) await this.meta.deleteSession(sid);
  }
  /** Session cookie max-age, exposed so routes can set the cookie consistently. */
  get sessionTtlSeconds() {
    return SESSION_TTL_SECONDS;
  }
  requireGithub() {
    if (!this.config.githubClientId)
      throw new ValidationError("auth not configured", "auth_not_configured");
  }
};
function constantTimeEqual(a, b) {
  const ab = Buffer.from(a);
  const bb = Buffer.from(b);
  if (ab.length !== bb.length) return false;
  return nodeTimingSafeEqual(ab, bb);
}

// src/middleware/error.ts
var errorHandler = (err, c) => {
  if (err instanceof RateLimitedError) {
    c.header("Retry-After", String(err.retryAfterSeconds));
    return c.json(
      { error: err.code, message: err.message, retry_after: err.retryAfterSeconds },
      429
    );
  }
  if (err instanceof AppError) {
    if (err.status >= 500) logger().error({ err, code: err.code, cause: err.cause }, err.message);
    else logger().info({ code: err.code }, err.message);
    return c.json({ error: err.code, message: err.message }, err.status);
  }
  logger().error({ err }, "unhandled error");
  return c.json({ error: "internal_error", message: "an unexpected error occurred" }, 500);
};

// src/middleware/rate-limit.ts
import { createMiddleware } from "hono/factory";
function clientIp(headers) {
  const xff = headers.get("x-forwarded-for");
  if (xff) return xff.split(",")[0].trim();
  return headers.get("x-real-ip") ?? "unknown";
}
function rateLimit(opts) {
  const hits = /* @__PURE__ */ new Map();
  return createMiddleware(async (c, next) => {
    if (opts.max <= 0) return next();
    const token = (c.req.header("authorization") ?? "").replace(/^Bearer\s+/, "").slice(0, 16);
    const key = `${token}|${clientIp(c.req.raw.headers)}`;
    const now = Date.now();
    let w = hits.get(key);
    if (!w || w.resetAt < now) {
      w = { count: 0, resetAt: now + opts.windowMs };
      hits.set(key, w);
    }
    w.count++;
    if (w.count > opts.max) {
      throw new RateLimitedError(Math.ceil((w.resetAt - now) / 1e3));
    }
    if (hits.size > 1e4) {
      for (const [k, v] of hits) if (v.resetAt < now) hits.delete(k);
    }
    await next();
  });
}
function rateLimitWrites(opts) {
  const limiter = rateLimit(opts);
  return createMiddleware(
    (c, next) => c.req.method === "GET" ? next() : limiter(c, next)
  );
}

// src/middleware/security.ts
function docSecurityHeaders(frameAncestors) {
  const csp = [
    "default-src 'self' data: blob: https:",
    "script-src 'self' 'unsafe-inline' 'unsafe-eval' data: blob: https:",
    "style-src 'self' 'unsafe-inline' https:",
    "img-src 'self' data: blob: https:",
    "font-src 'self' data: https:",
    "connect-src 'self' https:",
    "base-uri 'self'",
    `frame-ancestors ${frameAncestors}`
  ].join("; ");
  return {
    "Content-Security-Policy": csp,
    "X-Frame-Options": frameAncestors === "'none'" ? "DENY" : "SAMEORIGIN",
    "X-Content-Type-Options": "nosniff",
    "Referrer-Policy": "no-referrer"
  };
}

// src/routes/docs.ts
import { Hono } from "hono";
import { getCookie } from "hono/cookie";

// src/middleware/auth.ts
import { createMiddleware as createMiddleware2 } from "hono/factory";
function bearer(header) {
  const m = /^Bearer\s+(.+)$/.exec(header ?? "");
  return m ? m[1] : null;
}
var requireWriteAuth = createMiddleware2(async (c, next) => {
  const token = bearer(c.req.header("authorization"));
  if (!token || !await c.var.auth.isValidWriteToken(token)) {
    throw new UnauthorizedError();
  }
  c.set("writeToken", token);
  await next();
});
var maybeRequireReadAuth = createMiddleware2(async (c, next) => {
  if (!c.var.config.private) return next();
  const token = bearer(c.req.header("authorization"));
  if (token && await c.var.auth.isValidWriteToken(token)) return next();
  throw new NotFoundError("Not found");
});

// src/core/render.ts
import { readFileSync as readFileSync2 } from "fs";
import { fileURLToPath } from "url";
import { dirname, join as join2 } from "path";
var here = dirname(fileURLToPath(import.meta.url));
function loadOverlay() {
  for (const candidate of [join2(here, "overlay.js"), join2(here, "..", "overlay.js")]) {
    try {
      return readFileSync2(candidate, "utf8");
    } catch {
    }
  }
  throw new Error("overlay.js not found next to render module");
}
var OVERLAY_JS = loadOverlay();
function toOverlayIdentity(source) {
  if (!source) return null;
  const id = { login: source.login };
  if (source.avatar_url != null) id.avatar_url = source.avatar_url;
  if (source.name != null) id.name = source.name;
  return id;
}
function safeJsonForScript(obj) {
  return JSON.stringify(obj).replace(/<\/script>/gi, "<\\/script>").replace(/<!--/g, "<\\!--");
}
function escapeHtml(s) {
  return (s ?? "").replace(
    /[&<>"']/g,
    (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]
  );
}
function forHtmlComment(s) {
  return (s ?? "").replace(/--/g, "-\\-");
}
function injectOverlayCfg(rawHtml, cfg) {
  const inject = `<script>window.__TDOC__ = ${safeJsonForScript(cfg)};</script>
<script>${OVERLAY_JS}</script>`;
  return rawHtml.includes("</body>") ? rawHtml.replace("</body>", `${inject}
</body>`) : rawHtml + inject;
}

// src/routes/fork-export.ts
function reactionsText(rs) {
  if (!rs) return "";
  const parts = Object.entries(rs).filter(([, u]) => u && u.length > 0).map(([e, u]) => `${forHtmlComment(e)} (${u.length})`);
  return parts.length ? `    reactions: ${parts.join(", ")}
` : "";
}
function describeAnchor(c) {
  if (c.anchor?.kind === "element")
    return `(on ${forHtmlComment(c.anchor.label ?? c.anchor.selector ?? "element")})`;
  if (c.anchor && "text" in c.anchor && c.anchor.text) {
    return `(on text: "${forHtmlComment(c.anchor.text.replace(/"/g, '\\"').slice(0, 120))}")`;
  }
  return "(no anchor)";
}
function buildBanner(input, open) {
  let banner = `<!--
  ===== octo-doc fork export =====
  slug: ${forHtmlComment(input.slug)}
  version: ${forHtmlComment(String(input.version))}
  exported: ${(/* @__PURE__ */ new Date()).toISOString()}

  ## How to use this file
  Save it as ~/tdocs/<your-new-slug>/v1/index.html (or anywhere you like).
  Comments below are read-only metadata bundled with the fork. Agents can
  read them to apply changes \u2014 say "apply all comments to this doc" and the
  agent will find the anchored regions (marked with TDOC-COMMENT html
  comments inline below) and modify them accordingly.

  ## Comments included in this export
  ${open.length} comment(s).
`;
  open.forEach((c, i) => {
    const who = c.author?.login ? `@${forHtmlComment(c.author.login)}` : "anonymous";
    banner += `
  [${i + 1}] ${who} ${describeAnchor(c)}
    "${forHtmlComment(c.text.replace(/\n/g, " "))}"
${reactionsText(c.reactions)}`;
    for (const r of c.replies) {
      const rWho = r.author?.login ? `@${forHtmlComment(r.author.login)}` : "anonymous";
      banner += `      \u21B3 ${rWho}: "${forHtmlComment(r.text.replace(/\n/g, " "))}"
${reactionsText(r.reactions).replace(/^/gm, "  ")}`;
    }
  });
  return banner + `
  ===== end octo-doc fork export =====
-->
`;
}
function markAnchoredText(html, open) {
  let out = html;
  for (const c of open) {
    const needle = c.anchor && "text" in c.anchor ? c.anchor.text : void 0;
    if (!needle || needle.length < 2) continue;
    const idx = out.indexOf(needle);
    if (idx === -1) continue;
    const marker = `<!--TDOC-COMMENT id="${forHtmlComment(c.id)}" by="${forHtmlComment(c.author?.login ?? "anonymous")}"-->${needle}<!--/TDOC-COMMENT-->`;
    out = out.slice(0, idx) + marker + out.slice(idx + needle.length);
  }
  return out;
}
function buildForkExport(input) {
  const open = input.comments.filter((c) => c.status !== "resolved");
  const banner = buildBanner(input, open);
  const jsonBlock = `<script type="application/json" id="tdoc-fork-comments">${safeJsonForScript({
    slug: input.slug,
    version: input.version,
    exported: (/* @__PURE__ */ new Date()).toISOString(),
    comments: open
  })}</script>
`;
  let body = markAnchoredText(input.html, open);
  if (input.kind === "fork") {
    body = injectOverlayCfg(body, {
      slug: input.slug,
      version: input.version,
      identity: null,
      authConfigured: false,
      mode: "fork",
      originalSlug: input.slug
    });
  }
  return banner + jsonBlock + body;
}

// src/routes/docs.ts
function requireSlug(value) {
  const slug = safeSlug(value);
  if (!slug) throw new ValidationError("invalid or missing slug", "invalid_slug");
  return slug;
}
async function readMultipartBody(c) {
  const form = await c.req.parseBody();
  const file = form.file;
  const html = file && typeof file === "object" && "text" in file ? await file.text() : typeof form.html === "string" ? form.html : void 0;
  return {
    slug: form.slug,
    html,
    version: form.version ? Number(form.version) : void 0,
    title: typeof form.title === "string" ? form.title : void 0,
    meta: void 0,
    localComments: void 0
  };
}
async function readJsonBody(c) {
  const body = await c.req.json().catch(() => ({}));
  return {
    slug: body.slug,
    html: typeof body.html === "string" ? body.html : void 0,
    version: typeof body.version === "number" ? body.version : void 0,
    title: typeof body.title === "string" ? body.title : void 0,
    meta: body.meta,
    localComments: body.comments
  };
}
function readPublishBody(c) {
  const ct = (c.req.header("content-type") ?? "").toLowerCase();
  return ct.includes("multipart/form-data") ? readMultipartBody(c) : readJsonBody(c);
}
function docRoutes() {
  const app2 = new Hono();
  const publish = async (c) => {
    const body = await readPublishBody(c);
    const slug = requireSlug(body.slug);
    if (body.html === void 0) throw new ValidationError("html (file) required", "html_required");
    const result = await c.var.docs.publish({
      slug,
      html: body.html,
      ...body.version !== void 0 ? { version: body.version } : {},
      ...body.title !== void 0 ? { title: body.title } : {},
      ...body.meta !== void 0 ? { meta: body.meta } : {},
      ...body.localComments !== void 0 ? { localComments: body.localComments } : {}
    });
    return c.json({ ok: true, ...result });
  };
  app2.post("/api/docs", requireWriteAuth, publish);
  app2.post("/api/upload", requireWriteAuth, publish);
  app2.get("/api/docs/:slug/versions", async (c) => {
    const slug = requireSlug(c.req.param("slug"));
    const result = await c.var.docs.listVersions(slug);
    if (!result) throw new NotFoundError();
    return c.json(result);
  });
  app2.on(["GET", "HEAD"], "/d/:slug/v/:version", maybeRequireReadAuth, async (c) => {
    const slug = requireSlug(c.req.param("slug"));
    const vStr = c.req.param("version");
    if (!/^\d+$/.test(vStr)) throw new NotFoundError();
    const version = Number(vStr);
    const data = await c.var.docs.render(slug, version);
    if (!data) throw new NotFoundError(`Not found: ${slug} v${vStr}`);
    const session = await c.var.auth.getSession(getCookie(c, "tdoc_sid"));
    const mode = c.var.config.githubClientId ? "published" : "local";
    const html = injectOverlayCfg(data.html, {
      slug,
      version,
      identity: toOverlayIdentity(session),
      isOwner: c.var.auth.isOwner(session),
      authConfigured: !!c.var.config.githubClientId,
      mode,
      versions: data.versions ?? [{ n: version }]
    });
    return c.html(html);
  });
  app2.get("/d/:slug/v/:version/:kind{export|fork}", maybeRequireReadAuth, async (c) => {
    const slug = requireSlug(c.req.param("slug"));
    const vStr = c.req.param("version");
    if (!/^\d+$/.test(vStr)) throw new NotFoundError();
    const version = Number(vStr);
    const data = await c.var.docs.render(slug, version);
    if (!data) throw new NotFoundError(`Not found: ${slug} v${vStr}`);
    const kind = c.req.param("kind");
    const list = await c.var.comments.list(slug, version);
    const finalHtml = buildForkExport({ slug, version, html: data.html, comments: list, kind });
    const dl = c.req.query("download");
    const forceDownload = dl === "1" || kind === "export" && dl !== "0";
    c.header("Content-Type", "text/html; charset=utf-8");
    if (forceDownload)
      c.header("Content-Disposition", `attachment; filename="${slug}-v${vStr}-fork.html"`);
    return c.body(finalHtml);
  });
  app2.delete("/api/doc", requireWriteAuth, async (c) => {
    const slug = requireSlug(c.req.query("slug"));
    await c.var.docs.remove(slug);
    return c.json({ ok: true });
  });
  return app2;
}

// src/routes/comments.ts
import { Hono as Hono2 } from "hono";
import { getCookie as getCookie2 } from "hono/cookie";
function parseVersion(raw) {
  if (raw == null || raw === "") return Infinity;
  if (raw === "all") return "all";
  const n = Number(raw);
  return Number.isFinite(n) && n >= 0 ? n : Infinity;
}
function requireSlug2(value) {
  const slug = safeSlug(value);
  if (!slug) throw new ValidationError("invalid or missing slug", "invalid_slug");
  return slug;
}
async function viewer(c) {
  const session = await c.var.auth.getSession(getCookie2(c, "tdoc_sid"));
  if (c.var.config.githubClientId && !session)
    throw new UnauthorizedError("sign_in_required", "sign_in_required");
  return session;
}
function authorOf(session) {
  if (!session) return null;
  return {
    login: session.login,
    ...session.avatar_url != null ? { avatar_url: session.avatar_url } : {},
    ...session.name != null ? { name: session.name } : {}
  };
}
function canMutate(record, session, isOwner) {
  if (isOwner) return true;
  const who = record?.author?.login;
  return !!(who && session?.login && who === session.login);
}
function findTarget(list, id) {
  const top = list.find((c) => c.id === id);
  if (top) return top;
  for (const c of list) {
    const added = (c.events ?? []).find((e) => e.kind === "reply_added" && e.reply?.id === id);
    if (added && added.kind === "reply_added") return added.reply;
  }
  return null;
}
function commentRoutes() {
  const app2 = new Hono2();
  app2.get("/api/comments", async (c) => {
    const slug = requireSlug2(c.req.query("slug"));
    const list = await c.var.comments.list(slug, parseVersion(c.req.query("version")));
    return c.json(list);
  });
  app2.post("/api/comments", async (c) => {
    const session = await viewer(c);
    const body = await c.req.json().catch(() => ({}));
    const slug = requireSlug2(body.slug);
    const text = body.text;
    if (typeof text !== "string" || !text)
      throw new ValidationError("slug and text required", "text_required");
    const version = Number(body.version) || 1;
    const res = typeof body.parent_id === "string" ? await c.var.comments.reply(slug, {
      parentId: body.parent_id,
      author: authorOf(session),
      text,
      version
    }) : await c.var.comments.create(slug, {
      author: authorOf(session),
      text,
      anchor: body.anchor ?? null,
      version
    });
    return c.json(res.body, res.status);
  });
  app2.patch("/api/comments", async (c) => {
    const session = await viewer(c);
    const body = await c.req.json().catch(() => ({}));
    const slug = requireSlug2(body.slug);
    const id = body.id;
    const anchor = body.anchor;
    if (typeof id !== "string" || !anchor)
      throw new ValidationError("slug, id, anchor required", "anchor_required");
    const list = await c.var.comments.read(slug);
    const target = findTarget(list, id);
    if (!target) throw new ValidationError("not found", "not_found");
    if (c.var.config.githubClientId && !canMutate(target, session, c.var.auth.isOwner(session))) {
      throw new ForbiddenError("not the author", "not_author");
    }
    const version = Number(body.version) || 1;
    const res = await c.var.comments.reanchor(slug, {
      id,
      anchor,
      version,
      actor: session?.login ?? "local"
    });
    return c.json(res.body, res.status);
  });
  app2.delete("/api/comments", async (c) => {
    const slug = requireSlug2(c.req.query("slug"));
    if (c.req.query("all") === "1") return wipeComments(c, slug);
    const session = await viewer(c);
    const id = c.req.query("id");
    if (!id) throw new ValidationError("slug and id required", "id_required");
    const list = await c.var.comments.read(slug);
    const target = findTarget(list, id);
    if (!target) throw new ValidationError("not found", "not_found");
    if (c.var.config.githubClientId && !canMutate(target, session, c.var.auth.isOwner(session))) {
      throw new ForbiddenError("not the author", "not_author");
    }
    const v = parseVersion(c.req.query("version"));
    const version = typeof v === "number" && Number.isFinite(v) ? v : 999999;
    const res = await c.var.comments.remove(slug, {
      id,
      version,
      actor: session?.login ?? "local"
    });
    return c.json(res.body, res.status);
  });
  app2.post("/api/reactions", async (c) => {
    const session = await viewer(c);
    const body = await c.req.json().catch(() => ({}));
    const slug = requireSlug2(body.slug);
    const commentId = body.comment_id;
    const emoji = body.emoji;
    if (typeof commentId !== "string" || typeof emoji !== "string") {
      throw new ValidationError("slug, comment_id, emoji required", "reaction_fields_required");
    }
    if (emoji.length === 0 || emoji.length > 8)
      throw new ValidationError("invalid emoji", "invalid_emoji");
    const res = await c.var.comments.react(slug, {
      commentId,
      emoji,
      by: session?.login ?? "anon",
      version: Number(body.version) || 1
    });
    return c.json(res.body, res.status);
  });
  app2.post("/api/agent/reply", requireWriteAuth, agentReply);
  return app2;
}
async function wipeComments(c, slug) {
  const token = (c.req.header("authorization") ?? "").replace(/^Bearer\s+/, "");
  if (!await c.var.auth.isValidWriteToken(token)) throw new UnauthorizedError();
  const res = await c.var.comments.wipe(slug);
  return c.json(res.body, res.status);
}
function agentReplyEvents(replyId, author, text, verdict, version, now) {
  const events = [
    {
      kind: "reply_added",
      at_version: version,
      at: now,
      reply: { id: replyId, author, text, agent_status: verdict }
    }
  ];
  if (verdict === "applied") {
    events.push({
      kind: "marked_applied",
      at_version: version,
      at: now,
      applied_in: version,
      by: "tdoc-agent",
      agent_status: "applied"
    });
  } else if (verdict === "partial" || verdict === "question") {
    events.push({
      kind: "marked_open",
      at_version: version,
      at: now,
      by: "tdoc-agent",
      agent_status: verdict
    });
  }
  return events;
}
async function agentReply(c) {
  const body = await c.req.json().catch(() => ({}));
  const slug = requireSlug2(body.slug);
  const parentId = body.parent_id;
  const text = body.text;
  if (typeof parentId !== "string" || typeof text !== "string" || !text) {
    throw new ValidationError("slug, parent_id, text required", "agent_reply_fields_required");
  }
  const list = await c.var.comments.read(slug);
  const parent = list.find((cm) => cm.id === parentId);
  if (!parent) throw new ValidationError("parent not found", "parent_not_found");
  const verdict = ["applied", "partial", "question"].find((s) => s === body.status) ?? null;
  const version = Number(body.applied_in) || parent.created_in || 1;
  const now = (/* @__PURE__ */ new Date()).toISOString();
  const replyId = `r_${Date.now()}_${rand(4)}`;
  const author = {
    kind: "agent",
    login: "tdoc-agent",
    name: "tdoc-agent",
    avatar_url: null
  };
  const res = await c.var.comments.appendRaw(slug, {
    kind: "raw_events",
    id: parentId,
    events: agentReplyEvents(replyId, author, text, verdict, version, now),
    responseBody: {
      id: replyId,
      parent_id: parentId,
      text,
      author,
      agent_status: verdict,
      created: now,
      reactions: {}
    }
  });
  return c.json(res.body, res.status);
}

// src/routes/admin.ts
import { Hono as Hono3 } from "hono";
import { getCookie as getCookie3, setCookie } from "hono/cookie";
function setSessionCookie(c, sid, maxAge) {
  setCookie(c, "tdoc_sid", sid, {
    path: "/",
    httpOnly: true,
    secure: c.var.config.cookieSecure,
    sameSite: "Lax",
    maxAge
  });
}
function adminRoutes() {
  const app2 = new Hono3();
  app2.get("/api/ping", (c) => c.json({ ok: true, service: "tdoc" }));
  app2.get("/healthz", (c) => c.json({ ok: true }));
  app2.get("/api/admin/bootstrap", async (c) => {
    const result = await c.var.auth.bootstrap();
    return c.json({ ok: true, ...result });
  });
  app2.get("/api/auth/me", async (c) => {
    const session = await c.var.auth.getSession(getCookie3(c, "tdoc_sid"));
    return c.json({
      identity: session ? { login: session.login, avatar_url: session.avatar_url ?? null, name: session.name } : null,
      isOwner: c.var.auth.isOwner(session),
      authConfigured: !!c.var.config.githubClientId
    });
  });
  app2.post("/api/auth/device/start", async (c) => c.json(await c.var.auth.startDeviceFlow()));
  app2.post("/api/auth/device/poll", async (c) => {
    const body = await c.req.json().catch(() => ({}));
    if (typeof body.device_code !== "string")
      throw new ValidationError("device_code required", "device_code_required");
    const result = await c.var.auth.pollDeviceFlow(body.device_code);
    if (result.pending) return c.json({ pending: true });
    setSessionCookie(c, result.sid, c.var.auth.sessionTtlSeconds);
    return c.json({ ok: true, identity: result.identity });
  });
  app2.post("/api/auth/logout", async (c) => {
    await c.var.auth.logout(getCookie3(c, "tdoc_sid"));
    setCookie(c, "tdoc_sid", "", { path: "/", maxAge: 0, secure: c.var.config.cookieSecure });
    return c.json({ ok: true });
  });
  return app2;
}

// src/routes/pages.ts
import { Hono as Hono4 } from "hono";
import { getCookie as getCookie4 } from "hono/cookie";
function pageRoutes() {
  const app2 = new Hono4();
  app2.get("/", (c) => c.html(landingHtml(c.var.config.repoUrl)));
  app2.get("/me", async (c) => {
    const session = await c.var.auth.getSession(getCookie4(c, "tdoc_sid"));
    if (!c.var.auth.isOwner(session)) return c.redirect(c.var.config.repoUrl, 302);
    const all = await c.var.docs.listAllForOwner();
    return c.html(catalogHtml(session, all));
  });
  return app2;
}
function landingHtml(repo) {
  const repoLabel = repo.replace(/^https?:\/\//, "");
  return `<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>octo-doc</title>
<style>
  body { font: 15px system-ui, -apple-system, sans-serif; min-height: 100vh; margin: 0;
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    color: #111; background: #fff; gap: 10px; }
  h1 { font-size: 30px; margin: 0; color: #1652f0; }
  p { color: #666; margin: 0; }
  a { color: #1652f0; text-decoration: none; }
  a:hover { text-decoration: underline; }
  .sub { margin-top: 14px; font-size: 13px; color: #888; }
</style></head><body>
  <h1>octo-doc</h1>
  <p>Prompt-native, commentable documents. Self-hosted.</p>
  <p class="sub">Open a document from its shared link \xB7
    <a href="${escapeHtml(repo)}">${escapeHtml(repoLabel)}</a></p>
</body></html>`;
}
function catalogHtml(session, docs) {
  const rows = docs.map(
    (d) => `<tr>
      <td><a href="/d/${encodeURIComponent(d.slug)}/v/${d.latest}">${escapeHtml(d.title)}</a></td>
      <td>${escapeHtml(d.slug)}</td>
      <td>v${d.latest}</td>
    </tr>`
  ).join("");
  const who = session?.login ? ` \xB7 signed in as <b>${escapeHtml(session.login)}</b>` : "";
  return `<!doctype html><html><head><meta charset="utf-8"><title>octo-doc</title>
<style>
  body { font: 15px system-ui, -apple-system, sans-serif; max-width: 760px; margin: 60px auto; padding: 0 20px; color: #111; }
  h1 { font-size: 28px; margin: 0 0 4px; color: #1652f0; }
  table { width: 100%; border-collapse: collapse; }
  th, td { text-align: left; padding: 10px 12px; border-bottom: 1px solid #eee; }
  th { font-size: 12px; text-transform: uppercase; color: #888; letter-spacing: 0.04em; }
  a { color: #1652f0; text-decoration: none; }
  a:hover { text-decoration: underline; }
  .empty { color: #888; padding: 40px 0; text-align: center; }
  .who { color: #888; font-size: 13px; margin: 0 0 32px; }
  .who b { color: #444; font-weight: 600; }
</style></head><body>
<h1>My docs</h1>
<p class="who">Documents hosted on this server${who}.</p>
${rows.length === 0 ? '<p class="empty">No published docs yet.</p>' : `<table><thead><tr><th>Title</th><th>Slug</th><th>Version</th></tr></thead><tbody>${rows}</tbody></table>`}
</body></html>`;
}

// src/app.ts
async function createApp(env = process.env, deps = {}) {
  const config2 = loadConfig(env);
  const stores2 = deps.stores ?? await makeStores(config2);
  const comments = new CommentService(stores2.metaStore);
  const docs = new DocService(stores2.blobStore, stores2.metaStore, comments, {
    baseUrl: config2.baseUrl,
    maxHtmlBytes: config2.maxHtmlBytes
  });
  const auth = new AuthService(stores2.metaStore, config2);
  const app2 = new Hono5();
  app2.use("*", async (c, next) => {
    c.set("config", config2);
    c.set("docs", docs);
    c.set("comments", comments);
    c.set("auth", auth);
    await next();
  });
  app2.use(
    "/api/*",
    cors({
      origin: "*",
      allowMethods: ["GET", "POST", "PATCH", "DELETE", "OPTIONS"],
      allowHeaders: ["Content-Type", "Authorization"]
    })
  );
  const secHeaders = docSecurityHeaders(config2.frameAncestors);
  app2.use("/d/*", async (c, next) => {
    await next();
    for (const [k, v] of Object.entries(secHeaders)) c.header(k, v);
  });
  const limiter = rateLimit({ windowMs: config2.rateLimitWindowMs, max: config2.rateLimitMax });
  app2.use("/api/docs", limiter);
  app2.use("/api/upload", limiter);
  app2.use(
    "/api/comments",
    rateLimitWrites({ windowMs: config2.rateLimitWindowMs, max: config2.rateLimitMax })
  );
  app2.use("/api/reactions", limiter);
  app2.use("/api/agent/*", limiter);
  app2.route("/", adminRoutes());
  app2.route("/", docRoutes());
  app2.route("/", commentRoutes());
  app2.route("/", pageRoutes());
  app2.notFound((c) => c.text("Not found", 404));
  app2.onError(errorHandler);
  return {
    app: app2,
    config: config2,
    stores: stores2,
    close: () => stores2.metaStore.close()
  };
}

// src/index.ts
var { app, config, stores } = await createApp(process.env);
var log = initLogger(config.logLevel);
var server = serve({ fetch: app.fetch, port: config.port, hostname: config.host }, (info) => {
  log.info(
    {
      addr: `http://${config.host}:${info.port}`,
      storage: stores.spec,
      private: config.private,
      auth: config.githubClientId ? "github-device-flow" : "anonymous",
      writeToken: config.writeToken ? "static" : config.allowBootstrap ? "bootstrap" : "none"
    },
    "octo-doc listening"
  );
});
function shutdown(signal) {
  log.info({ signal }, "shutting down");
  server.close(() => {
    void stores.metaStore.close().then(() => process.exit(0));
  });
  setTimeout(() => process.exit(1), 5e3).unref();
}
process.on("SIGTERM", () => shutdown("SIGTERM"));
process.on("SIGINT", () => shutdown("SIGINT"));
//# sourceMappingURL=index.js.map