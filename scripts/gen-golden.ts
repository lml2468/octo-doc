/**
 * Golden fixture generator — the byte-equivalence safety net for the Go port.
 *
 * Runs the REAL upstream-ported TS core over a battery of inputs and writes the
 * results to `testdata/golden/`. The Go implementation must reproduce these
 * exactly:
 *   - stamp/  + cyrb53.json : BYTE-equivalent (stamped HTML / hash strings)
 *   - fold/, ops/, reconcile/ : LOGICALLY-equivalent (snapshot JSON)
 *
 * Why two flavors: `eventEid` for one-shot events is intentionally
 * non-deterministic (counter + perf clock), but it never appears in a folded
 * snapshot — the fold keys on it then drops it. So fold/ops/reconcile goldens
 * compare the snapshot JSON (deterministic given a fixed `at`), while stamp +
 * cyrb53 — which are pure and fully deterministic — compare bytes.
 *
 * Run: pnpm tsx scripts/gen-golden.ts
 */
import { mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { dirname } from 'node:path';
import { stampAids, cyrb53 } from '../src/core/stamp.js';
import { snapshotList, historyList } from '../src/core/comment-fold.js';
import { applyCommentOp, type CommentOp } from '../src/core/ops.js';
import { reconcileAnchors } from '../src/core/reconcile.js';
import type { Comment, StampedArtifact } from '../src/core/comment.types.js';

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, '..');
const goldenDir = join(root, 'testdata', 'golden');

/** Fixed timestamp so any `op.at`-less path is still deterministic in goldens. */
const AT = '2024-01-01T00:00:00.000Z';

function reset(dir: string): void {
  rmSync(dir, { recursive: true, force: true });
  mkdirSync(dir, { recursive: true });
}

function writeText(p: string, s: string): void {
  writeFileSync(p, s, 'utf8');
}

function writeJson(p: string, v: unknown): void {
  writeFileSync(p, JSON.stringify(v, null, 2) + '\n', 'utf8');
}

// ========== cyrb53 (byte-equivalent string→hash) ==========
// Covers the three integer/encoding traps the Go port must reproduce:
//   - 32-bit wrap-around (Math.imul)
//   - UTF-16 code-unit iteration (charCodeAt), NOT bytes or runes
//   - 53-bit float assembly + base36
function genCyrb53(): void {
  const inputs = [
    '',
    'a',
    'hello world',
    'The quick brown fox jumps over the lazy dog.',
    // UTF-16 trap: multi-byte CJK + astral emoji (surrogate pairs)
    '你好，世界',
    '日本語のテキスト',
    '🚀🔥✅ emoji mix 🎉',
    '𝕌𝕟𝕚𝕔𝕠𝕕𝕖 astral plane', // astral → surrogate pairs in UTF-16
    'mixed 中文 and English 123',
    // long string to exercise the loop
    'x'.repeat(1000),
    '<svg viewBox="0 0 24 24"><path d="M3 8.5"/></svg>',
  ];
  const table = inputs.map((s) => ({ input: s, seed: 0, hash: cyrb53(s, 0) }));
  // a couple with non-zero seed
  table.push({ input: 'hello world', seed: 42, hash: cyrb53('hello world', 42) });
  table.push({ input: '你好', seed: 7, hash: cyrb53('你好', 7) });
  writeJson(join(goldenDir, 'cyrb53.json'), table);
  console.log(`  cyrb53: ${table.length} cases`);
}

// ========== stampAids (byte-equivalent HTML→HTML) ==========
const STAMP_CASES: { name: string; html: string }[] = [
  {
    name: 'simple-img',
    html: `<!doctype html><html><body><div class="wrap"><h1>Title</h1><img src="a.png" alt="pic"><p>text</p></div></body></html>`,
  },
  {
    name: 'svg-with-viewbox',
    html: `<body><h2>Diagram</h2><svg viewBox="0 0 100 100"><circle cx="50" cy="50" r="40"/></svg></body>`,
  },
  {
    name: 'nested-section',
    html: `<body><section><h3>Outer</h3><p>a</p><figure><img src="x.jpg"><figcaption>cap</figcaption></figure></section></body>`,
  },
  {
    name: 'pre-raw-text',
    html: `<body><pre><code>if (a &lt; b) { return "&gt;"; }</code></pre><table><tr><td>cell</td></tr></table></body>`,
  },
  {
    name: 'script-style-skip',
    html: `<body><script>var x = "<table>not real</table>";</script><blockquote>quote</blockquote><style>.a{}</style></body>`,
  },
  {
    name: 'utf16-content',
    html: `<body><h1>中文标题</h1><section><p>段落内容 with emoji 🚀</p></section><blockquote>引用文字 𝕌</blockquote></body>`,
  },
  {
    name: 'already-stamped',
    html: `<body><img src="a.png" data-tdoc-aid="oldhash" alt="x"><section data-tdoc-aid="other"><p>hi</p></section></body>`,
  },
  {
    name: 'opt-in-markers',
    html: `<body><div data-tdoc-artifact><span>card content</span></div><div class="foo tdoc-artifact bar">widget</div><div class="plain">no stamp</div></body>`,
  },
  {
    name: 'attr-with-gt',
    html: `<body><img src="a.png" alt="a > b comparison"><details open><summary>more</summary><p>body</p></details></body>`,
  },
  {
    name: 'heading-pairing',
    html: `<body><h1>H1 with <em>markup</em></h1><aside>side</aside><h2>H2</h2><iframe src="https://x.com/embed"></iframe></body>`,
  },
  {
    name: 'self-closing-void',
    html: `<body><img src="a.png"/><svg viewBox="0 0 1 1"/><video src="v.mp4"></video></body>`,
  },
  {
    name: 'adversarial-unclosed',
    html: `<body><section><p>unterminated section<img src="a.png"></body>`,
  },
  {
    name: 'empty',
    html: ``,
  },
  {
    name: 'no-artifacts',
    html: `<body><h1>Just</h1><p>prose here, nothing commentable as a unit.</p><a href="#">link</a></body>`,
  },
];

function genStamp(): void {
  const dir = join(goldenDir, 'stamp');
  mkdirSync(dir, { recursive: true });
  for (const { name, html } of STAMP_CASES) {
    const { html: out, aids } = stampAids(html);
    writeText(join(dir, `${name}.in.html`), html);
    writeText(join(dir, `${name}.out.html`), out);
    writeJson(join(dir, `${name}.aids.json`), aids);
  }
  console.log(`  stamp: ${STAMP_CASES.length} cases`);
}

// ========== fold (event-log → snapshot, logical) ==========
function genFold(): void {
  const dir = join(goldenDir, 'fold');
  mkdirSync(dir, { recursive: true });

  const cases: { name: string; list: Comment[]; version: number | 'all' }[] = [
    {
      name: 'created-only',
      version: 1,
      list: [
        {
          id: 'c1',
          author: { login: 'alice' },
          created: AT,
          created_in: 1,
          events: [
            { kind: 'created', eid: 'e1', at_version: 1, at: AT, anchor: { kind: 'text', text: 'hi' }, text: 'first comment' },
          ],
        },
      ],
    },
    {
      name: 'reply-and-react',
      version: 2,
      list: [
        {
          id: 'c1',
          author: { login: 'alice' },
          created: AT,
          created_in: 1,
          events: [
            { kind: 'created', eid: 'e1', at_version: 1, at: AT, anchor: null, text: 'parent' },
            { kind: 'reply_added', eid: 'e2', at_version: 1, at: AT, reply: { id: 'r1', author: { login: 'bob' }, text: 'a reply', agent_status: null } },
            { kind: 'reaction_added', eid: 'e3', at_version: 1, at: AT, emoji: '👍', by: 'carol' },
            { kind: 'reaction_added', eid: 'e4', at_version: 1, at: AT, emoji: '👍', by: 'dave' },
          ],
        },
      ],
    },
    {
      name: 'agent-applied-verdict',
      version: 2,
      list: [
        {
          id: 'c1',
          author: { login: 'alice' },
          created: AT,
          created_in: 1,
          events: [
            { kind: 'created', eid: 'e1', at_version: 1, at: AT, anchor: null, text: 'fix this' },
            { kind: 'reply_added', eid: 'e2', at_version: 2, at: AT, reply: { id: 'r1', author: { kind: 'agent', login: 'tdoc-agent' }, text: 'done', agent_status: 'applied' } },
            { kind: 'marked_applied', eid: 'e3', at_version: 2, at: AT, applied_in: 2, by: 'tdoc-agent', agent_status: 'applied' },
          ],
        },
      ],
    },
    {
      name: 'version-windowing',
      version: 1,
      list: [
        {
          id: 'c1',
          author: { login: 'alice' },
          created: AT,
          created_in: 1,
          events: [
            { kind: 'created', eid: 'e1', at_version: 1, at: AT, anchor: null, text: 'v1 text' },
            { kind: 'text_edited', eid: 'e2', at_version: 2, at: AT, text: 'v2 text (should not show at v1)' },
          ],
        },
      ],
    },
    {
      name: 'reaction-toggle-off',
      version: 1,
      list: [
        {
          id: 'c1',
          author: { login: 'alice' },
          created: AT,
          created_in: 1,
          events: [
            { kind: 'created', eid: 'e1', at_version: 1, at: AT, anchor: null, text: 'p' },
            { kind: 'reaction_added', eid: 'e2', at_version: 1, at: AT, emoji: '🔥', by: 'bob' },
            { kind: 'reaction_removed', eid: 'e3', at_version: 1, at: AT, emoji: '🔥', by: 'bob' },
          ],
        },
      ],
    },
    {
      name: 'deleted-excluded',
      version: 1,
      list: [
        {
          id: 'c1',
          author: { login: 'alice' },
          created: AT,
          created_in: 1,
          events: [
            { kind: 'created', eid: 'e1', at_version: 1, at: AT, anchor: null, text: 'gone' },
            { kind: 'deleted', eid: 'e2', at_version: 1, at: AT, by: 'alice' },
          ],
        },
        {
          id: 'c2',
          author: { login: 'bob' },
          created: AT,
          created_in: 1,
          events: [{ kind: 'created', eid: 'e3', at_version: 1, at: AT, anchor: null, text: 'kept' }],
        },
      ],
    },
    {
      name: 'legacy-migration',
      version: 1,
      list: [
        // legacy flat comment (no events[]) — exercises ensureEventLog
        {
          id: 'c1',
          author: { login: 'alice' },
          created: AT,
          created_in: 1,
          version: 1,
          text: 'legacy comment',
          anchor: { kind: 'text', text: 'foo' },
          status: 'applied',
          applied_in: 1,
          reactions: { '👍': ['bob'] },
          replies: [{ id: 'r1', author: { login: 'carol' }, text: 'legacy reply', created: AT, version: 1 }],
        } as unknown as Comment,
      ],
    },
  ];

  for (const { name, list, version } of cases) {
    // fold migrates legacy comments in place — capture the input BEFORE folding.
    const inputClone = structuredClone(list);
    const out = version === 'all' ? historyList(list) : snapshotList(list, version);
    writeJson(join(dir, `${name}.in.json`), { list: inputClone, version });
    writeJson(join(dir, `${name}.out.json`), out);
  }
  console.log(`  fold: ${cases.length} cases`);
}

// ========== ops (apply mutation → snapshot/result, logical) ==========
function genOps(): void {
  const dir = join(goldenDir, 'ops');
  mkdirSync(dir, { recursive: true });

  const baseComment = (): Comment[] => [
    {
      id: 'c1',
      author: { login: 'alice' },
      created: AT,
      created_in: 1,
      events: [{ kind: 'created', eid: 'e1', at_version: 1, at: AT, anchor: { kind: 'text', text: 'anchor' }, text: 'base' }],
    },
  ];

  const cases: { name: string; list: Comment[]; op: CommentOp; snapshotVersion: number }[] = [
    {
      name: 'create',
      list: [],
      op: { kind: 'create', id: 'c1', author: { login: 'alice' }, text: 'new', anchor: { kind: 'text', text: 'x' }, version: 1, at: AT },
      snapshotVersion: 1,
    },
    {
      name: 'reply',
      list: baseComment(),
      op: { kind: 'reply', parent_id: 'c1', reply_id: 'r1', author: { login: 'bob' }, text: 'replying', version: 1, at: AT },
      snapshotVersion: 1,
    },
    {
      name: 'react',
      list: baseComment(),
      op: { kind: 'react', comment_id: 'c1', emoji: '🎉', by: 'bob', version: 1, at: AT },
      snapshotVersion: 1,
    },
    {
      name: 'patch-anchor',
      list: baseComment(),
      op: { kind: 'patch_anchor', id: 'c1', anchor: { kind: 'text', text: 'moved' }, reset_status: true, version: 1, actor: { login: 'alice' }, at: AT },
      snapshotVersion: 1,
    },
    {
      name: 'delete',
      list: baseComment(),
      op: { kind: 'delete', id: 'c1', version: 1, actor: { login: 'alice' }, at: AT },
      snapshotVersion: 1,
    },
  ];

  for (const { name, list, op, snapshotVersion } of cases) {
    const inputClone = structuredClone(list);
    const result = applyCommentOp(list, op);
    // fold the post-mutation list too, for a richer assertion
    const folded = snapshotList(list, snapshotVersion);
    writeJson(join(dir, `${name}.in.json`), { list: inputClone, op });
    writeJson(join(dir, `${name}.out.json`), { status: result.status, body: result.body, wipe: result.wipe ?? false, folded });
  }
  console.log(`  ops: ${cases.length} cases`);
}

// ========== reconcile (publish-time anchor rebind, logical) ==========
function genReconcile(): void {
  const dir = join(goldenDir, 'reconcile');
  mkdirSync(dir, { recursive: true });

  const cases: { name: string; list: Comment[]; aids: StampedArtifact[]; version: number }[] = [
    {
      name: 'still-valid',
      version: 2,
      aids: [{ aid: 'hash1', tag: 'svg', head: '<circle', heading: 'Diagram' }],
      list: [
        {
          id: 'c1',
          author: { login: 'a' },
          created: AT,
          created_in: 1,
          events: [{ kind: 'created', eid: 'e1', at_version: 1, at: AT, anchor: { kind: 'element', aid: 'hash1', selector: '[data-tdoc-aid="hash1"]', label: 'svg' }, text: 'on svg' }],
        },
      ],
    },
    {
      name: 'rebind-by-heading',
      version: 2,
      aids: [{ aid: 'newhash', tag: 'svg', head: '<circle', heading: 'Diagram' }],
      list: [
        {
          id: 'c1',
          author: { login: 'a' },
          created: AT,
          created_in: 1,
          events: [{ kind: 'created', eid: 'e1', at_version: 1, at: AT, anchor: { kind: 'element', aid: 'oldhash', selector: '[data-tdoc-aid="oldhash"]', label: 'svg', fallback: { nearestHeading: { text: 'Diagram' } } }, text: 'on svg' }],
        },
      ],
    },
    {
      name: 'lost-no-candidate',
      version: 2,
      aids: [{ aid: 'h', tag: 'img', head: '', heading: 'Other' }],
      list: [
        {
          id: 'c1',
          author: { login: 'a' },
          created: AT,
          created_in: 1,
          events: [{ kind: 'created', eid: 'e1', at_version: 1, at: AT, anchor: { kind: 'element', aid: 'gone', selector: '[data-tdoc-aid="gone"]', label: 'svg', fingerprint: { tag: 'svg' } }, text: 'orphan' }],
        },
      ],
    },
  ];

  for (const { name, list, aids, version } of cases) {
    const inputClone = structuredClone(list);
    reconcileAnchors(list, aids, version);
    const folded = snapshotList(list, version);
    writeJson(join(dir, `${name}.in.json`), { list: inputClone, aids, version });
    // The appended anchor_changed event carries a non-deterministic `at` (Date.now)
    // and eid — so we assert on the FOLDED anchor, not the raw event log.
    writeJson(join(dir, `${name}.out.json`), { folded });
  }
  console.log(`  reconcile: ${cases.length} cases`);
}

reset(goldenDir);
console.log('Generating golden fixtures →', goldenDir);
genCyrb53();
genStamp();
genFold();
genOps();
genReconcile();
console.log('Done.');
