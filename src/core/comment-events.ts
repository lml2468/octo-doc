/**
 * Comment event-log primitives: stable event ids, dedup/convergence, and lazy
 * migration of legacy flat comments into the event-log shape.
 *
 * Ported from the upstream Worker. The fold ({@link ./comment-fold.ts}) and the
 * mutation ops build on these. Kept dependency-free and pure.
 */
import type { Comment, CommentEvent, ReplySeed } from './comment.types.js';

/**
 * Monotonic counter for one-shot event ids. The upstream Worker used
 * `Math.random()`; a counter serves the same uniqueness role without a PRNG and
 * keeps ids stable within a process. Idempotent events keep deterministic ids,
 * so the dedup contract is unchanged.
 */
let eidCounter = 0;

/**
 * Compute a stable event id. Naturally-idempotent events (reactions, state
 * flags) get a deterministic id so a concurrent duplicate collapses to one;
 * one-shot events (created, reply, edits) get a unique id.
 */
export function eventEid(e: CommentEvent): string {
  switch (e.kind) {
    case 'reaction_added':
    case 'reaction_removed':
      return `${e.kind}:${e.emoji}:${e.by}`;
    case 'marked_applied':
    case 'marked_open':
    case 'deleted':
      return `${e.kind}:${e.at_version}`;
    default: {
      const nonce = (eidCounter++).toString(36);
      const hi = Math.floor(
        (typeof performance !== 'undefined' ? performance.now() : 0) * 1000,
      ).toString(36);
      return `${e.kind}:${e.at}:${nonce}_${hi}`;
    }
  }
}

/** Backfill missing `eid` on each event. Returns true if anything changed. */
export function backfillEids(events: CommentEvent[] | undefined): boolean {
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

/** Append an event to a comment, stamping an eid if absent. */
export function appendEvent(c: Comment, event: CommentEvent): void {
  if (!Array.isArray(c.events)) c.events = [];
  if (!event.eid) event.eid = eventEid(event);
  c.events.push(event);
}

/**
 * Collapse events sharing an eid, keeping the last occurrence. This is the
 * convergence point: merging two concurrently-written logs and folding through
 * `dedupEvents` yields the same result regardless of which write landed last.
 */
export function dedupEvents(events: CommentEvent[] | undefined): CommentEvent[] {
  if (!Array.isArray(events)) return [];
  const lastByEid = new Map<string, CommentEvent>();
  for (const e of events) if (e?.eid) lastByEid.set(e.eid, e);
  const out: CommentEvent[] = [];
  const emitted = new Set<string>();
  for (const e of events) {
    if (!e) continue;
    if (!e.eid) {
      out.push(e);
      continue;
    }
    if (emitted.has(e.eid)) continue;
    emitted.add(e.eid);
    out.push(lastByEid.get(e.eid)!);
  }
  return out;
}

/** Build a fresh `created`-and-friends event list from a legacy flat comment. */
function legacyToEvents(c: Comment): CommentEvent[] {
  const events: CommentEvent[] = [];
  const at = c.created || new Date().toISOString();
  const v = Number(c.version) || 1;
  events.push({ kind: 'created', at_version: v, at, anchor: c.anchor ?? null, text: c.text ?? '' });
  if (c.status === 'applied') {
    events.push({
      kind: 'marked_applied',
      at_version: Number(c.applied_in) || v,
      at,
      applied_in: Number(c.applied_in) || v,
      by: 'tdoc-agent',
      agent_status: 'applied',
    });
  }
  appendLegacyReactions(events, c.reactions, v, at);
  appendLegacyReplies(events, c.replies, v, at);
  return events;
}

/** Fold a legacy reactions map into `reaction_added` events. */
function appendLegacyReactions(
  events: CommentEvent[],
  reactions: Record<string, string[]> | undefined,
  v: number,
  at: string,
): void {
  for (const ev of legacyReactionEvents(reactions, v, at)) {
    events.push({ ...ev, kind: 'reaction_added' });
  }
}

/** A legacy flat reply record (pre event-log). */
interface LegacyReply {
  id: string;
  author?: ReplySeed['author'];
  text?: string;
  agent_status?: ReplySeed['agent_status'];
  version?: number;
  created?: string;
  reactions?: Record<string, string[]>;
}

/** Fold one legacy reply (and its reactions) into reply events. */
function appendLegacyReply(events: CommentEvent[], r: LegacyReply, v: number, at: string): void {
  const rv = Number(r.version) || v;
  const when = r.created || at;
  events.push({
    kind: 'reply_added',
    at_version: rv,
    at: when,
    reply: {
      id: r.id,
      author: r.author ?? null,
      text: r.text ?? '',
      agent_status: r.agent_status ?? null,
    },
  });
  for (const ev of legacyReactionEvents(r.reactions, rv, when)) {
    events.push({ ...ev, kind: 'reply_reaction_added', reply_id: r.id });
  }
}

/** Flatten a legacy reactions map into (emoji, by) pairs at a version/time. */
function legacyReactionEvents(
  reactions: Record<string, string[]> | undefined,
  v: number,
  at: string,
): { at_version: number; at: string; emoji: string; by: string }[] {
  if (!reactions || typeof reactions !== 'object') return [];
  const out: { at_version: number; at: string; emoji: string; by: string }[] = [];
  for (const emoji of Object.keys(reactions)) {
    for (const by of reactions[emoji] ?? []) out.push({ at_version: v, at, emoji, by });
  }
  return out;
}

/** Fold legacy replies into reply events. */
function appendLegacyReplies(
  events: CommentEvent[],
  replies: unknown[] | undefined,
  v: number,
  at: string,
): void {
  if (!Array.isArray(replies)) return;
  for (const raw of replies) appendLegacyReply(events, raw as LegacyReply, v, at);
}

/**
 * Ensure a comment has an `events[]` log, migrating a legacy flat record in
 * place if needed. Returns true if the record was migrated or had eids backfilled.
 */
export function ensureEventLog(c: Comment): boolean {
  if (Array.isArray(c.events)) return backfillEids(c.events);
  if (!c.id) return false;
  const events = legacyToEvents(c);
  backfillEids(events);
  c.events = events;
  const first = events[0];
  c.created_in = first?.at_version || Number(c.version) || 1;
  c.author = c.author ?? null;
  c.created = c.created || first?.at || new Date().toISOString();
  return true;
}

/** Migrate every comment in a list to the event-log shape. Returns true if any changed. */
export function ensureMigrated(list: Comment[]): boolean {
  let dirty = false;
  for (const c of list) if (ensureEventLog(c)) dirty = true;
  return dirty;
}

/**
 * Permanently collapse each comment's log to its deduped form. Called at publish
 * time so stored values stop growing unboundedly (superseded toggles, duplicate
 * eids). A no-op for correctness — the read-time fold already dedups.
 */
export function compactComments(comments: Comment[]): boolean {
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

/** Parse a stored comments value defensively; corrupt input folds to `[]`. */
export function safeParseList(raw: unknown): Comment[] {
  if (!raw) return [];
  try {
    const v = typeof raw === 'string' ? (JSON.parse(raw) as unknown) : raw;
    return Array.isArray(v) ? (v as Comment[]) : [];
  } catch {
    return [];
  }
}
