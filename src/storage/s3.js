// S3 / MinIO BlobStore (AWS SDK v3). Same interface as the FS adapter —
// selected with STORAGE=...+s3. Works against AWS S3, MinIO, R2, or any
// S3-compatible endpoint (set S3_ENDPOINT + S3_FORCE_PATH_STYLE=1 for MinIO).
//
// Object keys: docs/<safeKey(slug)>/v<version>/index.html — the slug is hashed
// (path-traversal defense, same as the FS adapter) so an unexpected slug can
// never produce a key outside the docs/ prefix.
import { createHash } from 'node:crypto';

function safeKey(slug) {
  return createHash('sha256').update(String(slug)).digest('hex').slice(0, 32);
}

export async function makeS3BlobStore(env) {
  const {
    S3Client, PutObjectCommand, GetObjectCommand, HeadObjectCommand,
    ListObjectsV2Command, DeleteObjectsCommand,
  } = await import('@aws-sdk/client-s3');

  const bucket = env.S3_BUCKET || 'octo-doc';
  const client = new S3Client({
    region: env.S3_REGION || env.AWS_REGION || 'us-east-1',
    endpoint: env.S3_ENDPOINT || undefined,
    forcePathStyle: /^(1|true|yes)$/i.test(env.S3_FORCE_PATH_STYLE || ''),
    credentials: (env.S3_ACCESS_KEY_ID || env.AWS_ACCESS_KEY_ID) ? {
      accessKeyId: env.S3_ACCESS_KEY_ID || env.AWS_ACCESS_KEY_ID,
      secretAccessKey: env.S3_SECRET_ACCESS_KEY || env.AWS_SECRET_ACCESS_KEY,
    } : undefined,
  });

  const prefixFor = (slug) => `docs/${safeKey(slug)}`;
  const keyFor = (slug, version) => `${prefixFor(slug)}/v${Number(version)}/index.html`;

  const streamToString = async (body) => {
    if (!body) return null;
    if (typeof body.transformToString === 'function') return body.transformToString('utf8');
    const chunks = [];
    for await (const c of body) chunks.push(typeof c === 'string' ? Buffer.from(c) : c);
    return Buffer.concat(chunks).toString('utf8');
  };

  return {
    async putDoc(slug, version, html) {
      await client.send(new PutObjectCommand({
        Bucket: bucket, Key: keyFor(slug, version), Body: html,
        ContentType: 'text/html; charset=utf-8',
      }));
      return { size: Buffer.byteLength(html) };
    },
    async getDoc(slug, version) {
      try {
        const r = await client.send(new GetObjectCommand({ Bucket: bucket, Key: keyFor(slug, version) }));
        return await streamToString(r.Body);
      } catch (e) {
        if (e?.$metadata?.httpStatusCode === 404 || e?.name === 'NoSuchKey') return null;
        throw e;
      }
    },
    async headDoc(slug, version) {
      try {
        const r = await client.send(new HeadObjectCommand({ Bucket: bucket, Key: keyFor(slug, version) }));
        return { size: r.ContentLength };
      } catch (e) {
        if (e?.$metadata?.httpStatusCode === 404 || e?.name === 'NotFound') return null;
        throw e;
      }
    },
    async listVersions(slug) {
      const out = [];
      let token;
      do {
        const r = await client.send(new ListObjectsV2Command({
          Bucket: bucket, Prefix: `${prefixFor(slug)}/`, ContinuationToken: token,
        }));
        for (const o of r.Contents || []) {
          const m = /\/v(\d+)\/index\.html$/.exec(o.Key);
          if (m) out.push(Number(m[1]));
        }
        token = r.IsTruncated ? r.NextContinuationToken : undefined;
      } while (token);
      return out.sort((a, b) => a - b);
    },
    async deleteDoc(slug) {
      let token;
      do {
        const r = await client.send(new ListObjectsV2Command({
          Bucket: bucket, Prefix: `${prefixFor(slug)}/`, ContinuationToken: token,
        }));
        const objects = (r.Contents || []).map(o => ({ Key: o.Key }));
        if (objects.length) {
          await client.send(new DeleteObjectsCommand({ Bucket: bucket, Delete: { Objects: objects } }));
        }
        token = r.IsTruncated ? r.NextContinuationToken : undefined;
      } while (token);
    },
  };
}
