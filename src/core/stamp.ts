/**
 * Artifact identity (`data-tdoc-aid`) stamping.
 *
 * Ported from the upstream Cloudflare Worker so that the SAME input HTML
 * produces the SAME stamped output byte-for-byte (verified by contract test).
 * Pure string manipulation, zero runtime dependencies.
 *
 * Why: positional CSS selectors silently drift when an edit restructures HTML.
 * Instead we stamp every commentable artifact with a content-hashed
 * `data-tdoc-aid`. The same artifact in a different version gets the same aid,
 * so comments anchor by identity, not by a path through the DOM.
 */
import type { StampedArtifact } from './comment.types.js';

/** Tags treated as commentable artifacts and stamped with an aid. */
const STAMPABLE_TAGS = [
  'img',
  'svg',
  'canvas',
  'video',
  'pre',
  'figure',
  'iframe',
  'section',
  'aside',
  'blockquote',
  'table',
  'details',
] as const;

/** Elements whose body is raw text (their `<` content is never markup). */
const RAW_TEXT_TAGS = ['script', 'style', 'textarea', 'title'] as const;

/** Attributes that are part of an artifact's identity (e.g. what makes an svg *this* svg). */
const INTRINSIC_ATTRS = ['viewBox', 'src', 'alt', 'aria-label', 'title'] as const;

/** One harvested element with the byte offsets needed to re-stamp it in place. */
interface Element {
  openStart: number;
  openEnd: number;
  closeEnd: number;
  tag: string;
  attrs: string;
  innerHtml: string;
  isVoid: boolean;
  cleanedAttrs: string;
  aid: string;
}

/** A heading and the offset at which it ends, for nearest-heading lookup. */
interface Heading {
  end: number;
  text: string;
}

/**
 * 53-bit public-domain string hash (cyrb53). Identical to the overlay's copy so
 * identities computed on either side agree.
 */
export function cyrb53(str: string, seed = 0): string {
  let h1 = 0xdeadbeef ^ seed;
  let h2 = 0x41c6ce57 ^ seed;
  for (let i = 0; i < str.length; i++) {
    const ch = str.charCodeAt(i);
    h1 = Math.imul(h1 ^ ch, 2654435761);
    h2 = Math.imul(h2 ^ ch, 1597334677);
  }
  h1 = Math.imul(h1 ^ (h1 >>> 16), 2246822507) ^ Math.imul(h2 ^ (h2 >>> 13), 3266489909);
  h2 = Math.imul(h2 ^ (h2 >>> 16), 2246822507) ^ Math.imul(h1 ^ (h1 >>> 13), 3266489909);
  return (4294967296 * (2097151 & h2) + (h1 >>> 0)).toString(36);
}

/** Compute the content-hash aid for one artifact element. */
function aidFor(tag: string, innerHtml: string, openAttrs: string): string {
  const intrinsics = INTRINSIC_ATTRS.map((a) => {
    const m = new RegExp('\\b' + a + '\\s*=\\s*"([^"]*)"', 'i').exec(openAttrs);
    return m ? a + '=' + m[1] : '';
  })
    .filter(Boolean)
    .join('|');
  const norm = innerHtml
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\sdata-tdoc-[\w-]+\s*=\s*"[^"]*"/gi, '')
    .replace(/\s+/g, ' ')
    .trim();
  return cyrb53(tag + '|' + intrinsics + '|' + norm);
}

/**
 * Index just past the `>` that closes the open tag starting at `lt`, treating
 * `>` inside quoted attribute values as ordinary text. Returns -1 if unterminated.
 */
function attrAwareOpenTagEnd(html: string, lt: number): number {
  let quote: string | null = null;
  for (let i = lt + 1; i < html.length; i++) {
    const ch = html[i];
    if (quote) {
      if (ch === quote) quote = null;
      continue;
    }
    if (ch === '"' || ch === "'") quote = ch;
    else if (ch === '>') return i + 1;
  }
  return -1;
}

/** Index just past a raw-text element's closing tag, so its body can't desync depth. */
function skipRawTextBodyAt(html: string, openTag: string, attrs: string, openEnd: number): number {
  if (/\/\s*$/.test(attrs)) return openEnd; // self-closed
  const closeRe = new RegExp(`</${openTag}\\s*>`, 'i');
  const m = closeRe.exec(html.slice(openEnd));
  return m ? openEnd + m.index + m[0].length : html.length;
}

/** Collect `<hN>` headings with their end offsets for nearest-heading lookup. */
function collectHeadings(html: string): Heading[] {
  const headRe = /<h([1-3])\b[^>]*>([\s\S]*?)<\/h\1>/gi;
  const headings: Heading[] = [];
  let m: RegExpExecArray | null;
  while ((m = headRe.exec(html))) {
    headings.push({
      end: m.index + m[0].length,
      text: (m[2] ?? '')
        .replace(/<[^>]+>/g, '')
        .replace(/\s+/g, ' ')
        .trim(),
    });
  }
  return headings;
}

/** Find the closing-tag end offset for a non-void element opened at `openEnd`. */
function findCloseEnd(html: string, tag: string, openEnd: number): number {
  const closeRe = new RegExp(`</${tag}\\s*>`, 'gi');
  const openRe = new RegExp(`<${tag}\\b`, 'gi');
  const rawRe = new RegExp(`<(${RAW_TEXT_TAGS.join('|')})\\b`, 'gi');
  let depth = 1;
  let scan = openEnd;
  while (scan < html.length) {
    closeRe.lastIndex = scan;
    openRe.lastIndex = scan;
    rawRe.lastIndex = scan;
    const close = closeRe.exec(html);
    const open = openRe.exec(html);
    const raw = rawRe.exec(html);
    const candidates = [close, open, raw].filter((x): x is RegExpExecArray => x !== null);
    if (candidates.length === 0) break;
    const next = candidates.sort((a, b) => a.index - b.index)[0]!;
    if (next === raw) {
      const rEnd = attrAwareOpenTagEnd(html, next.index);
      if (rEnd < 0) break;
      scan = skipRawTextBodyAt(
        html,
        (next[1] ?? '').toLowerCase(),
        html.slice(next.index, rEnd),
        rEnd,
      );
    } else if (next === close) {
      if (--depth === 0) return next.index + next[0].length;
      scan = next.index + next[0].length;
    } else {
      depth++;
      const oEnd = attrAwareOpenTagEnd(html, next.index);
      scan = oEnd < 0 ? next.index + next[0].length : oEnd;
    }
  }
  return openEnd;
}

/** Harvest one element (resolving its inner HTML) into `elements`, deduping by open offset. */
function harvest(
  html: string,
  openStart: number,
  openEnd: number,
  tag: string,
  attrs: string,
  seen: Set<number>,
  elements: Omit<Element, 'cleanedAttrs' | 'aid'>[],
): void {
  if (seen.has(openStart)) return;
  const isVoid = /^(img|iframe)$/i.test(tag) || /\/\s*$/.test(attrs);
  let closeEnd = openEnd;
  let innerHtml = '';
  if (!isVoid) {
    closeEnd = findCloseEnd(html, tag, openEnd);
    innerHtml = html.slice(openEnd, closeEnd - `</${tag}>`.length);
  }
  seen.add(openStart);
  elements.push({ openStart, openEnd, closeEnd, tag, attrs, innerHtml, isVoid });
}

/** Pass 1: harvest every known stampable tag. */
function harvestStampableTags(
  html: string,
  seen: Set<number>,
  elements: Omit<Element, 'cleanedAttrs' | 'aid'>[],
): void {
  for (const tag of STAMPABLE_TAGS) {
    const openRe = new RegExp(`<${tag}\\b`, 'gi');
    let m: RegExpExecArray | null;
    while ((m = openRe.exec(html))) {
      const end = attrAwareOpenTagEnd(html, m.index);
      if (end < 0) continue;
      harvest(
        html,
        m.index,
        end,
        tag,
        html.slice(m.index + 1 + tag.length, end - 1),
        seen,
        elements,
      );
    }
  }
}

/** Pass 2: harvest opt-in markers (`data-tdoc-artifact` or class `tdoc-artifact`). */
function harvestOptInMarkers(
  html: string,
  seen: Set<number>,
  elements: Omit<Element, 'cleanedAttrs' | 'aid'>[],
): void {
  const probe = /<([a-z][\w-]*)\b/gi;
  let m: RegExpExecArray | null;
  while ((m = probe.exec(html))) {
    const tag = (m[1] ?? '').toLowerCase();
    const end = attrAwareOpenTagEnd(html, m.index);
    if (end < 0) continue;
    const attrs = html.slice(m.index + 1 + (m[1] ?? '').length, end - 1);
    if (
      /\bdata-tdoc-artifact\b/i.test(attrs) ||
      /class\s*=\s*"[^"]*\btdoc-artifact\b[^"]*"/i.test(attrs)
    ) {
      harvest(html, m.index, end, tag, attrs, seen, elements);
    }
  }
}

/** Result of {@link stampAids}: the stamped HTML and the artifact index. */
export interface StampResult {
  html: string;
  aids: StampedArtifact[];
}

/**
 * Stamp `data-tdoc-aid` on every commentable artifact in `rawHtml`.
 *
 * @param rawHtml - the document HTML to stamp
 * @returns the stamped HTML plus the list of stamped artifacts (aid, tag,
 *   head excerpt, nearest heading)
 */
export function stampAids(rawHtml: string): StampResult {
  const headings = collectHeadings(rawHtml);
  const nearestHeadingAt = (idx: number): string | null => {
    let best: string | null = null;
    for (const h of headings) {
      if (h.end <= idx) best = h.text;
      else break;
    }
    return best;
  };

  const seen = new Set<number>();
  const harvested: Omit<Element, 'cleanedAttrs' | 'aid'>[] = [];
  harvestStampableTags(rawHtml, seen, harvested);
  harvestOptInMarkers(rawHtml, seen, harvested);

  const aids: StampedArtifact[] = [];
  const elements: Element[] = harvested.map((e) => {
    const cleanedAttrs = e.attrs.replace(/\s+data-tdoc-aid\s*=\s*"[^"]*"/gi, '');
    const cleanedInner = e.innerHtml.replace(/\sdata-tdoc-aid\s*=\s*"[^"]*"/gi, '');
    const aid = aidFor(e.tag, cleanedInner, cleanedAttrs);
    aids.push({
      aid,
      tag: e.tag,
      head: e.innerHtml.slice(0, 80),
      heading: nearestHeadingAt(e.openStart),
    });
    return { ...e, cleanedAttrs, aid };
  });

  // Apply stamps in reverse offset order so earlier offsets stay valid.
  elements.sort((a, b) => b.openStart - a.openStart);
  let out = rawHtml;
  for (const e of elements) {
    const selfClose = /\/\s*$/.test(e.attrs) ? '/' : '';
    const stampedOpen = e.isVoid
      ? `<${e.tag}${e.cleanedAttrs} data-tdoc-aid="${e.aid}"${selfClose}>`
      : `<${e.tag}${e.cleanedAttrs} data-tdoc-aid="${e.aid}">`;
    out = out.slice(0, e.openStart) + stampedOpen + out.slice(e.openEnd);
  }
  return { html: out, aids };
}
