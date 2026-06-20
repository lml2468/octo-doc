/**
 * Comment, reaction, and agent-reply HTTP routes. Thin handlers over
 * {@link CommentService}; authorization (author/owner) is resolved here, the
 * serialized write happens in the service.
 */
import { Hono, type Context } from 'hono';
import { getCookie } from 'hono/cookie';
import type { AppEnv } from '../http-context.js';
import { requireSlug } from '../config.js';
import { ForbiddenError, UnauthorizedError, ValidationError } from '../errors.js';
import { requireWriteAuth, bearer } from '../middleware/auth.js';
import { findHost } from '../core/index.js';
import type { AgentStatus, Author, Comment, CommentEvent } from '../core/index.js';
import type { Session } from '../storage/types.js';
import { rand } from '../services/ids.js';

/** Parse the `version` query/body value: a number, `'all'`, or Infinity (latest). */
function parseVersion(raw: string | undefined): number | 'all' {
  if (raw == null || raw === '') return Infinity;
  if (raw === 'all') return 'all';
  const n = Number(raw);
  return Number.isFinite(n) && n >= 0 ? n : Infinity;
}

/** Resolve the viewer session, requiring one when GitHub auth is configured. */
async function viewer(c: Context<AppEnv>): Promise<Session | null> {
  const session = await c.var.auth.getSession(getCookie(c, 'tdoc_sid'));
  if (c.var.config.githubClientId && !session)
    throw new UnauthorizedError('sign_in_required', 'sign_in_required');
  return session;
}

/** Author identity for a write, or null in anonymous (local) mode. */
function authorOf(session: Session | null): Author | null {
  if (!session) return null;
  return {
    login: session.login,
    ...(session.avatar_url != null ? { avatar_url: session.avatar_url } : {}),
    ...(session.name != null ? { name: session.name } : {}),
  };
}

/** Can `session` mutate `record` (its author, or the owner)? */
function canMutate(
  record: { author?: Author | null } | undefined,
  session: Session | null,
  isOwner: boolean,
): boolean {
  if (isOwner) return true;
  const who = record?.author?.login;
  return !!(who && session?.login && who === session.login);
}

/** The author record to authorize against for a comment or reply id, or null. */
function findAuthorRecord(list: Comment[], id: string): { author?: Author | null } | null {
  const found = findHost(list, id);
  if (!found) return null;
  return found.reply ?? found.comment;
}

/** Build the comment routes. */
export function commentRoutes(): Hono<AppEnv> {
  const app = new Hono<AppEnv>();

  app.get('/api/comments', async (c) => {
    const slug = requireSlug(c.req.query('slug'));
    const list = await c.var.comments.list(slug, parseVersion(c.req.query('version')));
    return c.json(list);
  });

  app.post('/api/comments', async (c) => {
    const session = await viewer(c);
    const body = (await c.req.json().catch(() => ({}))) as Record<string, unknown>;
    const slug = requireSlug(body.slug);
    const text = body.text;
    if (typeof text !== 'string' || !text)
      throw new ValidationError('slug and text required', 'text_required');
    const version = Number(body.version) || 1;
    const res =
      typeof body.parent_id === 'string'
        ? await c.var.comments.reply(slug, {
            parentId: body.parent_id,
            author: authorOf(session),
            text,
            version,
          })
        : await c.var.comments.create(slug, {
            author: authorOf(session),
            text,
            anchor: (body.anchor as Comment['anchor']) ?? null,
            version,
          });
    return c.json(res.body, res.status as 200);
  });

  app.patch('/api/comments', async (c) => {
    const session = await viewer(c);
    const body = (await c.req.json().catch(() => ({}))) as Record<string, unknown>;
    const slug = requireSlug(body.slug);
    const id = body.id;
    const anchor = body.anchor;
    if (typeof id !== 'string' || !anchor)
      throw new ValidationError('slug, id, anchor required', 'anchor_required');
    const list = await c.var.comments.read(slug);
    const target = findAuthorRecord(list, id);
    if (!target) throw new ValidationError('not found', 'not_found');
    if (c.var.config.githubClientId && !canMutate(target, session, c.var.auth.isOwner(session))) {
      throw new ForbiddenError('not the author', 'not_author');
    }
    const version = Number(body.version) || 1;
    const res = await c.var.comments.reanchor(slug, {
      id,
      anchor: anchor as NonNullable<Comment['anchor']>,
      version,
      actor: session?.login ?? 'local',
    });
    return c.json(res.body, res.status as 200);
  });

  app.delete('/api/comments', async (c) => {
    const slug = requireSlug(c.req.query('slug'));
    if (c.req.query('all') === '1') return wipeComments(c, slug);
    const session = await viewer(c);
    const id = c.req.query('id');
    if (!id) throw new ValidationError('slug and id required', 'id_required');
    const list = await c.var.comments.read(slug);
    const target = findAuthorRecord(list, id);
    if (!target) throw new ValidationError('not found', 'not_found');
    if (c.var.config.githubClientId && !canMutate(target, session, c.var.auth.isOwner(session))) {
      throw new ForbiddenError('not the author', 'not_author');
    }
    const v = parseVersion(c.req.query('version'));
    const version = typeof v === 'number' && Number.isFinite(v) ? v : 999_999;
    const res = await c.var.comments.remove(slug, {
      id,
      version,
      actor: session?.login ?? 'local',
    });
    return c.json(res.body, res.status as 200);
  });

  app.post('/api/reactions', async (c) => {
    const session = await viewer(c);
    const body = (await c.req.json().catch(() => ({}))) as Record<string, unknown>;
    const slug = requireSlug(body.slug);
    const commentId = body.comment_id;
    const emoji = body.emoji;
    if (typeof commentId !== 'string' || typeof emoji !== 'string') {
      throw new ValidationError('slug, comment_id, emoji required', 'reaction_fields_required');
    }
    if (emoji.length === 0 || emoji.length > 8)
      throw new ValidationError('invalid emoji', 'invalid_emoji');
    const res = await c.var.comments.react(slug, {
      commentId,
      emoji,
      by: session?.login ?? 'anon',
      version: Number(body.version) || 1,
    });
    return c.json(res.body, res.status as 200);
  });

  app.post('/api/agent/reply', requireWriteAuth, agentReply);

  return app;
}

/** Admin: wipe all comments for a slug (write-token gated). */
async function wipeComments(c: Context<AppEnv>, slug: string): Promise<Response> {
  const token = bearer(c.req.header('authorization'));
  if (!token || !(await c.var.auth.isValidWriteToken(token))) throw new UnauthorizedError();
  const res = await c.var.comments.wipe(slug);
  return c.json(res.body, res.status as 200);
}

/** Build the event list for an agent reply (+ optional verdict state change). */
function agentReplyEvents(
  replyId: string,
  author: Author,
  text: string,
  verdict: AgentStatus | null,
  version: number,
  now: string,
): CommentEvent[] {
  const events: CommentEvent[] = [
    {
      kind: 'reply_added',
      at_version: version,
      at: now,
      reply: { id: replyId, author, text, agent_status: verdict },
    },
  ];
  if (verdict === 'applied') {
    events.push({
      kind: 'marked_applied',
      at_version: version,
      at: now,
      applied_in: version,
      by: 'tdoc-agent',
      agent_status: 'applied',
    });
  } else if (verdict === 'partial' || verdict === 'question') {
    events.push({
      kind: 'marked_open',
      at_version: version,
      at: now,
      by: 'tdoc-agent',
      agent_status: verdict,
    });
  }
  return events;
}

async function agentReply(c: Context<AppEnv>): Promise<Response> {
  const body = (await c.req.json().catch(() => ({}))) as Record<string, unknown>;
  const slug = requireSlug(body.slug);
  const parentId = body.parent_id;
  const text = body.text;
  if (typeof parentId !== 'string' || typeof text !== 'string' || !text) {
    throw new ValidationError('slug, parent_id, text required', 'agent_reply_fields_required');
  }
  const list = await c.var.comments.read(slug);
  const parent = list.find((cm) => cm.id === parentId);
  if (!parent) throw new ValidationError('parent not found', 'parent_not_found');

  const verdict =
    (['applied', 'partial', 'question'] as const).find((s) => s === body.status) ?? null;
  const version = Number(body.applied_in) || parent.created_in || 1;
  const now = new Date().toISOString();
  const replyId = `r_${Date.now()}_${rand(4)}`;
  const author: Author = {
    kind: 'agent',
    login: 'tdoc-agent',
    name: 'tdoc-agent',
    avatar_url: null,
  };

  const res = await c.var.comments.appendRaw(slug, {
    kind: 'raw_events',
    id: parentId,
    events: agentReplyEvents(replyId, author, text, verdict, version, now),
    responseBody: {
      id: replyId,
      parent_id: parentId,
      text,
      author,
      agent_status: verdict,
      created: now,
      reactions: {},
    },
  });
  return c.json(res.body, res.status as 200);
}
