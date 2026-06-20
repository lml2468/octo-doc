// HTML helpers + overlay injection — ported from worker.js. The overlay JS is
// loaded once at module init (replacing the Worker's build-time bundling: the
// Worker inlined overlay.js into a `__TDOC_OVERLAY_JS__` placeholder; we just
// read the file at runtime — same bytes reach the browser).
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const OVERLAY_JS = readFileSync(join(__dirname, '..', 'overlay.js'), 'utf8');

// Escape `</script>` and HTML comment terminators so a stray value inside a
// JSON payload can't break out of the surrounding <script> block.
export function safeJsonForScript(obj) {
  return JSON.stringify(obj).replace(/<\/script>/gi, '<\\/script>').replace(/<!--/g, '<\\!--');
}

// Full HTML escaping for interpolating untrusted strings into markup.
export function escapeHtml(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, c =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

// Make an untrusted string safe to interpolate inside an HTML comment.
export function forHtmlComment(s) {
  return String(s == null ? '' : s).replace(/--/g, '-\\-');
}

// Inject the overlay boot + cfg into a document. Single source of truth for
// "put window.__TDOC__ + overlay.js before </body>".
export function injectOverlayCfg(rawHtml, cfg) {
  const inject =
    `<script>window.__TDOC__ = ${safeJsonForScript(cfg)};</script>\n` +
    `<script>${OVERLAY_JS}</script>`;
  if (rawHtml.includes('</body>')) return rawHtml.replace('</body>', `${inject}\n</body>`);
  return rawHtml + inject;
}

export function injectOverlay(rawHtml, slug, version, identity, versions, isOwner, mode = 'published') {
  return injectOverlayCfg(rawHtml, {
    slug, version,
    identity: identity || null,
    isOwner: !!isOwner,
    authConfigured: true,
    mode,
    versions: Array.isArray(versions) && versions.length ? versions : [{ n: version }],
  });
}
