/**
 * CommentService — the serialized owner of per-slug comment mutations.
 *
 * All writes for a slug run under a per-slug mutex, making read → apply → write
 * atomic (the role the Cloudflare Durable Object played). Reads fold the stored
 * event log. The service layer is the only place routes touch comment logic.
 */
import type { MetadataStore } from '../storage/types.js';
import type { Anchor, Author, Comment, CommentSnapshot } from '../core/comment.types.js';
import { applyCommentOp, type CommentOp, type OpResult } from '../core/ops.js';
import { ensureMigrated, safeParseList } from '../core/comment-events.js';
import { snapshotList, historyList } from '../core/comment-fold.js';
import { makeKeyedMutex, type LockFn } from '../core/mutex.js';
import { rand } from './ids.js';

/** A folded comment view request: a specific version, or the full history. */
export type VersionScope = number | 'all';

/** Result of a serialized comment mutation (HTTP-shaped). */
export interface MutationResult {
  status: number;
  body: unknown;
}

export class CommentService {
  private readonly lock: LockFn = makeKeyedMutex();

  constructor(private readonly meta: MetadataStore) {}

  /** Fold a slug's comments to a version snapshot, or the full cross-version history. */
  async list(slug: string, scope: VersionScope): Promise<CommentSnapshot[]> {
    const list = await this.read(slug);
    return scope === 'all' ? historyList(list) : snapshotList(list, scope);
  }

  /** Read + migrate the raw comment list for a slug (callers fold it). */
  async read(slug: string): Promise<Comment[]> {
    const list = safeParseList(await this.meta.getComments(slug));
    ensureMigrated(list);
    return list;
  }

  /** Create a top-level comment. */
  create(
    slug: string,
    input: { author: Author | null; text: string; anchor?: Anchor | null; version: number },
  ): Promise<MutationResult> {
    return this.mutate(slug, {
      kind: 'create',
      id: `c_${Date.now()}_${rand(4)}`,
      author: input.author,
      text: input.text,
      anchor: input.anchor ?? null,
      version: input.version,
    });
  }

  /** Add a reply to a parent comment. */
  reply(
    slug: string,
    input: { parentId: string; author: Author | null; text: string; version: number },
  ): Promise<MutationResult> {
    return this.mutate(slug, {
      kind: 'reply',
      parent_id: input.parentId,
      reply_id: `r_${Date.now()}_${rand(4)}`,
      author: input.author,
      text: input.text,
      version: input.version,
    });
  }

  /** Toggle an emoji reaction on a comment or reply. */
  react(
    slug: string,
    input: { commentId: string; emoji: string; by: string; version: number },
  ): Promise<MutationResult> {
    return this.mutate(slug, {
      kind: 'react',
      comment_id: input.commentId,
      emoji: input.emoji,
      by: input.by,
      version: input.version,
    });
  }

  /** Re-anchor a comment (resets its agent verdict). */
  reanchor(
    slug: string,
    input: { id: string; anchor: Anchor; version: number; actor: string },
  ): Promise<MutationResult> {
    return this.mutate(slug, {
      kind: 'patch_anchor',
      id: input.id,
      anchor: input.anchor,
      reset_status: true,
      version: input.version,
      actor: { login: input.actor },
    });
  }

  /** Soft-delete a comment or reply at a version. */
  remove(
    slug: string,
    input: { id: string; version: number; actor: string },
  ): Promise<MutationResult> {
    return this.mutate(slug, {
      kind: 'delete',
      id: input.id,
      version: input.version,
      actor: { login: input.actor },
    });
  }

  /** Append pre-built events to a comment (agent reply path). */
  appendRaw(slug: string, op: Extract<CommentOp, { kind: 'raw_events' }>): Promise<MutationResult> {
    return this.mutate(slug, op);
  }

  /** Wipe all comments for a slug. */
  wipe(slug: string): Promise<MutationResult> {
    return this.mutate(slug, { kind: 'wipe' });
  }

  /** Publish-time non-destructive merge + anchor reconcile. */
  publishMerge(
    slug: string,
    op: Omit<Extract<CommentOp, { kind: 'publish_merge' }>, 'kind'>,
  ): Promise<MutationResult> {
    return this.mutate(slug, { kind: 'publish_merge', ...op });
  }

  /** Run a comment op under the per-slug lock, persisting on success. */
  private mutate(slug: string, op: CommentOp): Promise<MutationResult> {
    return this.lock(slug, async () => {
      const list = safeParseList(await this.meta.getComments(slug));
      const res: OpResult = applyCommentOp(list, op);
      if (res.status === 200) {
        if (res.wipe) await this.meta.deleteComments(slug);
        else await this.meta.putComments(slug, list);
      }
      return { status: res.status, body: res.body };
    });
  }
}
