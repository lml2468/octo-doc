/**
 * DocService — publish, render-data, version listing, and deletion of documents.
 *
 * Publishing is the critical path: stamp artifacts (byte-equivalent to upstream),
 * write the immutable blob atomically, bump the monotonic version list, and
 * reconcile/merge comments. Routes call this; it owns no HTTP concerns.
 */
import type { BlobStore } from '../storage/types.js';
import type { MetadataStore, DocMeta } from '../storage/types.js';
import type { Comment } from '../core/comment.types.js';
import { stampAids } from '../core/stamp.js';
import { PayloadTooLargeError, ValidationError } from '../errors.js';
import type { CommentService } from './comment-service.js';

/** Input to {@link DocService.publish}. */
export interface PublishInput {
  slug: string;
  html: string;
  /** Explicit version (legacy JSON path); omit to auto-increment. */
  version?: number;
  title?: string;
  meta?: Partial<DocMeta>;
  localComments?: Comment[];
}

/** Result of a successful publish. */
export interface PublishResult {
  slug: string;
  version: number;
  url: string;
  size: number;
  aids: number;
  mergedComments: number;
}

/** Render data for a document version. */
export interface RenderData {
  html: string;
  versions: { n: number; created?: string | null }[] | null;
}

export class DocService {
  constructor(
    private readonly blobs: BlobStore,
    private readonly meta: MetadataStore,
    private readonly comments: CommentService,
    private readonly opts: { baseUrl: string; maxHtmlBytes: number },
  ) {}

  /**
   * Publish a new (or explicitly-versioned) document.
   *
   * @throws {ValidationError} if the HTML is empty
   * @throws {PayloadTooLargeError} if the HTML exceeds the configured cap
   */
  async publish(input: PublishInput): Promise<PublishResult> {
    if (typeof input.html !== 'string' || input.html.length === 0) {
      throw new ValidationError('html (file) required', 'html_required');
    }
    if (Buffer.byteLength(input.html) > this.opts.maxHtmlBytes) {
      throw new PayloadTooLargeError(
        `document exceeds ${this.opts.maxHtmlBytes} bytes`,
        'html_too_large',
      );
    }

    const version = await this.resolveVersion(input.slug, input.version);
    const { html: stamped, aids } = stampAids(input.html);

    const put = await this.blobs.putDoc(input.slug, version, stamped);
    if (!(await this.blobs.headDoc(input.slug, version))) {
      throw new ValidationError('blob write did not persist', 'blob_write_lost');
    }

    await this.upsertMeta(input, version);

    const merge = await this.comments.publishMerge(input.slug, {
      localComments: input.localComments ?? [],
      aids,
      version,
    });
    const mergedComments = (merge.body as { mergedComments?: number }).mergedComments ?? 0;

    return {
      slug: input.slug,
      version,
      url: `${this.opts.baseUrl}/d/${input.slug}/v/${version}`,
      size: put.size,
      aids: aids.length,
      mergedComments,
    };
  }

  /** Fetch raw stored HTML + the version list for rendering, or null if absent. */
  async render(slug: string, version: number): Promise<RenderData | null> {
    const html = await this.blobs.getDoc(slug, version);
    if (html == null) return null;
    const meta = await this.meta.getMeta(slug);
    const versions =
      meta && Array.isArray(meta.versions)
        ? meta.versions.map((v) => ({ n: v.n, created: v.created ?? null }))
        : null;
    return { html, versions };
  }

  /** List versions for a slug (meta-derived, falling back to blob scan). */
  async listVersions(slug: string): Promise<{
    slug: string;
    title: string;
    versions: { n: number; created: string | null }[];
  } | null> {
    const meta = await this.meta.getMeta(slug);
    const blobVersions = await this.blobs.listVersions(slug);
    if (!meta && blobVersions.length === 0) return null;
    const versions =
      meta && Array.isArray(meta.versions) && meta.versions.length
        ? meta.versions.map((v) => ({ n: v.n, created: v.created ?? null }))
        : blobVersions.map((n) => ({ n, created: null }));
    return { slug, title: meta?.title ?? slug, versions };
  }

  /** Delete all versions, metadata, and comments for a slug. */
  async remove(slug: string): Promise<void> {
    await this.blobs.deleteDoc(slug);
    await this.meta.deleteMeta(slug);
    await this.comments.wipe(slug);
  }

  /**
   * List all docs with a reachable latest version, for the owner catalog. A doc
   * whose latest blob is missing is skipped so the catalog never links to a 404.
   */
  async listAllForOwner(): Promise<{ slug: string; title: string; latest: number }[]> {
    const all = await this.meta.listMeta();
    // Probe each doc's latest blob in parallel — independent existence checks.
    const checked = await Promise.all(
      all.map(async ({ slug, meta }) => {
        const latest = meta.versions?.[meta.versions.length - 1]?.n ?? 1;
        const exists = await this.blobs.headDoc(slug, latest);
        return exists ? { slug, title: meta.title ?? slug, latest } : null;
      }),
    );
    return checked.filter((d): d is { slug: string; title: string; latest: number } => d !== null);
  }

  /** Next version = max existing + 1, unless an explicit version was given. */
  private async resolveVersion(slug: string, explicit?: number): Promise<number> {
    if (explicit && Number.isFinite(explicit)) return Number(explicit);
    const existing = await this.blobs.listVersions(slug);
    return (existing.length ? Math.max(...existing) : 0) + 1;
  }

  /** Merge the new version into the slug's monotonic version list + metadata. */
  private async upsertMeta(input: PublishInput, version: number): Promise<void> {
    const prev: DocMeta = (await this.meta.getMeta(input.slug)) ?? {
      slug: input.slug,
      title: input.slug,
      versions: [],
    };
    const versions = Array.isArray(prev.versions) ? prev.versions.slice() : [];
    if (!versions.some((v) => v.n === version))
      versions.push({ n: version, created: new Date().toISOString() });
    versions.sort((a, b) => a.n - b.n);
    await this.meta.putMeta(input.slug, {
      ...prev,
      ...input.meta,
      slug: input.slug,
      title: input.title ?? input.meta?.title ?? prev.title ?? input.slug,
      versions,
    });
  }
}
