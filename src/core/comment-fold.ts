/**
 * Folding a comment's event log into a point-in-time {@link CommentSnapshot}.
 *
 * The fundamental rule: reading "as of version N" replays events with
 * `at_version <= N`. Events are deduped (convergence) and stable-sorted by
 * `at_version` so the fold is independent of physical write order.
 */
import type {
  AgentStatus,
  Comment,
  CommentEvent,
  CommentSnapshot,
  Reactions,
  ReplySnapshot,
} from './comment.types.js';
import { dedupEvents, ensureEventLog } from './comment-events.js';

/** Agent verdict → emoji rendered synthetically on the parent at fold time. */
const AGENT_STATUS_EMOJI: Record<AgentStatus, string> = {
  applied: '✅',
  partial: '🟡',
  question: '❓',
};

function isFiniteVersion(v: number): boolean {
  return Number.isFinite(v) && v >= 0;
}

/** Mutable accumulator threaded through the fold. */
interface FoldState {
  snap: CommentSnapshot;
  replyOrder: string[];
  replyById: Map<string, ReplySnapshot>;
  agentVerdict: AgentStatus | null;
}

/** Toggle a login into/out of a reactions map for one emoji. */
function applyReaction(reactions: Reactions, emoji: string, by: string, add: boolean): void {
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

/** Apply a content event (text/anchor) to the snapshot. */
function applyContentEvent(st: FoldState, e: CommentEvent): boolean {
  const { snap } = st;
  switch (e.kind) {
    case 'created':
      snap.anchor = e.anchor ?? null;
      snap.text = e.text || '';
      return true;
    case 'text_edited':
      snap.text = e.text || '';
      return true;
    case 'anchor_changed':
      snap.anchor = e.anchor ?? null;
      if (e.reset_status) {
        snap.status = 'open';
        snap.applied_in = undefined;
      }
      return true;
    default:
      return false;
  }
}

/** Apply a status event (applied/open/deleted) to the snapshot. */
function applyStatusEvent(st: FoldState, e: CommentEvent): boolean {
  const { snap } = st;
  switch (e.kind) {
    case 'marked_applied':
      snap.status = 'applied';
      snap.applied_in = e.applied_in ?? e.at_version;
      st.agentVerdict = e.agent_status ?? 'applied';
      return true;
    case 'marked_open':
      snap.status = 'open';
      snap.applied_in = undefined;
      st.agentVerdict = e.agent_status ?? null;
      return true;
    case 'deleted':
      snap.deleted = true;
      return true;
    default:
      return false;
  }
}

/** Apply a parent-level reaction event to the snapshot. */
function applyParentReaction(st: FoldState, e: CommentEvent): boolean {
  if (e.kind === 'reaction_added' && e.emoji && e.by) {
    applyReaction(st.snap.reactions, e.emoji, e.by, true);
    return true;
  }
  if (e.kind === 'reaction_removed' && e.emoji && e.by) {
    applyReaction(st.snap.reactions, e.emoji, e.by, false);
    return true;
  }
  return false;
}

/** Apply any event to the fold state, routing by category. */
function applyCommentEvent(st: FoldState, e: CommentEvent): void {
  if (applyContentEvent(st, e)) return;
  if (applyStatusEvent(st, e)) return;
  if (applyParentReaction(st, e)) return;
  applyReplyEvent(st, e);
}

/** Add a reply from a `reply_added` event. */
function addReply(st: FoldState, e: Extract<CommentEvent, { kind: 'reply_added' }>): void {
  if (!e.reply?.id) return;
  st.replyOrder.push(e.reply.id);
  st.replyById.set(e.reply.id, {
    id: e.reply.id,
    parent_id: st.snap.id,
    author: e.reply.author ?? null,
    text: e.reply.text || '',
    agent_status: e.reply.agent_status ?? null,
    created: e.at,
    reactions: {},
    deleted: false,
  });
}

/** Apply a reply reaction add/remove event. */
function applyReplyReaction(
  st: FoldState,
  e: Extract<CommentEvent, { kind: 'reply_reaction_added' | 'reply_reaction_removed' }>,
): void {
  const r = st.replyById.get(e.reply_id);
  if (r && e.emoji && e.by)
    applyReaction(r.reactions, e.emoji, e.by, e.kind === 'reply_reaction_added');
}

/** Apply a reply-level event to the fold state. */
function applyReplyEvent(st: FoldState, e: CommentEvent): void {
  switch (e.kind) {
    case 'reply_added':
      return addReply(st, e);
    case 'reply_text_edited': {
      const r = st.replyById.get(e.reply_id);
      if (r) r.text = e.text || '';
      return;
    }
    case 'reply_deleted': {
      const r = st.replyById.get(e.reply_id);
      if (r) r.deleted = true;
      return;
    }
    case 'reply_reaction_added':
    case 'reply_reaction_removed':
      return applyReplyReaction(st, e);
    default:
      return;
  }
}

/**
 * Order events deterministically: dedup, then sort by `at_version`. Relies on
 * `Array.prototype.sort` being stable (ES2019+), so equal-version events keep
 * their original relative order without an explicit index tiebreak.
 */
function orderedEvents(events: CommentEvent[]): CommentEvent[] {
  // dedupEvents returns a fresh array, so sorting it in place is safe.
  return dedupEvents(events).sort((a, b) => (a.at_version || 0) - (b.at_version || 0));
}

/** Fresh snapshot scaffold for a comment. */
function emptySnapshot(c: Comment): CommentSnapshot {
  return {
    id: c.id,
    author: c.author,
    created: c.created,
    created_in: c.created_in,
    version: c.created_in,
    anchor: null,
    text: '',
    status: 'open',
    applied_in: undefined,
    replies: [],
    reactions: {},
    deleted: false,
  };
}

/** Replay all events up to `at` into a fresh fold state. */
function replay(c: Comment, at: number): FoldState {
  const st: FoldState = {
    snap: emptySnapshot(c),
    replyOrder: [],
    replyById: new Map(),
    agentVerdict: null,
  };
  for (const e of orderedEvents(c.events)) {
    const v = e.at_version;
    if (isFiniteVersion(v) && v <= at) applyCommentEvent(st, e);
  }
  return st;
}

/** Finalize a fold: render the synthetic agent emoji and collect live replies. */
function finalize(st: FoldState): CommentSnapshot {
  const verdict = st.agentVerdict;
  if (verdict && AGENT_STATUS_EMOJI[verdict]) {
    applyReaction(st.snap.reactions, AGENT_STATUS_EMOJI[verdict], 'tdoc-agent', true);
  }
  st.snap.replies = st.replyOrder
    .map((id) => st.replyById.get(id))
    .filter((r): r is ReplySnapshot => !!r && !r.deleted);
  return st.snap;
}

/**
 * Fold a comment into its snapshot as of version `v`.
 *
 * @param c - the stored comment (migrated in place if legacy)
 * @param v - the version to fold to; `Infinity` for the latest state
 * @returns the folded snapshot, or `null` if the comment did not exist at `v`
 */
export function snapshotAt(c: Comment, v: number): CommentSnapshot | null {
  ensureEventLog(c);
  if (!Array.isArray(c.events) || c.events.length === 0) return null;
  const at = isFiniteVersion(v) ? v : Infinity;
  if (c.created_in != null && c.created_in > at) return null;
  return finalize(replay(c, at));
}

/** Fold a list at version `v`, returning only alive (non-deleted) snapshots. */
export function snapshotList(list: Comment[], v: number): CommentSnapshot[] {
  const out: CommentSnapshot[] = [];
  for (const c of list) {
    const s = snapshotAt(c, v);
    if (s && !s.deleted) out.push(s);
  }
  return out;
}

/**
 * Fold every comment across all versions at its richest state (used by pull so
 * comments anchored to an older version are never dropped). Deleted comments are
 * still excluded.
 */
export function historyList(list: Comment[]): CommentSnapshot[] {
  return snapshotList(list, Infinity);
}
