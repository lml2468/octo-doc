import { describe, it, expect } from 'vitest';
import { readFileSync, existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { stampAids } from '../../src/core/stamp.js';

const here = dirname(fileURLToPath(import.meta.url));
const UPSTREAM = '/Users/merlin/Workspace/tdoc/worker/worker.js';

const SAMPLES = [
  '<h1>Hi</h1><figure><svg viewBox="0 0 4 4"><rect/></svg></figure>',
  '<section><p>x</p><img src="a.png" alt="a > b"></section>',
  '<pre>code <section> not real</section></pre><table><tr><td>1</td></tr></table>',
  '<div data-tdoc-artifact><canvas></canvas></div><blockquote>q</blockquote>',
  '<script>var x="</section>";</script><section>real</section>',
];

describe('stampAids', () => {
  it('stamps figure + svg with content-hashed aids', () => {
    const { html, aids } = stampAids('<figure><svg viewBox="0 0 4 4"><rect/></svg></figure>');
    expect(aids).toHaveLength(2);
    expect(html).toMatch(/<figure data-tdoc-aid="[\w]+">/);
    expect(html).toMatch(/<svg viewBox="0 0 4 4" data-tdoc-aid="[\w]+">/);
  });

  it('gives the same artifact the same aid regardless of position', () => {
    const a = stampAids('<section><p>hi</p></section>').aids[0]!.aid;
    const b = stampAids('<div><section><p>hi</p></section></div>').aids[0]!.aid;
    expect(a).toBe(b);
  });

  it('does not let a > inside an attribute truncate the tag', () => {
    const { html } = stampAids('<img src="a.png" alt="a > b">');
    expect(html).toMatch(/alt="a > b" data-tdoc-aid="[\w]+"/);
  });

  it('skips raw-text bodies so a fake </section> inside <script> is ignored', () => {
    const { aids } = stampAids('<script>var x="</section>";</script><section>real</section>');
    expect(aids).toHaveLength(1);
    expect(aids[0]!.tag).toBe('section');
  });

  // The byte-equivalence guarantee: same input → same output as the upstream Worker.
  it.runIf(existsSync(UPSTREAM))(
    'is byte-equivalent to the upstream Cloudflare worker',
    async () => {
      const wsrc = readFileSync(UPSTREAM, 'utf8');
      const start = wsrc.indexOf('const STAMPABLE_TAGS');
      const endMarker = '\n  return { html: out, aids };\n}';
      const chunk = wsrc.slice(start, wsrc.lastIndexOf(endMarker) + endMarker.length);
      const mod = (await import(
        'data:text/javascript,' + encodeURIComponent(chunk + '\nexport { stampAids };')
      )) as {
        stampAids: (h: string) => { html: string; aids: unknown[] };
      };
      const samples = [...SAMPLES];
      const sample = join(here, '..', 'fixtures', 'sample-v2.html');
      if (existsSync(sample)) samples.push(readFileSync(sample, 'utf8'));
      for (const s of samples) {
        const mine = stampAids(s);
        const theirs = mod.stampAids(s);
        expect(mine.html, `html for: ${s.slice(0, 40)}`).toBe(theirs.html);
        expect(mine.aids).toStrictEqual(theirs.aids);
      }
    },
  );
});
