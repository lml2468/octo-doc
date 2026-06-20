/**
 * Public pages: a neutral landing page at `/` (no catalog — docs are link-only)
 * and an owner-only catalog at `/me`.
 */
import { Hono } from 'hono';
import { getCookie } from 'hono/cookie';
import type { AppEnv } from '../http-context.js';
import type { Session } from '../storage/types.js';
import { escapeHtml } from '../core/render.js';

/** Build the landing + catalog routes. */
export function pageRoutes(): Hono<AppEnv> {
  const app = new Hono<AppEnv>();

  app.get('/', (c) => c.html(landingHtml(c.var.config.repoUrl)));

  app.get('/me', async (c) => {
    const session = await c.var.auth.getSession(getCookie(c, 'tdoc_sid'));
    if (!c.var.auth.isOwner(session)) return c.redirect(c.var.config.repoUrl, 302);
    const all = await c.var.docs.listAllForOwner();
    return c.html(catalogHtml(session, all));
  });

  return app;
}

/** Neutral, catalog-free landing page. */
function landingHtml(repo: string): string {
  const repoLabel = repo.replace(/^https?:\/\//, '');
  return `<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>octo-doc</title>
<style>
  body { font: 15px system-ui, -apple-system, sans-serif; min-height: 100vh; margin: 0;
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    color: #111; background: #fff; gap: 10px; }
  h1 { font-size: 30px; margin: 0; color: #1652f0; }
  p { color: #666; margin: 0; }
  a { color: #1652f0; text-decoration: none; }
  a:hover { text-decoration: underline; }
  .sub { margin-top: 14px; font-size: 13px; color: #888; }
</style></head><body>
  <h1>octo-doc</h1>
  <p>Prompt-native, commentable documents. Self-hosted.</p>
  <p class="sub">Open a document from its shared link ·
    <a href="${escapeHtml(repo)}">${escapeHtml(repoLabel)}</a></p>
</body></html>`;
}

/** Owner-only doc catalog. */
function catalogHtml(
  session: Session | null,
  docs: { slug: string; title: string; latest: number }[],
): string {
  const rows = docs
    .map(
      (d) => `<tr>
      <td><a href="/d/${encodeURIComponent(d.slug)}/v/${d.latest}">${escapeHtml(d.title)}</a></td>
      <td>${escapeHtml(d.slug)}</td>
      <td>v${d.latest}</td>
    </tr>`,
    )
    .join('');
  const who = session?.login ? ` · signed in as <b>${escapeHtml(session.login)}</b>` : '';
  return `<!doctype html><html><head><meta charset="utf-8"><title>octo-doc</title>
<style>
  body { font: 15px system-ui, -apple-system, sans-serif; max-width: 760px; margin: 60px auto; padding: 0 20px; color: #111; }
  h1 { font-size: 28px; margin: 0 0 4px; color: #1652f0; }
  table { width: 100%; border-collapse: collapse; }
  th, td { text-align: left; padding: 10px 12px; border-bottom: 1px solid #eee; }
  th { font-size: 12px; text-transform: uppercase; color: #888; letter-spacing: 0.04em; }
  a { color: #1652f0; text-decoration: none; }
  a:hover { text-decoration: underline; }
  .empty { color: #888; padding: 40px 0; text-align: center; }
  .who { color: #888; font-size: 13px; margin: 0 0 32px; }
  .who b { color: #444; font-weight: 600; }
</style></head><body>
<h1>My docs</h1>
<p class="who">Documents hosted on this server${who}.</p>
${
  rows.length === 0
    ? '<p class="empty">No published docs yet.</p>'
    : `<table><thead><tr><th>Title</th><th>Slug</th><th>Version</th></tr></thead><tbody>${rows}</tbody></table>`
}
</body></html>`;
}
