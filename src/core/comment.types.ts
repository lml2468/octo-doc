/**
 * Domain types for the comment event-log model.
 *
 * Each comment is an append-only list of {@link CommentEvent}s. Reading a comment
 * "as of version N" folds events with `at_version <= N` into a {@link CommentSnapshot}.
 * Mutations append events; they never overwrite — so every version is a snapshot.
 */

/** A user or agent identity attached to a comment, reply, or reaction. */
export interface Author {
  readonly login: string;
  readonly name?: string;
  readonly avatar_url?: string | null;
  /** Present for the publish-time agent identity (`tdoc-agent`). */
  readonly kind?: 'agent';
}

/** The agent's verdict on a comment, rendered as an emoji at fold time. */
export type AgentStatus = 'applied' | 'partial' | 'question';

/** Where a comment is attached in the document. */
export type Anchor =
  | { kind: 'text'; text: string; context_before?: string; context_after?: string }
  | {
      kind: 'element';
      aid?: string;
      selector?: string;
      label?: string;
      fingerprint?: { tag?: string };
      fallback?: AnchorFallback;
    }
  | {
      kind: 'lost';
      reason?: string;
      label?: string;
      fingerprint?: { tag?: string };
      fallback?: AnchorFallback;
    };

/** Heuristic data used to re-bind a drifted element anchor at publish time. */
export interface AnchorFallback {
  nearestHeading?: { text: string };
}

/** Discriminated union of every event kind in a comment's log. */
export type CommentEvent =
  | {
      kind: 'created';
      eid?: string;
      at_version: number;
      at: string;
      anchor: Anchor | null;
      text: string;
    }
  | { kind: 'text_edited'; eid?: string; at_version: number; at: string; text: string }
  | {
      kind: 'anchor_changed';
      eid?: string;
      at_version: number;
      at: string;
      anchor: Anchor | null;
      reset_status?: boolean;
      by?: string;
    }
  | {
      kind: 'marked_applied';
      eid?: string;
      at_version: number;
      at: string;
      applied_in?: number;
      by?: string;
      agent_status?: AgentStatus;
    }
  | {
      kind: 'marked_open';
      eid?: string;
      at_version: number;
      at: string;
      by?: string;
      agent_status?: AgentStatus | null;
    }
  | { kind: 'deleted'; eid?: string; at_version: number; at: string; by?: string }
  | {
      kind: 'reaction_added';
      eid?: string;
      at_version: number;
      at: string;
      emoji: string;
      by: string;
    }
  | {
      kind: 'reaction_removed';
      eid?: string;
      at_version: number;
      at: string;
      emoji: string;
      by: string;
    }
  | { kind: 'reply_added'; eid?: string; at_version: number; at: string; reply: ReplySeed }
  | {
      kind: 'reply_text_edited';
      eid?: string;
      at_version: number;
      at: string;
      reply_id: string;
      text: string;
    }
  | {
      kind: 'reply_deleted';
      eid?: string;
      at_version: number;
      at: string;
      reply_id: string;
      by?: string;
    }
  | {
      kind: 'reply_reaction_added';
      eid?: string;
      at_version: number;
      at: string;
      reply_id: string;
      emoji: string;
      by: string;
    }
  | {
      kind: 'reply_reaction_removed';
      eid?: string;
      at_version: number;
      at: string;
      reply_id: string;
      emoji: string;
      by: string;
    };

/** The payload of a `reply_added` event. */
export interface ReplySeed {
  id: string;
  author: Author | null;
  text: string;
  agent_status?: AgentStatus | null;
}

/** A stored comment: stable identity plus its append-only event log. */
export interface Comment {
  id: string;
  author: Author | null;
  created: string;
  created_in: number;
  events: CommentEvent[];
  /** Legacy fields tolerated on read; migrated lazily into `events`. */
  version?: number;
  anchor?: Anchor | null;
  text?: string;
  status?: string;
  applied_in?: number;
  replies?: unknown[];
  reactions?: Record<string, string[]>;
}

/** Reactions keyed by emoji → list of logins that reacted. */
export type Reactions = Record<string, string[]>;

/** A reply folded to a point-in-time view. */
export interface ReplySnapshot {
  id: string;
  parent_id: string;
  author: Author | null;
  text: string;
  agent_status: AgentStatus | null;
  created: string;
  reactions: Reactions;
  deleted: boolean;
}

/** A comment folded to a point-in-time view (what the overlay/API consume). */
export interface CommentSnapshot {
  id: string;
  author: Author | null;
  created: string;
  created_in: number;
  version: number;
  anchor: Anchor | null;
  text: string;
  status: string;
  applied_in: number | undefined;
  replies: ReplySnapshot[];
  reactions: Reactions;
  deleted: boolean;
}

/** A commentable artifact discovered + stamped during {@link stampAids}. */
export interface StampedArtifact {
  aid: string;
  tag: string;
  head: string;
  heading: string | null;
}
