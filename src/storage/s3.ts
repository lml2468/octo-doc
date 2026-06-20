/**
 * S3 / MinIO {@link BlobStore} (AWS SDK v3). Same contract as the FS adapter.
 * Works against AWS S3, MinIO, or R2 (set `S3_ENDPOINT` + `S3_FORCE_PATH_STYLE=1`
 * for MinIO). Selected with `STORAGE=...+s3`.
 *
 * Object keys hash the slug (`docs/<hash>/v<version>/index.html`) so an
 * unexpected slug can never produce a key outside the `docs/` prefix. S3 PUT is
 * atomic by nature — there is no half-written object.
 */
import type { Config } from '../config.js';
import type { BlobStore } from './types.js';
import { hashSlug } from './keys.js';

/** Narrow structural types for the S3 client surface we use (avoids `any`). */
interface S3Like {
  send(command: unknown): Promise<{
    Body?: { transformToString?(enc: string): Promise<string> } & AsyncIterable<Uint8Array>;
    ContentLength?: number;
    Contents?: { Key?: string }[];
    IsTruncated?: boolean;
    NextContinuationToken?: string;
    $metadata?: { httpStatusCode?: number };
  }>;
}

interface S3Module {
  S3Client: new (cfg: unknown) => S3Like;
  PutObjectCommand: new (cfg: unknown) => unknown;
  GetObjectCommand: new (cfg: unknown) => unknown;
  HeadObjectCommand: new (cfg: unknown) => unknown;
  ListObjectsV2Command: new (cfg: unknown) => unknown;
  DeleteObjectsCommand: new (cfg: unknown) => unknown;
}

function isNotFound(err: unknown): boolean {
  const e = err as { $metadata?: { httpStatusCode?: number }; name?: string };
  return e?.$metadata?.httpStatusCode === 404 || e?.name === 'NoSuchKey' || e?.name === 'NotFound';
}

async function bodyToString(
  body: NonNullable<Awaited<ReturnType<S3Like['send']>>['Body']>,
): Promise<string> {
  if (typeof body.transformToString === 'function') return body.transformToString('utf8');
  const chunks: Buffer[] = [];
  for await (const c of body) chunks.push(Buffer.from(c));
  return Buffer.concat(chunks).toString('utf8');
}

/** Open the S3/MinIO blob store from `S3_*` env, ensuring nothing leaks the SDK types. */
export async function makeS3BlobStore(config: Config): Promise<BlobStore> {
  const s3 = (await import('@aws-sdk/client-s3')) as unknown as S3Module;
  const env = process.env;
  const bucket = env.S3_BUCKET ?? 'octo-doc';
  const client = new s3.S3Client({
    region: env.S3_REGION ?? env.AWS_REGION ?? 'us-east-1',
    endpoint: env.S3_ENDPOINT,
    forcePathStyle: /^(1|true|yes)$/i.test(env.S3_FORCE_PATH_STYLE ?? ''),
    credentials:
      (env.S3_ACCESS_KEY_ID ?? env.AWS_ACCESS_KEY_ID)
        ? {
            accessKeyId: (env.S3_ACCESS_KEY_ID ?? env.AWS_ACCESS_KEY_ID)!,
            secretAccessKey: (env.S3_SECRET_ACCESS_KEY ?? env.AWS_SECRET_ACCESS_KEY)!,
          }
        : undefined,
  });
  void config;

  const prefixFor = (slug: string): string => `docs/${hashSlug(slug)}`;
  const keyFor = (slug: string, version: number): string =>
    `${prefixFor(slug)}/v${version}/index.html`;

  return {
    async putDoc(slug, version, html) {
      await client.send(
        new s3.PutObjectCommand({
          Bucket: bucket,
          Key: keyFor(slug, version),
          Body: html,
          ContentType: 'text/html; charset=utf-8',
        }),
      );
      return { size: Buffer.byteLength(html) };
    },
    async getDoc(slug, version) {
      try {
        const r = await client.send(
          new s3.GetObjectCommand({ Bucket: bucket, Key: keyFor(slug, version) }),
        );
        return r.Body ? await bodyToString(r.Body) : null;
      } catch (err) {
        if (isNotFound(err)) return null;
        throw err;
      }
    },
    async headDoc(slug, version) {
      try {
        const r = await client.send(
          new s3.HeadObjectCommand({ Bucket: bucket, Key: keyFor(slug, version) }),
        );
        return { size: r.ContentLength ?? 0 };
      } catch (err) {
        if (isNotFound(err)) return null;
        throw err;
      }
    },
    async listVersions(slug) {
      const out: number[] = [];
      let token: string | undefined;
      do {
        const r = await client.send(
          new s3.ListObjectsV2Command({
            Bucket: bucket,
            Prefix: `${prefixFor(slug)}/`,
            ContinuationToken: token,
          }),
        );
        for (const o of r.Contents ?? []) {
          const m = /\/v(\d+)\/index\.html$/.exec(o.Key ?? '');
          if (m) out.push(Number(m[1]));
        }
        token = r.IsTruncated ? r.NextContinuationToken : undefined;
      } while (token);
      return out.sort((a, b) => a - b);
    },
    async deleteDoc(slug) {
      let token: string | undefined;
      do {
        const r = await client.send(
          new s3.ListObjectsV2Command({
            Bucket: bucket,
            Prefix: `${prefixFor(slug)}/`,
            ContinuationToken: token,
          }),
        );
        const objects = (r.Contents ?? []).map((o) => ({ Key: o.Key }));
        if (objects.length) {
          await client.send(
            new s3.DeleteObjectsCommand({ Bucket: bucket, Delete: { Objects: objects } }),
          );
        }
        token = r.IsTruncated ? r.NextContinuationToken : undefined;
      } while (token);
    },
  };
}
