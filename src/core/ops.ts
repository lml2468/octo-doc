/**
 * Comment mutation operations.
 *
 * Each op is a serializable descriptor; {@link applyCommentOp} applies it to an
 * in-memory list and returns an {@link OpResult}. This is the single place
 * comment mutation logic lives — the service layer serializes calls per slug.
 * Pure w.r.t. I/O: it only mutates the passed `list`.
 */
import type {
  Anchor,
  Author,
  Comment,
  CommentEvent,
  CommentSnapshot,
  Reactions,
  ReplySeed,
} from './comment.types.js';
import {
  appendEvent,
  backfillEids,
  ensureEventLog,
  ensureMigrated,
  compactComments,
} from './comment-events.js';
import { snapshotAt } from './comment-fold.js';
import { reconcileAnchors } from './reconcile.js';
import type { StampedArtifact } from './comment.types.js';

/** Discriminated union of every comment operation. */
export type CommentOp =
  | {
      kind: 'create';
      id: string;
      author: Author | null;
      text: string;
      anchor?: Anchor | null;
      version: number;
      at?: string;
    }
  | {
      kind: 'reply';
      parent_id: string;
      reply_id: string;
      author: Author | null;
      text: string;
      version: number;
      at?: string;
    }
  | {
      kind: 'patch_anchor';
      id: string;
      anchor: Anchor;
      reset_status: boolean;
      version: number;
      actor: { login: string };
      at?: string;
    }
  | { kind: 'react'; comment_id: string; emoji: string; by: string; version: number; at?: string }
  | { kind: 'delete'; id: string; version: number; actor: { login: string }; at?: string }
  | { kind: 'raw_events'; id: string; events: CommentEvent[]; responseBody?: unknown; at?: string }
  | { kind: 'wipe'; at?: string }
  | {
      kind: 'publish_merge';
      localComments: Comment[];
      aids: StampedArtifact[];
      version: number;
      at?: string;
    };

/** Result of applying an op: HTTP-shaped status + body, plus an internal wipe flag. */
export interface OpResult {
  status: number;
  body: unknown;
  /** Signals the caller to delete the slug's comment key entirely. */
  wipe?: boolean;
}

/**
 * Locate a comment or reply by id. Returns the host comment and, when the id
 * names a reply, that reply's seed (its author/text). The single traversal used
 * by reactions, deletes, and route-level authorization.
 */
export function findHost(
  list: Comment[],
  id: string,
): { comment: Comment; reply: ReplySeed | null } | null {
  const top = list.find((c) => c.id === id);
  if (top) return { comment: top, reply: null };
  for (const c of list) {
    const added = (c.events ?? []).find((e) => e.kind === 'reply_added' && e.reply?.id === id);
    if (added && added.kind === 'reply_added') return { comment: c, reply: added.reply };
  }
  return null;
}

function reactionsFor(reactions: Reactions, by: string, emoji: string): boolean {
  return (reactions[emoji] ?? []).includes(by);
}

function opCreate(
  list: Comment[],
  op: Extract<CommentOp, { kind: 'create' }>,
  now: string,
): OpResult {
  const entry: Comment = {
    id: op.id,
    author: op.author,
    created: now,
    created_in: op.version,
    events: [
      {
        kind: 'created',
        at_version: op.version,
        at: now,
        anchor: op.anchor ?? null,
        text: op.text,
      },
    ],
  };
  backfillEids(entry.events);
  list.push(entry);
  return { status: 200, body: snapshotAt(entry, op.version) };
}

function opReply(
  list: Comment[],
  op: Extract<CommentOp, { kind: 'reply' }>,
  now: string,
): OpResult {
  const parent = list.find((c) => c.id === op.parent_id);
  if (!parent) return { status: 404, body: { error: 'parent_not_found' } };
  appendEvent(parent, {
    kind: 'reply_added',
    at_version: op.version,
    at: now,
    reply: { id: op.reply_id, author: op.author, text: op.text, agent_status: null },
  });
  return {
    status: 200,
    body: {
      id: op.reply_id,
      parent_id: op.parent_id,
      author: op.author,
      text: op.text,
      created: now,
      version: op.version,
    },
  };
}

function opPatchAnchor(
  list: Comment[],
  op: Extract<CommentOp, { kind: 'patch_anchor' }>,
  now: string,
): OpResult {
  const target = list.find((c) => c.id === op.id);
  if (!target) return { status: 404, body: { error: 'not_found' } };
  appendEvent(target, {
    kind: 'anchor_changed',
    at_version: op.version,
    at: now,
    reset_status: op.reset_status,
    anchor: op.anchor,
    by: op.actor.login,
  });
  return { status: 200, body: snapshotAt(target, op.version) };
}

/** Read the reactions map for a target (top-level comment or a specific reply). */
function reactionsOf(snap: CommentSnapshot, replyId: string | null): Reactions {
  if (!replyId) return snap.reactions;
  return snap.replies.find((r) => r.id === replyId)?.reactions ?? {};
}

function opReact(
  list: Comment[],
  op: Extract<CommentOp, { kind: 'react' }>,
  now: string,
): OpResult {
  const found = findHost(list, op.comment_id);
  if (!found) return { status: 404, body: { error: 'not_found' } };
  const replyId = found.reply ? op.comment_id : null;
  const host = found.comment;
  const snap = snapshotAt(host, op.version);
  if (!snap) return { status: 404, body: { error: 'not_visible_at_version' } };

  const had = reactionsFor(reactionsOf(snap, replyId), op.by, op.emoji);
  const base = { at_version: op.version, at: now, emoji: op.emoji, by: op.by };
  appendEvent(
    host,
    replyId
      ? {
          ...base,
          kind: had ? 'reply_reaction_removed' : 'reply_reaction_added',
          reply_id: replyId,
        }
      : { ...base, kind: had ? 'reaction_removed' : 'reaction_added' },
  );

  const fresh = snapshotAt(host, op.version)!;
  return { status: 200, body: { ok: true, reactions: reactionsOf(fresh, replyId) } };
}

function opDelete(
  list: Comment[],
  op: Extract<CommentOp, { kind: 'delete' }>,
  now: string,
): OpResult {
  const found = findHost(list, op.id);
  if (!found) return { status: 404, body: { error: 'not_found' } };
  appendEvent(
    found.comment,
    found.reply
      ? {
          kind: 'reply_deleted',
          at_version: op.version,
          at: now,
          reply_id: op.id,
          by: op.actor.login,
        }
      : { kind: 'deleted', at_version: op.version, at: now, by: op.actor.login },
  );
  return { status: 200, body: { ok: true } };
}

function opRawEvents(list: Comment[], op: Extract<CommentOp, { kind: 'raw_events' }>): OpResult {
  const target = list.find((c) => c.id === op.id);
  if (!target) return { status: 404, body: { error: 'not_found' } };
  for (const ev of op.events) appendEvent(target, ev);
  return { status: 200, body: op.responseBody ?? { ok: true } };
}

function opPublishMerge(
  list: Comment[],
  op: Extract<CommentOp, { kind: 'publish_merge' }>,
): OpResult {
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

/**
 * Apply one comment operation to `list` (mutating it) and return the result.
 *
 * @param list - the slug's in-memory comment list (migrated in place)
 * @param op - the operation descriptor
 */
export function applyCommentOp(list: Comment[], op: CommentOp): OpResult {
  ensureMigrated(list);
  const now = op.at ?? new Date().toISOString();
  switch (op.kind) {
    case 'create':
      return opCreate(list, op, now);
    case 'reply':
      return opReply(list, op, now);
    case 'patch_anchor':
      return opPatchAnchor(list, op, now);
    case 'react':
      return opReact(list, op, now);
    case 'delete':
      return opDelete(list, op, now);
    case 'raw_events':
      return opRawEvents(list, op);
    case 'wipe':
      return { status: 200, body: { ok: true, deleted: list.length }, wipe: true };
    case 'publish_merge':
      return opPublishMerge(list, op);
  }
}
