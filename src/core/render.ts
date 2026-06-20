/**
 * HTML helpers + overlay injection.
 *
 * The browser overlay (`overlay.js`) is loaded once at module init and injected
 * before `</body>`. This replaces the Worker's build-time inlining; the bytes
 * reaching the browser are identical.
 */
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));

/** Locate `overlay.js` next to the running module (dist/) or in src/ during dev. */
function loadOverlay(): string {
  for (const candidate of [join(here, 'overlay.js'), join(here, '..', 'overlay.js')]) {
    try {
      return readFileSync(candidate, 'utf8');
    } catch {
      // try next candidate
    }
  }
  throw new Error('overlay.js not found next to render module');
}

const OVERLAY_JS = loadOverlay();

/** The boot config injected as `window.__TDOC__` for the overlay. */
export interface OverlayConfig {
  slug: string;
  version: number;
  identity: OverlayIdentity | null;
  mode: 'published' | 'local' | 'fork';
  authConfigured: boolean;
  isOwner?: boolean;
  versions?: { n: number; created?: string | null }[];
  originalSlug?: string;
}

/** Minimal identity shape the overlay renders in its toolbar. */
export interface OverlayIdentity {
  login: string;
  avatar_url?: string | null;
  name?: string;
}

/** Build an {@link OverlayIdentity} from session-ish fields, honoring exactOptional. */
export function toOverlayIdentity(
  source: { login: string; avatar_url?: string | null; name?: string } | null,
): OverlayIdentity | null {
  if (!source) return null;
  const id: OverlayIdentity = { login: source.login };
  if (source.avatar_url != null) id.avatar_url = source.avatar_url;
  if (source.name != null) id.name = source.name;
  return id;
}

/** Escape `</script>` and HTML-comment openers so JSON can't break out of a `<script>`. */
export function safeJsonForScript(obj: unknown): string {
  return JSON.stringify(obj)
    .replace(/<\/script>/gi, '<\\/script>')
    .replace(/<!--/g, '<\\!--');
}

/** Full HTML escaping for interpolating untrusted strings into markup. */
export function escapeHtml(s: string | null | undefined): string {
  return (s ?? '').replace(
    /[&<>"']/g,
    (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c] as string,
  );
}

/** Neutralize `--` so an untrusted string can't terminate an HTML comment. */
export function forHtmlComment(s: string | null | undefined): string {
  return (s ?? '').replace(/--/g, '-\\-');
}

/** Inject the overlay boot script + config before `</body>` (or append). */
export function injectOverlayCfg(rawHtml: string, cfg: OverlayConfig): string {
  const inject = `<script>window.__TDOC__ = ${safeJsonForScript(cfg)};</script>\n<script>${OVERLAY_JS}</script>`;
  return rawHtml.includes('</body>')
    ? rawHtml.replace('</body>', `${inject}\n</body>`)
    : rawHtml + inject;
}
