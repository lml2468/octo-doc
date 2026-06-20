// Session + GitHub Device Flow helpers. Ported from the Worker, with KV swapped
// for the metadata store. Sessions are optional: without GITHUB_CLIENT_ID the
// device-flow endpoints return a clear "auth not configured" error and reads
// stay anonymous (the local-mode behavior).
import { randomBytes } from 'node:crypto';
import { getCookie, setCookie } from 'hono/cookie';

export function rand(n) {
  return randomBytes(n).toString('hex');
}

export async function getSession(c, metaStore) {
  const sid = getCookie(c, 'tdoc_sid');
  if (!sid) return null;
  const data = await metaStore.getSession(sid);
  if (!data) return null;
  return { id: sid, ...data };
}

export function isOwnerSession(config, session) {
  const owner = (config.owner || '').trim().toLowerCase();
  if (!owner || !session || !session.login) return false;
  return session.login.toLowerCase() === owner;
}

// Authorization for mutating a comment/reply: DENY by default. Allow only the
// record's author or the doc owner. A record with a null author is NOT mutable
// by an arbitrary signed-in user.
export function canMutate(record, session, config) {
  if (isOwnerSession(config, session)) return true;
  const who = record && record.author && record.author.login;
  return !!(who && session && session.login && who === session.login);
}

export function setSessionCookie(c, config, sid, maxAge) {
  setCookie(c, 'tdoc_sid', sid, {
    path: '/', httpOnly: true, secure: config.cookieSecure, sameSite: 'Lax', maxAge,
  });
}
export function clearSessionCookie(c, config) {
  setCookie(c, 'tdoc_sid', '', { path: '/', maxAge: 0, secure: config.cookieSecure });
}

// ── GitHub API helpers ───────────────────────────────────────────────────────
export async function ghPost(path, formObj) {
  const body = new URLSearchParams(formObj).toString();
  const r = await fetch(`https://github.com${path}`, {
    method: 'POST',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/x-www-form-urlencoded',
      'User-Agent': 'octo-doc',
    },
    body,
  });
  const ct = r.headers.get('content-type') || '';
  const raw = await r.text();
  if (ct.includes('application/json')) {
    try { return JSON.parse(raw); } catch { return { error: 'gh_parse', error_description: raw.slice(0, 200) }; }
  }
  const params = new URLSearchParams(raw);
  const out = {};
  for (const [k, v] of params) out[k] = v;
  if (!Object.keys(out).length) return { error: 'gh_empty', error_description: `status=${r.status} ct=${ct}` };
  return out;
}

export async function ghUser(token) {
  const r = await fetch('https://api.github.com/user', {
    headers: {
      Accept: 'application/vnd.github+json',
      Authorization: `Bearer ${token}`,
      'User-Agent': 'octo-doc',
    },
  });
  return r.json();
}
