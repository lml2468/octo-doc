import { describe, it, expect } from 'vitest';
import { buildForkExport } from '../../src/routes/fork-export.js';
import type { CommentSnapshot } from '../../src/core/comment.types.js';

const snap = (over: Partial<CommentSnapshot>): CommentSnapshot => ({
  id: 'c1',
  author: { login: 'alice' },
  created: '2026-01-01',
  created_in: 1,
  version: 1,
  anchor: { kind: 'text', text: 'Hello' },
  text: 'a comment',
  status: 'open',
  applied_in: undefined,
  replies: [],
  reactions: {},
  deleted: false,
  ...over,
});

describe('buildForkExport', () => {
  const html = '<!doctype html><body><h1>Hello</h1></body>';

  it('export embeds banner + JSON block + inline marker for text anchors', () => {
    const out = buildForkExport({
      slug: 's',
      version: 1,
      html,
      comments: [snap({})],
      kind: 'export',
    });
    expect(out).toMatch(/octo-doc fork export/);
    expect(out).toMatch(/id="tdoc-fork-comments"/);
    expect(out).toMatch(/<!--TDOC-COMMENT id="c1" by="alice"-->Hello<!--\/TDOC-COMMENT-->/);
    // export does not boot the overlay
    expect(out).not.toMatch(/"mode":"fork"/);
  });

  it('fork boots the overlay in fork mode', () => {
    const out = buildForkExport({ slug: 's', version: 1, html, comments: [], kind: 'fork' });
    expect(out).toMatch(/"mode":"fork"/);
  });

  it('renders element anchors, reactions, and replies in the banner', () => {
    const out = buildForkExport({
      slug: 's',
      version: 1,
      html,
      comments: [
        snap({
          anchor: { kind: 'element', label: 'svg' },
          reactions: { '👍': ['bob'] },
          replies: [
            {
              id: 'r1',
              parent_id: 'c1',
              author: { login: 'bob' },
              text: 'a reply',
              agent_status: null,
              created: '2026-01-02',
              reactions: { '🎉': ['carol'] },
              deleted: false,
            },
          ],
        }),
      ],
      kind: 'export',
    });
    expect(out).toMatch(/on svg/);
    expect(out).toMatch(/👍 \(1\)/);
    expect(out).toMatch(/↳ @bob: "a reply"/);
  });

  it('neutralizes comment-terminator sequences in the banner', () => {
    const out = buildForkExport({
      slug: 's',
      version: 1,
      html,
      comments: [snap({ text: 'evil --> oops' })],
      kind: 'export',
    });
    // The banner is an HTML comment; the `--` run must be broken so the
    // attacker text cannot terminate it early.
    const banner = out.slice(0, out.indexOf('-->\n'));
    expect(banner).toContain('evil -\\-> oops');
    expect(banner).not.toContain('evil --> oops');
  });
});
