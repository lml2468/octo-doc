/**
 * Builds the `/export` and `/fork` HTML: an agent-readable comment banner, a
 * structured JSON block, inline `TDOC-COMMENT` markers around anchored text, and
 * (for fork) the overlay booted in read-only mode. Pure string assembly.
 */
import type { CommentSnapshot, Reactions } from '../core/comment.types.js';
import { forHtmlComment, injectOverlayCfg, safeJsonForScript } from '../core/render.js';

/** Input to {@link buildForkExport}. */
export interface ForkExportInput {
  slug: string;
  version: number;
  html: string;
  comments: CommentSnapshot[];
  kind: 'export' | 'fork';
}

/** Render a reactions map as `emoji (n)` for the human-readable banner. */
function reactionsText(rs: Reactions | undefined): string {
  if (!rs) return '';
  const parts = Object.entries(rs)
    .filter(([, u]) => u && u.length > 0)
    .map(([e, u]) => `${forHtmlComment(e)} (${u.length})`);
  return parts.length ? `    reactions: ${parts.join(', ')}\n` : '';
}

/** Describe a comment's anchor for the banner. */
function describeAnchor(c: CommentSnapshot): string {
  if (c.anchor?.kind === 'element')
    return `(on ${forHtmlComment(c.anchor.label ?? c.anchor.selector ?? 'element')})`;
  if (c.anchor && 'text' in c.anchor && c.anchor.text) {
    return `(on text: "${forHtmlComment(c.anchor.text.replace(/"/g, '\\"').slice(0, 120))}")`;
  }
  return '(no anchor)';
}

/** Build the leading agent-readable banner comment. */
function buildBanner(input: ForkExportInput, open: CommentSnapshot[]): string {
  let banner = `<!--
  ===== octo-doc fork export =====
  slug: ${forHtmlComment(input.slug)}
  version: ${forHtmlComment(String(input.version))}
  exported: ${new Date().toISOString()}

  ## How to use this file
  Save it as ~/tdocs/<your-new-slug>/v1/index.html (or anywhere you like).
  Comments below are read-only metadata bundled with the fork. Agents can
  read them to apply changes — say "apply all comments to this doc" and the
  agent will find the anchored regions (marked with TDOC-COMMENT html
  comments inline below) and modify them accordingly.

  ## Comments included in this export
  ${open.length} comment(s).
`;
  open.forEach((c, i) => {
    const who = c.author?.login ? `@${forHtmlComment(c.author.login)}` : 'anonymous';
    banner += `\n  [${i + 1}] ${who} ${describeAnchor(c)}\n    "${forHtmlComment(c.text.replace(/\n/g, ' '))}"\n${reactionsText(c.reactions)}`;
    for (const r of c.replies) {
      const rWho = r.author?.login ? `@${forHtmlComment(r.author.login)}` : 'anonymous';
      banner += `      ↳ ${rWho}: "${forHtmlComment(r.text.replace(/\n/g, ' '))}"\n${reactionsText(r.reactions).replace(/^/gm, '  ')}`;
    }
  });
  return banner + `\n  ===== end octo-doc fork export =====\n-->\n`;
}

/** Wrap the first occurrence of each text anchor with TDOC-COMMENT markers. */
function markAnchoredText(html: string, open: CommentSnapshot[]): string {
  let out = html;
  for (const c of open) {
    const needle = c.anchor && 'text' in c.anchor ? c.anchor.text : undefined;
    if (!needle || needle.length < 2) continue;
    const idx = out.indexOf(needle);
    if (idx === -1) continue;
    const marker = `<!--TDOC-COMMENT id="${forHtmlComment(c.id)}" by="${forHtmlComment(c.author?.login ?? 'anonymous')}"-->${needle}<!--/TDOC-COMMENT-->`;
    out = out.slice(0, idx) + marker + out.slice(idx + needle.length);
  }
  return out;
}

/** Assemble the full export/fork document. */
export function buildForkExport(input: ForkExportInput): string {
  const open = input.comments.filter((c) => c.status !== 'resolved');
  const banner = buildBanner(input, open);
  const jsonBlock = `<script type="application/json" id="tdoc-fork-comments">${safeJsonForScript({
    slug: input.slug,
    version: input.version,
    exported: new Date().toISOString(),
    comments: open,
  })}</script>\n`;

  let body = markAnchoredText(input.html, open);
  if (input.kind === 'fork') {
    body = injectOverlayCfg(body, {
      slug: input.slug,
      version: input.version,
      identity: null,
      authConfigured: false,
      mode: 'fork',
      originalSlug: input.slug,
    });
  }
  return banner + jsonBlock + body;
}
