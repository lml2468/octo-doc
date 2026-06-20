import { describe, it, expect } from 'vitest';
import { reconcileAnchors } from '../../src/core/reconcile.js';
import { snapshotAt } from '../../src/core/comment-fold.js';
import {
  ensureEventLog,
  ensureMigrated,
  compactComments,
  safeParseList,
} from '../../src/core/comment-events.js';
import type { Comment, StampedArtifact } from '../../src/core/comment.types.js';

const aid = (a: string, tag: string, heading: string | null = null): StampedArtifact => ({
  aid: a,
  tag,
  head: '',
  heading,
});

/** A comment anchored to an element aid. */
function elementComment(
  id: string,
  anchorAid: string,
  opts: { label?: string; heading?: string } = {},
): Comment {
  return {
    id,
    author: { login: 'a' },
    created: '2026-01-01T00:00:00Z',
    created_in: 1,
    events: [
      {
        kind: 'created',
        at_version: 1,
        at: '2026-01-01T00:00:00Z',
        text: 'c',
        anchor: {
          kind: 'element',
          aid: anchorAid,
          selector: `[data-tdoc-aid="${anchorAid}"]`,
          ...(opts.label ? { label: opts.label } : {}),
          ...(opts.heading ? { fallback: { nearestHeading: { text: opts.heading } } } : {}),
        },
      },
    ],
  };
}

describe('reconcileAnchors', () => {
  it('leaves an anchor untouched when its aid still resolves', () => {
    const list = [elementComment('c1', 'AID1')];
    reconcileAnchors(list, [aid('AID1', 'svg')], 2);
    // No new event appended (only the original created event).
    expect(list[0]!.events).toHaveLength(1);
  });

  it('rebinds to the sole tag match when the old aid is gone', () => {
    const list = [elementComment('c1', 'OLD', { label: 'svg' })];
    reconcileAnchors(list, [aid('NEW', 'svg')], 2);
    expect(snapshotAt(list[0]!, 2)!.anchor).toMatchObject({ kind: 'element', aid: 'NEW' });
    // Older version still resolves to the original anchor.
    expect(snapshotAt(list[0]!, 1)!.anchor).toMatchObject({ aid: 'OLD' });
  });

  it('disambiguates by nearest heading when multiple tags match', () => {
    const list = [elementComment('c1', 'OLD', { label: 'svg', heading: 'Section B' })];
    reconcileAnchors(list, [aid('A', 'svg', 'Section A'), aid('B', 'svg', 'Section B')], 2);
    expect(snapshotAt(list[0]!, 2)!.anchor).toMatchObject({ aid: 'B' });
  });

  it('marks lost when no confident candidate exists', () => {
    const list = [elementComment('c1', 'OLD', { label: 'svg' })];
    reconcileAnchors(list, [aid('A', 'svg'), aid('B', 'svg')], 2); // ambiguous
    expect(snapshotAt(list[0]!, 2)!.anchor).toMatchObject({ kind: 'lost' });
  });

  it('does not re-append a lost event once already lost', () => {
    const list = [elementComment('c1', 'OLD', { label: 'svg' })];
    reconcileAnchors(list, [aid('A', 'svg'), aid('B', 'svg')], 2);
    const afterFirst = list[0]!.events.length;
    reconcileAnchors(list, [aid('A', 'svg'), aid('B', 'svg')], 3);
    expect(list[0]!.events.length).toBe(afterFirst); // no churn
  });

  it('ignores text anchors and deleted comments', () => {
    const textComment: Comment = {
      id: 'c1',
      author: null,
      created: '2026-01-01',
      created_in: 1,
      events: [
        {
          kind: 'created',
          at_version: 1,
          at: '2026-01-01',
          text: 'c',
          anchor: { kind: 'text', text: 'hi' },
        },
      ],
    };
    reconcileAnchors([textComment], [aid('X', 'svg')], 2);
    expect(textComment.events).toHaveLength(1);
  });

  it('rebinds a previously-lost anchor when the artifact returns', () => {
    const list = [elementComment('c1', 'OLD', { label: 'svg' })];
    reconcileAnchors(list, [aid('A', 'svg'), aid('B', 'svg')], 2); // ambiguous → lost
    expect(snapshotAt(list[0]!, 2)!.anchor).toMatchObject({ kind: 'lost' });
    reconcileAnchors(list, [aid('NEW', 'svg')], 3); // now unambiguous → recovers
    expect(snapshotAt(list[0]!, 3)!.anchor).toMatchObject({ kind: 'element', aid: 'NEW' });
  });

  it('resolves an anchor that stores its aid only in the selector', () => {
    const c: Comment = {
      id: 'c1',
      author: null,
      created: '2026-01-01',
      created_in: 1,
      events: [
        {
          kind: 'created',
          at_version: 1,
          at: '2026-01-01',
          text: 'c',
          anchor: { kind: 'element', selector: '[data-tdoc-aid="SEL"]', label: 'svg' },
        },
      ],
    };
    reconcileAnchors([c], [aid('SEL', 'svg')], 2); // still valid via selector → no new event
    expect(c.events).toHaveLength(1);
  });
});

describe('legacy migration', () => {
  it('migrates a flat comment (status/reactions/replies) into events', () => {
    const legacy = {
      id: 'c1',
      version: 2,
      text: 'hello',
      status: 'applied',
      applied_in: 2,
      anchor: { kind: 'text', text: 'x' },
      author: { login: 'u' },
      reactions: { '👍': ['u'] },
      replies: [
        { id: 'r1', text: 'hi', author: { login: 'v' }, version: 2, reactions: { '🎉': ['v'] } },
      ],
    } as unknown as Comment;
    expect(ensureEventLog(legacy)).toBe(true);
    const snap = snapshotAt(legacy, 2)!;
    expect(snap.text).toBe('hello');
    expect(snap.status).toBe('applied');
    expect(snap.reactions['👍']).toContain('u');
    expect(snap.replies[0]!.text).toBe('hi');
    expect(snap.replies[0]!.reactions['🎉']).toContain('v');
  });

  it('ensureMigrated reports whether anything changed', () => {
    const already: Comment = {
      id: 'c1',
      author: null,
      created: 'x',
      created_in: 1,
      events: [
        { kind: 'created', at_version: 1, at: 'x', anchor: null, text: 'a', eid: 'created:x:0' },
      ],
    };
    expect(ensureMigrated([already])).toBe(false);
  });

  it('compactComments collapses duplicate-eid events', () => {
    const c: Comment = {
      id: 'c1',
      author: null,
      created: 'x',
      created_in: 1,
      events: [
        {
          kind: 'reaction_added',
          at_version: 1,
          at: 'a',
          emoji: '👍',
          by: 'u',
          eid: 'reaction_added:👍:u',
        },
        {
          kind: 'reaction_added',
          at_version: 1,
          at: 'b',
          emoji: '👍',
          by: 'u',
          eid: 'reaction_added:👍:u',
        },
      ],
    };
    expect(compactComments([c])).toBe(true);
    expect(c.events).toHaveLength(1);
  });

  it('safeParseList tolerates junk', () => {
    expect(safeParseList(undefined)).toStrictEqual([]);
    expect(safeParseList('not json')).toStrictEqual([]);
    expect(safeParseList('{"not":"array"}')).toStrictEqual([]);
    expect(safeParseList('[{"id":"c1"}]')).toHaveLength(1);
  });
});
