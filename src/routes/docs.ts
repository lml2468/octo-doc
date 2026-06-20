/**
 * Document HTTP routes: publish, render, export/fork, version listing, delete.
 * Thin handlers — validation + shape only; all logic lives in {@link DocService}
 * and {@link CommentService}.
 */
import { Hono, type Context } from 'hono';
import { getCookie } from 'hono/cookie';
import type { AppEnv } from '../http-context.js';
import { requireSlug } from '../config.js';
import { NotFoundError, ValidationError } from '../errors.js';
import { requireWriteAuth, maybeRequireReadAuth } from '../middleware/auth.js';
import { injectOverlayCfg, toOverlayIdentity } from '../core/render.js';
import { buildForkExport } from './fork-export.js';
import type { Comment } from '../core/index.js';
import type { DocMeta } from '../storage/types.js';

interface PublishBody {
  slug: unknown;
  html: string | undefined;
  version: number | undefined;
  title: string | undefined;
  meta: Partial<DocMeta> | undefined;
  localComments: Comment[] | undefined;
}

/** Extract publish input from a multipart/form-data request. */
async function readMultipartBody(c: Context<AppEnv>): Promise<PublishBody> {
  const form = await c.req.parseBody();
  const file = form.file;
  const html =
    file && typeof file === 'object' && 'text' in file
      ? await (file as File).text()
      : typeof form.html === 'string'
        ? form.html
        : undefined;
  return {
    slug: form.slug,
    html,
    version: form.version ? Number(form.version) : undefined,
    title: typeof form.title === 'string' ? form.title : undefined,
    meta: undefined,
    localComments: undefined,
  };
}

/** Extract publish input from a JSON request (legacy CLI path). */
async function readJsonBody(c: Context<AppEnv>): Promise<PublishBody> {
  const body = (await c.req.json().catch(() => ({}))) as Record<string, unknown>;
  return {
    slug: body.slug,
    html: typeof body.html === 'string' ? body.html : undefined,
    version: typeof body.version === 'number' ? body.version : undefined,
    title: typeof body.title === 'string' ? body.title : undefined,
    meta: body.meta as Partial<DocMeta> | undefined,
    localComments: body.comments as Comment[] | undefined,
  };
}

/** Extract publish input from either multipart/form-data or JSON. */
function readPublishBody(c: Context<AppEnv>): Promise<PublishBody> {
  const ct = (c.req.header('content-type') ?? '').toLowerCase();
  return ct.includes('multipart/form-data') ? readMultipartBody(c) : readJsonBody(c);
}

/** Build the document routes. */
export function docRoutes(): Hono<AppEnv> {
  const app = new Hono<AppEnv>();

  const publish = async (c: Context<AppEnv>) => {
    const body = await readPublishBody(c);
    const slug = requireSlug(body.slug);
    if (body.html === undefined) throw new ValidationError('html (file) required', 'html_required');
    const result = await c.var.docs.publish({
      slug,
      html: body.html,
      ...(body.version !== undefined ? { version: body.version } : {}),
      ...(body.title !== undefined ? { title: body.title } : {}),
      ...(body.meta !== undefined ? { meta: body.meta } : {}),
      ...(body.localComments !== undefined ? { localComments: body.localComments } : {}),
    });
    return c.json({ ok: true, ...result });
  };

  app.post('/api/docs', requireWriteAuth, publish);
  app.post('/api/upload', requireWriteAuth, publish); // legacy alias
  // /api/docs is a write-only endpoint: a GET (or any non-POST) still requires
  // the write token, so an unauthenticated probe gets a clear 401, not a 404.
  app.all('/api/docs', requireWriteAuth, (c) => c.json({ error: 'method_not_allowed' }, 405));

  app.get('/api/docs/:slug/versions', async (c) => {
    const slug = requireSlug(c.req.param('slug'));
    const result = await c.var.docs.listVersions(slug);
    if (!result) throw new NotFoundError();
    return c.json(result);
  });

  app.on(['GET', 'HEAD'], '/d/:slug/v/:version', maybeRequireReadAuth, async (c) => {
    const slug = requireSlug(c.req.param('slug'));
    const vStr = c.req.param('version');
    if (!/^\d+$/.test(vStr)) throw new NotFoundError();
    const version = Number(vStr);
    const data = await c.var.docs.render(slug, version);
    if (!data) throw new NotFoundError(`Not found: ${slug} v${vStr}`);

    const session = await c.var.auth.getSession(getCookie(c, 'tdoc_sid'));
    const mode = c.var.config.githubClientId ? 'published' : 'local';
    const html = injectOverlayCfg(data.html, {
      slug,
      version,
      identity: toOverlayIdentity(session),
      isOwner: c.var.auth.isOwner(session),
      authConfigured: !!c.var.config.githubClientId,
      mode,
      versions: data.versions ?? [{ n: version }],
    });
    return c.html(html);
  });

  app.get('/d/:slug/v/:version/:kind{export|fork}', maybeRequireReadAuth, async (c) => {
    const slug = requireSlug(c.req.param('slug'));
    const vStr = c.req.param('version');
    if (!/^\d+$/.test(vStr)) throw new NotFoundError();
    const version = Number(vStr);
    const data = await c.var.docs.render(slug, version);
    if (!data) throw new NotFoundError(`Not found: ${slug} v${vStr}`);

    const kind = c.req.param('kind') as 'export' | 'fork';
    const list = await c.var.comments.list(slug, version);
    const finalHtml = buildForkExport({ slug, version, html: data.html, comments: list, kind });

    const dl = c.req.query('download');
    const forceDownload = dl === '1' || (kind === 'export' && dl !== '0');
    c.header('Content-Type', 'text/html; charset=utf-8');
    if (forceDownload)
      c.header('Content-Disposition', `attachment; filename="${slug}-v${vStr}-fork.html"`);
    return c.body(finalHtml);
  });

  app.delete('/api/doc', requireWriteAuth, async (c) => {
    const slug = requireSlug(c.req.query('slug'));
    await c.var.docs.remove(slug);
    return c.json({ ok: true });
  });

  return app;
}
