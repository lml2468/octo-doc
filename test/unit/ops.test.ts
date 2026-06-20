import { describe, it, expect } from 'vitest';
import { applyCommentOp } from '../../src/core/ops.js';
import { snapshotAt } from '../../src/core/comment-fold.js';
import type { Comment } from '../../src/core/comment.types.js';

/** Seed a list with one comment + one reply, returning ids. */
function seeded(): { list: Comment[]; commentId: string; replyId: string } {
  const list: Comment[] = [];
  applyCommentOp(list, {
    kind: 'create',
    id: 'c1',
    author: { login: 'a' },
    text: 'hi',
    version: 1,
  });
  applyCommentOp(list, {
    kind: 'reply',
    parent_id: 'c1',
    reply_id: 'r1',
    author: { login: 'b' },
    text: 'yo',
    version: 1,
  });
  return { list, commentId: 'c1', replyId: 'r1' };
}

describe('applyCommentOp — reactions', () => {
  it('toggles a reaction on a reply', () => {
    const { list, replyId } = seeded();
    applyCommentOp(list, { kind: 'react', comment_id: replyId, emoji: '🎉', by: 'x', version: 1 });
    let snap = snapshotAt(list[0]!, 1)!;
    expect(snap.replies[0]!.reactions['🎉']).toStrictEqual(['x']);
    applyCommentOp(list, { kind: 'react', comment_id: replyId, emoji: '🎉', by: 'x', version: 1 });
    snap = snapshotAt(list[0]!, 1)!;
    expect(snap.replies[0]!.reactions['🎉']).toBeUndefined();
  });

  it('404s a reaction on an unknown id', () => {
    const { list } = seeded();
    expect(
      applyCommentOp(list, { kind: 'react', comment_id: 'nope', emoji: '👍', by: 'x', version: 1 })
        .status,
    ).toBe(404);
  });
});

describe('applyCommentOp — delete', () => {
  it('soft-deletes a reply', () => {
    const { list, replyId } = seeded();
    expect(
      applyCommentOp(list, { kind: 'delete', id: replyId, version: 1, actor: { login: 'b' } })
        .status,
    ).toBe(200);
    expect(snapshotAt(list[0]!, 1)!.replies).toHaveLength(0);
  });

  it('404s deleting an unknown id', () => {
    const { list } = seeded();
    expect(
      applyCommentOp(list, { kind: 'delete', id: 'nope', version: 1, actor: { login: 'b' } })
        .status,
    ).toBe(404);
  });
});

describe('applyCommentOp — not-found branches', () => {
  it('reply to a missing parent 404s', () => {
    const list: Comment[] = [];
    expect(
      applyCommentOp(list, {
        kind: 'reply',
        parent_id: 'nope',
        reply_id: 'r',
        author: null,
        text: 't',
        version: 1,
      }).status,
    ).toBe(404);
  });

  it('patch_anchor on a missing comment 404s', () => {
    const list: Comment[] = [];
    expect(
      applyCommentOp(list, {
        kind: 'patch_anchor',
        id: 'nope',
        anchor: { kind: 'text', text: 'x' },
        reset_status: true,
        version: 1,
        actor: { login: 'a' },
      }).status,
    ).toBe(404);
  });

  it('raw_events on a missing comment 404s', () => {
    const list: Comment[] = [];
    expect(applyCommentOp(list, { kind: 'raw_events', id: 'nope', events: [] }).status).toBe(404);
  });

  it('wipe reports the deleted count and signals removal', () => {
    const { list } = seeded();
    const res = applyCommentOp(list, { kind: 'wipe' });
    expect(res.wipe).toBe(true);
    expect((res.body as { deleted: number }).deleted).toBe(1);
  });
});
