// Comment, reaction, and agent-reply routes. Ported from the Worker; the
// per-slug Durable Object is replaced by commentStore (mutex-serialized).
import { Hono } from 'hono';
import { snapshotList, historyList, ensureEventLog, snapshotAt } from '../core/comments.js';
import { safeSlug } from '../config.js';
import { getSession, canMutate, rand } from './auth.js';
import { requireWriteAuth } from '../middleware/security.js';

function parseVersionParam(c) {
  const v = c.req.query('version');
  if (v == null || v === '') return Infinity;
  if (v === 'all') return 'all';
  const n = Number(v);
  return Number.isFinite(n) && n >= 0 ? n : Infinity;
}

export function commentRoutes(ctx) {
  const { config, metaStore, commentStore } = ctx;
  const app = new Hono();
  const requireSession = config.githubClientId;

  // GET /api/comments?slug=&version=
  app.get('/api/comments', async (c) => {
    const slug = safeSlug(c.req.query('slug'));
    if (!slug) return c.json({ error: 'invalid or missing slug' }, 400);
    const list = await commentStore.readComments(slug);
    const V = parseVersionParam(c);
    return c.json(V === 'all' ? historyList(list) : snapshotList(list, V));
  });

  // POST /api/comments — create or reply
  app.post('/api/comments', async (c) => {
    const s = await getSession(c, metaStore);
    if (requireSession && !s) return c.json({ error: 'sign_in_required' }, 401);
    let body = {};
    try { body = await c.req.json(); } catch {}
    const slug = safeSlug(body.slug);
    const { version, anchor, text: commentText, parent_id } = body;
    if (!slug || !commentText) return c.json({ error: 'slug and text required' }, 400);
    const author = s ? { login: s.login, avatar_url: s.avatar_url, name: s.name } : null;
    const created = new Date().toISOString();
    const V = Number(version) || 1;
    const op = parent_id
      ? { kind: 'reply', slug, parent_id, reply_id: `r_${Date.now()}_${rand(4)}`, author, text: commentText, version: V, at: created }
      : { kind: 'create', slug, id: `c_${Date.now()}_${rand(4)}`, author, text: commentText, anchor: anchor || null, version: V, at: created };
    const res = await commentStore.mutateComments(slug, op);
    return c.json(res.body, res.status);
  });

  // PATCH /api/comments — re-anchor (author or owner only)
  app.patch('/api/comments', async (c) => {
    const s = await getSession(c, metaStore);
    if (requireSession && !s) return c.json({ error: 'sign_in_required' }, 401);
    let body = {};
    try { body = await c.req.json(); } catch {}
    const slug = safeSlug(body.slug);
    const { id, anchor, version } = body;
    if (!slug || !id || !anchor) return c.json({ error: 'slug, id, anchor required' }, 400);
    const authList = await commentStore.readComments(slug);
    const target = authList.find(x => x.id === id);
    if (!target) return c.json({ error: 'not_found' }, 404);
    if (requireSession && !canMutate(target, s, config)) return c.json({ error: 'not_author' }, 403);
    const V = Number(version) || target.created_in || 1;
    const res = await commentStore.mutateComments(slug, {
      kind: 'patch_anchor', slug, id, anchor, reset_status: true, version: V, actor: { login: s?.login || 'local' },
    });
    return c.json(res.body, res.status);
  });

  // DELETE /api/comments?slug=&id=&version=  (or ?all=1 for admin wipe)
  app.delete('/api/comments', async (c) => {
    const slug = safeSlug(c.req.query('slug'));
    if (!slug) return c.json({ error: 'invalid slug' }, 400);

    // Admin wipe — Bearer-authّd.
    if (c.req.query('all') === '1') {
      const { isValidWriteToken } = await import('../middleware/security.js');
      const auth = c.req.header('authorization') || '';
      const m = auth.match(/^Bearer\s+(.+)$/);
      if (!m || !(await isValidWriteToken(c, config, m[1]))) return c.json({ error: 'unauthorized' }, 401);
      const res = await commentStore.mutateComments(slug, { kind: 'wipe', slug });
      return c.json(res.body, res.status);
    }

    const s = await getSession(c, metaStore);
    if (requireSession && !s) return c.json({ error: 'sign_in_required' }, 401);
    const id = c.req.query('id');
    if (!id) return c.json({ error: 'slug and id required' }, 400);
    const V = parseVersionParam(c);
    const stampVersion = Number.isFinite(V) ? V : 999999;

    const authList = await commentStore.readComments(slug);
    let authorized = false;
    const top = authList.find(x => x.id === id);
    if (top) {
      if (requireSession && !canMutate(top, s, config)) return c.json({ error: 'not_author' }, 403);
      authorized = true;
    } else {
      for (const cm of authList) {
        ensureEventLog(cm);
        const reply = (cm.events || []).find(e => e.kind === 'reply_added' && e.reply && e.reply.id === id);
        if (reply) {
          if (requireSession && !canMutate(reply.reply, s, config)) return c.json({ error: 'not_author' }, 403);
          authorized = true;
          break;
        }
      }
    }
    if (!authorized) return c.json({ error: 'not_found' }, 404);
    const res = await commentStore.mutateComments(slug, {
      kind: 'delete', slug, id, version: stampVersion, actor: { login: s?.login || 'local' },
    });
    return c.json(res.body, res.status);
  });

  // POST /api/reactions — toggle emoji on a comment or reply
  app.post('/api/reactions', async (c) => {
    const s = await getSession(c, metaStore);
    if (requireSession && !s) return c.json({ error: 'sign_in_required' }, 401);
    let body = {};
    try { body = await c.req.json(); } catch {}
    const slug = safeSlug(body.slug);
    const { comment_id, emoji, version } = body;
    if (!slug || !comment_id || !emoji) return c.json({ error: 'slug, comment_id, emoji required' }, 400);
    if (emoji.length > 8 || emoji.length === 0) return c.json({ error: 'invalid_emoji' }, 400);
    const V = Number(version) || 1;
    const by = s ? s.login : 'anon';
    const res = await commentStore.mutateComments(slug, { kind: 'react', slug, comment_id, emoji, by, version: V });
    return c.json(res.body, res.status);
  });

  // POST /api/agent/reply — agent posts a reply + verdict (Bearer-authّd)
  app.post('/api/agent/reply', requireWriteAuth(config), async (c) => {
    let body = {};
    try { body = await c.req.json(); } catch {}
    const slug = safeSlug(body.slug);
    const { parent_id, text: replyText, status: agentStatus, applied_in, bind_anchor_aid } = body;
    if (!slug || !parent_id || !replyText) return c.json({ error: 'slug, parent_id, text required' }, 400);
    const authList = await commentStore.readComments(slug);
    const parent = authList.find(x => x.id === parent_id);
    if (!parent) return c.json({ error: 'parent_not_found' }, 404);

    const verdict = ['applied', 'partial', 'question'].includes(agentStatus) ? agentStatus : null;
    const V = Number(applied_in) || parent.created_in || 1;
    const now = new Date().toISOString();
    const replyId = `r_${Date.now()}_${rand(4)}`;
    const agentAuthor = { kind: 'agent', login: 'tdoc-agent', name: 'tdoc-agent', avatar_url: null };

    const events = [{
      kind: 'reply_added', at_version: V, at: now,
      reply: { id: replyId, author: agentAuthor, text: replyText, agent_status: verdict },
    }];
    if (verdict === 'applied') {
      events.push({ kind: 'marked_applied', at_version: V, at: now, applied_in: V, by: 'tdoc-agent', agent_status: 'applied' });
    } else if (verdict === 'partial' || verdict === 'question') {
      events.push({ kind: 'marked_open', at_version: V, at: now, by: 'tdoc-agent', agent_status: verdict });
    }
    if (bind_anchor_aid && typeof bind_anchor_aid === 'string') {
      const cur = snapshotAt(parent, V) || {};
      const fallback = cur.anchor?.fallback;
      const label = cur.anchor?.label || 'svg';
      events.push({
        kind: 'anchor_changed', at_version: V, at: now, by: 'tdoc-agent', reset_status: false,
        anchor: { kind: 'element', aid: bind_anchor_aid, selector: `[data-tdoc-aid="${bind_anchor_aid}"]`, label, ...(fallback ? { fallback } : {}) },
      });
    }
    const res = await commentStore.mutateComments(slug, {
      kind: 'raw_events', slug, id: parent_id, events,
      responseBody: { id: replyId, parent_id, text: replyText, author: agentAuthor, agent_status: verdict, created: now, reactions: {} },
    });
    return c.json(res.body, res.status);
  });

  return app;
}
