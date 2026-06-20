// Core logic unit tests — aid stamping parity vs upstream, and the event-log
// comment fold. These guard the "byte-equivalent rendering" success criterion.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, existsSync } from 'node:fs';
import { stampAids } from '../../src/core/stamp.js';
import { applyCommentOp } from '../../src/core/ops.js';
import { snapshotAt, snapshotList, historyList, dedupEvents } from '../../src/core/comments.js';

const UPSTREAM = '/Users/merlin/Workspace/tdoc/worker/worker.js';

test('stampAids: stamps commentable artifacts with content-hashed aids', () => {
  const { html, aids } = stampAids('<figure><svg viewBox="0 0 4 4"><rect/></svg></figure>');
  assert.equal(aids.length, 2); // figure + svg
  assert.match(html, /<figure data-tdoc-aid="[\w]+">/);
  assert.match(html, /<svg viewBox="0 0 4 4" data-tdoc-aid="[\w]+">/);
});

test('stampAids: same artifact => same aid across calls (identity, not position)', () => {
  const a = stampAids('<section><p>hi</p></section>').aids[0].aid;
  const b = stampAids('<div><section><p>hi</p></section></div>').aids[0].aid;
  assert.equal(a, b);
});

test('stampAids: > inside an attribute does not truncate the tag', () => {
  const { html } = stampAids('<img src="a.png" alt="a > b">');
  assert.match(html, /alt="a > b" data-tdoc-aid="[\w]+"/);
});

test('stampAids: byte-parity with the upstream Cloudflare worker', { skip: !existsSync(UPSTREAM) }, async () => {
  const wsrc = readFileSync(UPSTREAM, 'utf8');
  const start = wsrc.indexOf('const STAMPABLE_TAGS');
  const endMarker = '\n  return { html: out, aids };\n}';
  const lastEnd = wsrc.lastIndexOf(endMarker) + endMarker.length;
  const chunk = wsrc.slice(start, lastEnd);
  const mod = await import('data:text/javascript,' + encodeURIComponent(chunk + '\nexport { stampAids };'));
  const theirs = mod.stampAids;
  const samples = [
    '<h1>Hi</h1><figure><svg viewBox="0 0 4 4"><rect/></svg></figure>',
    '<section><p>x</p><img src="a.png" alt="a > b"></section>',
    '<pre>code <section> not real</section></pre><table><tr><td>1</td></tr></table>',
    '<div data-tdoc-artifact><canvas></canvas></div><blockquote>q</blockquote>',
    '<script>var x="</section>";</script><section>real</section>',
  ];
  for (const s of samples) {
    const a = stampAids(s), b = theirs(s);
    assert.equal(a.html, b.html, `html mismatch for: ${s.slice(0, 40)}`);
    assert.deepEqual(a.aids, b.aids, `aids mismatch for: ${s.slice(0, 40)}`);
  }
});

test('comment fold: create → reply → react → delete folds correctly', () => {
  const list = [];
  applyCommentOp(list, { kind: 'create', id: 'c1', author: { login: 'a' }, text: 'hello', version: 1, at: '2026-01-01T00:00:00Z' });
  applyCommentOp(list, { kind: 'reply', parent_id: 'c1', reply_id: 'r1', author: { login: 'b' }, text: 'hi back', version: 1, at: '2026-01-01T00:01:00Z' });
  applyCommentOp(list, { kind: 'react', comment_id: 'c1', emoji: '👍', by: 'b', version: 1 });

  const snap = snapshotAt(list[0], 1);
  assert.equal(snap.text, 'hello');
  assert.equal(snap.replies.length, 1);
  assert.equal(snap.replies[0].text, 'hi back');
  assert.deepEqual(snap.reactions['👍'], ['b']);

  // toggle the reaction off
  applyCommentOp(list, { kind: 'react', comment_id: 'c1', emoji: '👍', by: 'b', version: 1 });
  assert.equal(snapshotAt(list[0], 1).reactions['👍'], undefined);

  // delete hides it from snapshotList but historyList still excludes deleted
  applyCommentOp(list, { kind: 'delete', id: 'c1', version: 1, actor: { login: 'a' } });
  assert.equal(snapshotList(list, 1).length, 0);
});

test('comment fold: version scoping — a v3 comment is invisible at v2', () => {
  const list = [];
  applyCommentOp(list, { kind: 'create', id: 'c1', author: { login: 'a' }, text: 'late', version: 3, at: '2026-01-03T00:00:00Z' });
  assert.equal(snapshotAt(list[0], 2), null);
  assert.equal(snapshotList(list, 2).length, 0);
  assert.equal(snapshotList(list, 3).length, 1);
  assert.equal(historyList(list).length, 1); // history is cross-version
});

test('dedupEvents: idempotent reaction events converge (last write wins per eid)', () => {
  const events = [
    { kind: 'reaction_added', emoji: '👍', by: 'x', at_version: 1, eid: 'reaction_added:👍:x' },
    { kind: 'reaction_added', emoji: '👍', by: 'x', at_version: 1, eid: 'reaction_added:👍:x' },
  ];
  assert.equal(dedupEvents(events).length, 1);
});

test('publish_merge: non-destructive add-by-id, never overwrites', () => {
  const list = [];
  applyCommentOp(list, { kind: 'create', id: 'c1', author: { login: 'a' }, text: 'server', version: 1, at: '2026-01-01T00:00:00Z' });
  const localComments = [
    { id: 'c1', events: [{ kind: 'created', at_version: 1, at: '2026-01-01', text: 'LOCAL clobber attempt' }] },
    { id: 'c2', events: [{ kind: 'created', at_version: 1, at: '2026-01-01', text: 'genuinely new' }] },
  ];
  const res = applyCommentOp(list, { kind: 'publish_merge', localComments, aids: [], version: 1 });
  assert.equal(res.body.mergedComments, 1); // only c2 added
  assert.equal(snapshotAt(list.find(c => c.id === 'c1'), 1).text, 'server'); // not clobbered
});
