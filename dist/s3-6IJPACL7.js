// src/storage/s3.ts
import { createHash } from "crypto";
function hashSlug(slug) {
  return createHash("sha256").update(slug).digest("hex").slice(0, 32);
}
function isNotFound(err) {
  const e = err;
  return e?.$metadata?.httpStatusCode === 404 || e?.name === "NoSuchKey" || e?.name === "NotFound";
}
async function bodyToString(body) {
  if (typeof body.transformToString === "function") return body.transformToString("utf8");
  const chunks = [];
  for await (const c of body) chunks.push(Buffer.from(c));
  return Buffer.concat(chunks).toString("utf8");
}
async function makeS3BlobStore(config) {
  const s3 = await import("@aws-sdk/client-s3");
  const env = process.env;
  const bucket = env.S3_BUCKET ?? "octo-doc";
  const client = new s3.S3Client({
    region: env.S3_REGION ?? env.AWS_REGION ?? "us-east-1",
    endpoint: env.S3_ENDPOINT,
    forcePathStyle: /^(1|true|yes)$/i.test(env.S3_FORCE_PATH_STYLE ?? ""),
    credentials: env.S3_ACCESS_KEY_ID ?? env.AWS_ACCESS_KEY_ID ? {
      accessKeyId: env.S3_ACCESS_KEY_ID ?? env.AWS_ACCESS_KEY_ID,
      secretAccessKey: env.S3_SECRET_ACCESS_KEY ?? env.AWS_SECRET_ACCESS_KEY
    } : void 0
  });
  void config;
  const prefixFor = (slug) => `docs/${hashSlug(slug)}`;
  const keyFor = (slug, version) => `${prefixFor(slug)}/v${version}/index.html`;
  return {
    async putDoc(slug, version, html) {
      await client.send(
        new s3.PutObjectCommand({
          Bucket: bucket,
          Key: keyFor(slug, version),
          Body: html,
          ContentType: "text/html; charset=utf-8"
        })
      );
      return { size: Buffer.byteLength(html) };
    },
    async getDoc(slug, version) {
      try {
        const r = await client.send(
          new s3.GetObjectCommand({ Bucket: bucket, Key: keyFor(slug, version) })
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
          new s3.HeadObjectCommand({ Bucket: bucket, Key: keyFor(slug, version) })
        );
        return { size: r.ContentLength ?? 0 };
      } catch (err) {
        if (isNotFound(err)) return null;
        throw err;
      }
    },
    async listVersions(slug) {
      const out = [];
      let token;
      do {
        const r = await client.send(
          new s3.ListObjectsV2Command({
            Bucket: bucket,
            Prefix: `${prefixFor(slug)}/`,
            ContinuationToken: token
          })
        );
        for (const o of r.Contents ?? []) {
          const m = /\/v(\d+)\/index\.html$/.exec(o.Key ?? "");
          if (m) out.push(Number(m[1]));
        }
        token = r.IsTruncated ? r.NextContinuationToken : void 0;
      } while (token);
      return out.sort((a, b) => a - b);
    },
    async deleteDoc(slug) {
      let token;
      do {
        const r = await client.send(
          new s3.ListObjectsV2Command({
            Bucket: bucket,
            Prefix: `${prefixFor(slug)}/`,
            ContinuationToken: token
          })
        );
        const objects = (r.Contents ?? []).map((o) => ({ Key: o.Key }));
        if (objects.length) {
          await client.send(
            new s3.DeleteObjectsCommand({ Bucket: bucket, Delete: { Objects: objects } })
          );
        }
        token = r.IsTruncated ? r.NextContinuationToken : void 0;
      } while (token);
    }
  };
}
export {
  makeS3BlobStore
};
//# sourceMappingURL=s3-6IJPACL7.js.map