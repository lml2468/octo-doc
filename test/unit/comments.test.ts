import { describe, it, expect } from 'vitest';
import { applyCommentOp } from '../../src/core/ops.js';
import { snapshotAt, snapshotList, historyList } from '../../src/core/comment-fold.js';
import { dedupEvents } from '../../src/core/comment-events.js';
import type { Comment, CommentEvent } from '../../src/core/comment.types.js';

describe('comment fold', () => {
  it('folds create → reply → react, then toggles the reaction off', () => {
    const list: Comment[] = [];
    applyCommentOp(list, {
      kind: 'create',
      id: 'c1',
      author: { login: 'a' },
      text: 'hello',
      version: 1,
      at: '2026-01-01T00:00:00Z',
    });
    applyCommentOp(list, {
      kind: 'reply',
      parent_id: 'c1',
      reply_id: 'r1',
      author: { login: 'b' },
      text: 'hi back',
      version: 1,
      at: '2026-01-01T00:01:00Z',
    });
    applyCommentOp(list, { kind: 'react', comment_id: 'c1', emoji: '👍', by: 'b', version: 1 });

    const snap = snapshotAt(list[0]!, 1)!;
    expect(snap.text).toBe('hello');
    expect(snap.replies).toHaveLength(1);
    expect(snap.replies[0]!.text).toBe('hi back');
    expect(snap.reactions['👍']).toStrictEqual(['b']);

    applyCommentOp(list, { kind: 'react', comment_id: 'c1', emoji: '👍', by: 'b', version: 1 });
    expect(snapshotAt(list[0]!, 1)!.reactions['👍']).toBeUndefined();
  });

  it('soft-deletes: hidden from snapshotList', () => {
    const list: Comment[] = [];
    applyCommentOp(list, {
      kind: 'create',
      id: 'c1',
      author: { login: 'a' },
      text: 'x',
      version: 1,
    });
    applyCommentOp(list, { kind: 'delete', id: 'c1', version: 1, actor: { login: 'a' } });
    expect(snapshotList(list, 1)).toHaveLength(0);
  });

  it('version-scopes: a v3 comment is invisible at v2', () => {
    const list: Comment[] = [];
    applyCommentOp(list, {
      kind: 'create',
      id: 'c1',
      author: { login: 'a' },
      text: 'late',
      version: 3,
    });
    expect(snapshotAt(list[0]!, 2)).toBeNull();
    expect(snapshotList(list, 2)).toHaveLength(0);
    expect(snapshotList(list, 3)).toHaveLength(1);
    expect(historyList(list)).toHaveLength(1);
  });

  it('agent verdict renders an emoji synthetically', () => {
    const list: Comment[] = [];
    applyCommentOp(list, {
      kind: 'create',
      id: 'c1',
      author: { login: 'a' },
      text: 'x',
      version: 1,
    });
    applyCommentOp(list, {
      kind: 'raw_events',
      id: 'c1',
      events: [
        {
          kind: 'marked_applied',
          at_version: 1,
          at: '2026-01-01',
          applied_in: 1,
          by: 'tdoc-agent',
          agent_status: 'applied',
        },
      ],
    });
    const snap = snapshotAt(list[0]!, 1)!;
    expect(snap.status).toBe('applied');
    expect(snap.reactions['✅']).toContain('tdoc-agent');
  });
});

describe('dedupEvents convergence', () => {
  it('collapses idempotent reaction events sharing an eid', () => {
    const events: CommentEvent[] = [
      {
        kind: 'reaction_added',
        emoji: '👍',
        by: 'x',
        at_version: 1,
        at: 'a',
        eid: 'reaction_added:👍:x',
      },
      {
        kind: 'reaction_added',
        emoji: '👍',
        by: 'x',
        at_version: 1,
        at: 'b',
        eid: 'reaction_added:👍:x',
      },
    ];
    expect(dedupEvents(events)).toHaveLength(1);
  });

  it('is order-independent: backdated events sort by at_version', () => {
    const list: Comment[] = [];
    applyCommentOp(list, { kind: 'create', id: 'c1', author: null, text: 'v1', version: 1 });
    // Append a text edit stamped at v1 AFTER a v2 edit — fold must honor version order.
    applyCommentOp(list, {
      kind: 'raw_events',
      id: 'c1',
      events: [{ kind: 'text_edited', at_version: 2, at: 'b', text: 'v2' }],
    });
    applyCommentOp(list, {
      kind: 'raw_events',
      id: 'c1',
      events: [{ kind: 'text_edited', at_version: 1, at: 'a', text: 'v1-late' }],
    });
    expect(snapshotAt(list[0]!, 1)!.text).toBe('v1-late');
    expect(snapshotAt(list[0]!, 2)!.text).toBe('v2');
  });
});

describe('publish_merge', () => {
  it('adds new comments by id and never overwrites existing ones', () => {
    const list: Comment[] = [];
    applyCommentOp(list, {
      kind: 'create',
      id: 'c1',
      author: { login: 'a' },
      text: 'server',
      version: 1,
      at: '2026-01-01T00:00:00Z',
    });
    const localComments: Comment[] = [
      {
        id: 'c1',
        author: null,
        created: '2026-01-01',
        created_in: 1,
        events: [
          { kind: 'created', at_version: 1, at: '2026-01-01', anchor: null, text: 'LOCAL clobber' },
        ],
      },
      {
        id: 'c2',
        author: null,
        created: '2026-01-01',
        created_in: 1,
        events: [{ kind: 'created', at_version: 1, at: '2026-01-01', anchor: null, text: 'new' }],
      },
    ];
    const res = applyCommentOp(list, {
      kind: 'publish_merge',
      localComments,
      aids: [],
      version: 1,
    });
    expect((res.body as { mergedComments: number }).mergedComments).toBe(1);
    expect(snapshotAt(list.find((c) => c.id === 'c1')!, 1)!.text).toBe('server');
  });
});
