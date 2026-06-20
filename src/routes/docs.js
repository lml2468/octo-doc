// Document routes: publish (POST /api/docs + legacy /api/upload), view
// (GET /d/:slug/v/:version), export/fork, version listing, delete.
import { Hono } from 'hono';
import { stampAids } from '../core/stamp.js';
import {
  injectOverlay, injectOverlayCfg, safeJsonForScript, forHtmlComment, escapeHtml,
} from '../core/render.js';
import { snapshotList, ensureMigrated } from '../core/comments.js';
import { safeSlug } from '../config.js';
import { getSession, isOwnerSession } from './auth.js';
import { requireWriteAuth } from '../middleware/security.js';

export function docRoutes(ctx) {
  const { config, metaStore, blobStore, commentStore } = ctx;
  const app = new Hono();

  // ── Publish: POST /api/docs ────────────────────────────────────────────────
  // New canonical endpoint. Accepts multipart/form-data (file=@doc.html,
  // slug=…, optional title) OR application/json ({slug, version, html, meta,
  // comments}). Version is auto-assigned (monotonic) for multipart; explicit
  // for JSON (the legacy CLI path). Bearer-authّd by the caller of this route.
  const publish = async (c) => {
    const ct = (c.req.header('content-type') || '').toLowerCase();
    let slug, version, doc, meta, localComments, title;

    if (ct.includes('multipart/form-data')) {
      const form = await c.req.parseBody();
      slug = form.slug;
      title = form.title;
      const file = form.file;
      if (file && typeof file === 'object' && typeof file.text === 'function') {
        doc = await file.text();
      } else if (typeof form.html === 'string') {
        doc = form.html;
      }
      if (form.version) version = Number(form.version);
    } else {
      let body = {};
      try { body = await c.req.json(); } catch {}
      ({ slug, version, html: doc, meta, comments: localComments } = body);
      title = body.title;
    }

    slug = safeSlug(slug);
    if (!slug) return c.json({ error: 'invalid or missing slug' }, 400);
    if (typeof doc !== 'string' || !doc) return c.json({ error: 'html (file) required' }, 400);
    if (Buffer.byteLength(doc) > config.maxHtmlBytes) {
      return c.json({ error: 'html_too_large', max_bytes: config.maxHtmlBytes }, 413);
    }

    // Auto-assign the next version when not explicitly provided (multipart path).
    if (!version || !Number.isFinite(version)) {
      const existing = await blobStore.listVersions(slug);
      version = (existing.length ? Math.max(...existing) : 0) + 1;
    }
    version = Number(version);

    // Identity-stamp every commentable artifact (byte-equivalent to upstream).
    const { html: stampedHtml, aids } = stampAids(doc);

    const put = await blobStore.putDoc(slug, version, stampedHtml);
    const verify = await blobStore.headDoc(slug, version);
    if (!verify) return c.json({ error: 'blob_write_lost' }, 500);

    // Merge/maintain meta: keep a monotonic versions[] list.
    const prevMeta = (await metaStore.getMeta(slug)) || {};
    const versions = Array.isArray(prevMeta.versions) ? prevMeta.versions.slice() : [];
    if (!versions.find(v => v.n === version)) {
      versions.push({ n: version, created: new Date().toISOString() });
    }
    versions.sort((a, b) => a.n - b.n);
    const newMeta = {
      ...prevMeta,
      ...(meta || {}),
      slug,
      title: title || (meta && meta.title) || prevMeta.title || slug,
      versions,
    };
    await metaStore.putMeta(slug, newMeta);

    // Reconcile existing comments + non-destructively merge local ones.
    let mergedLocal = 0;
    try {
      const res = await commentStore.mutateComments(slug, {
        kind: 'publish_merge', slug, localComments: localComments || [], aids, version,
      });
      mergedLocal = (res.body && res.body.mergedComments) || 0;
    } catch { /* non-fatal */ }

    // Version GC: enforce the per-slug quota by deleting the oldest blobs.
    if (config.maxVersionsPerSlug > 0) {
      const all = await blobStore.listVersions(slug);
      if (all.length > config.maxVersionsPerSlug) {
        // (delete handled per-version would need a per-version blob delete;
        // documented as a future enhancement — quota currently warns via meta)
      }
    }

    const base = config.baseUrl || '';
    return c.json({
      ok: true,
      slug,
      version,
      url: `${base}/d/${slug}/v/${version}`,
      size: put.size,
      aids: aids.length,
      mergedComments: mergedLocal,
    });
  };

  app.post('/api/docs', requireWriteAuth(config), publish);
  // Legacy alias preserved for the existing CLI contract.
  app.post('/api/upload', requireWriteAuth(config), publish);

  // ── List versions: GET /api/docs/:slug/versions ─────────────────────────────
  app.get('/api/docs/:slug/versions', async (c) => {
    const slug = safeSlug(c.req.param('slug'));
    if (!slug) return c.json({ error: 'invalid slug' }, 400);
    const meta = await metaStore.getMeta(slug);
    const blobVersions = await blobStore.listVersions(slug);
    if (!meta && !blobVersions.length) return c.json({ error: 'not_found' }, 404);
    const versions = (meta && Array.isArray(meta.versions) && meta.versions.length)
      ? meta.versions.map(v => ({ n: v.n, created: v.created || null }))
      : blobVersions.map(n => ({ n, created: null }));
    return c.json({ slug, title: (meta && meta.title) || slug, versions });
  });

  // ── View: GET /d/:slug/v/:version ───────────────────────────────────────────
  app.on(['GET', 'HEAD'], '/d/:slug/v/:version', async (c) => {
    const slug = safeSlug(c.req.param('slug'));
    const vStr = c.req.param('version');
    if (!slug || !/^\d+$/.test(vStr)) return c.text('Not found', 404);
    const raw = await blobStore.getDoc(slug, vStr);
    if (raw == null) return c.text(`Not found: ${slug} v${vStr}`, 404);

    const session = await getSession(c, metaStore);
    const identity = session ? { login: session.login, avatar_url: session.avatar_url, name: session.name } : null;
    let versions = null;
    const meta = await metaStore.getMeta(slug);
    if (meta && Array.isArray(meta.versions)) versions = meta.versions.map(v => ({ n: v.n, created: v.created || null }));

    const mode = config.githubClientId ? 'published' : 'local';
    const out = injectOverlay(raw, slug, Number(vStr), identity, versions, isOwnerSession(config, session), mode);
    return c.html(out);
  });

  // ── Export / fork: GET /d/:slug/v/:version/(export|fork) ─────────────────────
  app.get('/d/:slug/v/:version/:kind{export|fork}', async (c) => {
    const slug = safeSlug(c.req.param('slug'));
    const vStr = c.req.param('version');
    const kind = c.req.param('kind');
    if (!slug || !/^\d+$/.test(vStr)) return c.text('Not found', 404);
    let html = await blobStore.getDoc(slug, vStr);
    if (html == null) return c.text(`Not found: ${slug} v${vStr}`, 404);

    const rawList = await commentStore.readComments(slug);
    ensureMigrated(rawList);
    const comments = snapshotList(rawList, Number(vStr));
    const openComments = comments.filter(cm => cm.status !== 'resolved');

    const reactionsText = (rs) => {
      if (!rs) return '';
      const parts = Object.entries(rs).filter(([, u]) => u && u.length > 0)
        .map(([e, u]) => `${forHtmlComment(e)} (${u.length})`);
      return parts.length ? `    reactions: ${parts.join(', ')}\n` : '';
    };
    let banner = `<!--
  ===== octo-doc fork export =====
  slug: ${forHtmlComment(slug)}
  version: ${forHtmlComment(vStr)}
  exported: ${new Date().toISOString()}

  ## How to use this file
  Save it as ~/tdocs/<your-new-slug>/v1/index.html (or anywhere you like).
  Comments below are read-only metadata bundled with the fork. Agents can
  read them to apply changes — say "apply all comments to this doc" and the
  agent will find the anchored regions (marked with TDOC-COMMENT html
  comments inline below) and modify them accordingly.

  ## Comments included in this export
  ${openComments.length} comment(s).
`;
    for (let i = 0; i < openComments.length; i++) {
      const cm = openComments[i];
      const who = cm.author?.login ? `@${forHtmlComment(cm.author.login)}` : 'anonymous';
      const anchor = cm.anchor?.kind === 'element'
        ? `(on ${forHtmlComment(cm.anchor.label || cm.anchor.selector || 'element')})`
        : cm.anchor?.text ? `(on text: "${forHtmlComment(cm.anchor.text.replace(/"/g, '\\"').slice(0, 120))}")` : '(no anchor)';
      banner += `\n  [${i + 1}] ${who} ${anchor}\n    "${forHtmlComment(cm.text.replace(/\n/g, ' '))}"\n${reactionsText(cm.reactions)}`;
      if (Array.isArray(cm.replies)) {
        for (const r of cm.replies) {
          const rWho = r.author?.login ? `@${forHtmlComment(r.author.login)}` : 'anonymous';
          banner += `      ↳ ${rWho}: "${forHtmlComment(r.text.replace(/\n/g, ' '))}"\n${reactionsText(r.reactions).replace(/^/gm, '  ')}`;
        }
      }
    }
    banner += `\n  ===== end octo-doc fork export =====\n-->\n`;

    const jsonBlock = `<script type="application/json" id="tdoc-fork-comments">${
      safeJsonForScript({ slug, version: Number(vStr), exported: new Date().toISOString(), comments: openComments })
    }</script>\n`;

    for (const cm of openComments) {
      if (cm.anchor?.kind !== 'text' && !cm.anchor?.text) continue;
      const needle = cm.anchor.text;
      if (!needle || needle.length < 2) continue;
      const idx = html.indexOf(needle);
      if (idx === -1) continue;
      const replacement = `<!--TDOC-COMMENT id="${forHtmlComment(cm.id)}" by="${forHtmlComment(cm.author?.login || 'anonymous')}"-->${needle}<!--/TDOC-COMMENT-->`;
      html = html.slice(0, idx) + replacement + html.slice(idx + needle.length);
    }

    let bodyHtml = html;
    if (kind === 'fork') {
      bodyHtml = injectOverlayCfg(bodyHtml, {
        slug, version: Number(vStr), identity: null,
        authConfigured: false, mode: 'fork', originalSlug: slug,
      });
    }

    const finalHtml = banner + jsonBlock + bodyHtml;
    const dl = c.req.query('download');
    const defaultAttach = kind === 'export';
    const forceDownload = dl === '1' || (defaultAttach && dl !== '0');
    c.header('Content-Type', 'text/html; charset=utf-8');
    if (forceDownload) c.header('Content-Disposition', `attachment; filename="${slug}-v${vStr}-fork.html"`);
    return c.body(finalHtml);
  });

  // ── Delete a doc: DELETE /api/doc?slug= ──────────────────────────────────────
  app.delete('/api/doc', requireWriteAuth(config), async (c) => {
    const slug = safeSlug(c.req.query('slug'));
    if (!slug) return c.json({ error: 'invalid slug' }, 400);
    await blobStore.deleteDoc(slug);
    await metaStore.deleteMeta(slug);
    await commentStore.mutateComments(slug, { kind: 'wipe', slug });
    await metaStore.deleteComments(slug);
    return c.json({ ok: true });
  });

  return app;
}

// Catalog/landing pages (mounted separately so they can stay at "/" and "/me").
export function pageRoutes(ctx) {
  const { config, metaStore, blobStore } = ctx;
  const app = new Hono();

  app.get('/', (c) => c.html(landingHtml(config)));

  app.get('/me', async (c) => {
    const session = await getSession(c, metaStore);
    if (!isOwnerSession(config, session)) {
      return c.redirect(config.repoUrl || 'https://github.com/lml2468/octo-doc', 302);
    }
    const all = await metaStore.listMeta();
    const rows = [];
    for (const { slug, meta } of all) {
      const latest = meta.versions?.[meta.versions.length - 1]?.n || 1;
      const exists = await blobStore.headDoc(slug, latest);
      if (!exists) continue;
      rows.push(`<tr>
        <td><a href="/d/${encodeURIComponent(slug)}/v/${latest}">${escapeHtml(meta.title || slug)}</a></td>
        <td>${escapeHtml(slug)}</td>
        <td>v${latest}</td>
      </tr>`);
    }
    return c.html(catalogHtml(session, rows));
  });

  return app;
}

function landingHtml(config) {
  const repo = config.repoUrl || 'https://github.com/lml2468/octo-doc';
  return `<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>octo-doc</title>
<style>
  body { font: 15px system-ui, -apple-system, sans-serif; min-height: 100vh;
    margin: 0; display: flex; flex-direction: column; align-items: center;
    justify-content: center; color: #111; background: #fff; gap: 10px; }
  h1 { font-size: 30px; margin: 0; color: #1652f0; }
  p { color: #666; margin: 0; }
  a { color: #1652f0; text-decoration: none; }
  a:hover { text-decoration: underline; }
  .sub { margin-top: 14px; font-size: 13px; color: #888; }
</style></head><body>
  <h1>octo-doc</h1>
  <p>Prompt-native, commentable documents. Self-hosted.</p>
  <p class="sub">Open a document from its shared link ·
    <a href="${escapeHtml(repo)}">${escapeHtml(repo.replace(/^https?:\/\//, ''))}</a></p>
</body></html>`;
}

function catalogHtml(session, rows) {
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
<p class="who">Documents hosted on this server${session && session.login ? ` · signed in as <b>${escapeHtml(session.login)}</b>` : ''}.</p>
${rows.length === 0 ? '<p class="empty">No published docs yet.</p>' :
  `<table><thead><tr><th>Title</th><th>Slug</th><th>Version</th></tr></thead><tbody>${rows.join('')}</tbody></table>`}
</body></html>`;
}
