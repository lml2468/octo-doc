// Storage adapter contract.
//
// The route layer depends ONLY on these two interfaces — never on SQLite,
// Postgres, FS, or S3 types. Swapping STORAGE=sqlite+fs → postgres+s3 changes
// zero application code (success criterion: "存储适配器切换零应用代码改动").
//
// Both interfaces are async (Promises) even where the reference SQLite/FS impl
// is synchronous, so the Postgres/S3 adapters drop in without rippling sync→
// async changes into the routes.
//
// ─────────────────────────────────────────────────────────────────────────
// MetadataStore — small structured records (doc meta, comment logs, sessions,
// tokens). The unit of storage is a JSON-serializable value keyed by string.
//
//   getMeta(slug)            -> meta object | null
//   putMeta(slug, meta)      -> void
//   deleteMeta(slug)         -> void
//   listMeta()               -> [{ slug, meta }]   (for the owner catalog)
//
//   getComments(slug)        -> array (always; corrupt/absent -> [])
//   putComments(slug, list)  -> void
//   deleteComments(slug)     -> void
//
//   getSession(sid)          -> session object | null   (honors TTL)
//   putSession(sid, data, ttlSeconds) -> void
//   deleteSession(sid)       -> void
//
//   getToken(token)          -> { token, created } | null
//   putToken(token, record)  -> void
//   anyToken()               -> boolean   (has a write token been provisioned?)
//
//   close()                  -> void
//
// ─────────────────────────────────────────────────────────────────────────
// BlobStore — immutable HTML documents, keyed by (slug, version).
//
//   putDoc(slug, version, html)   -> { size }
//   getDoc(slug, version)         -> string | null
//   headDoc(slug, version)        -> { size } | null
//   listVersions(slug)            -> number[]  (ascending)
//   deleteDoc(slug)               -> void      (all versions for the slug)
//
// SECURITY: implementations MUST treat slug/version as untrusted. The slug is
// validated upstream (safeSlug) but blob keys are additionally derived so a
// path-traversal slug can never escape the storage root (see fs.js).
//
// This file is documentation + a tiny factory; there is no base class to
// inherit (duck typing keeps the adapters free of cross-imports).

import { makeSqliteMetadataStore } from './sqlite.js';
import { makeFsBlobStore } from './fs.js';

// STORAGE selects the pair, e.g. "sqlite+fs" (default), "postgres+s3".
// `env` here is the loaded config object (config.storage), falling back to a
// raw process.env-style object (env.STORAGE) so this is callable either way.
export async function makeStores(env) {
  const spec = (env.storage || env.STORAGE || 'sqlite+fs').toLowerCase();
  const [metaKind, blobKind] = spec.split('+');

  let metaStore;
  if (metaKind === 'sqlite') metaStore = makeSqliteMetadataStore(env);
  else if (metaKind === 'postgres' || metaKind === 'pg') {
    const { makePostgresMetadataStore } = await import('./postgres.js');
    metaStore = await makePostgresMetadataStore(env);
  } else throw new Error(`unknown metadata store: ${metaKind} (expected sqlite|postgres)`);

  let blobStore;
  if (blobKind === 'fs') blobStore = makeFsBlobStore(env);
  else if (blobKind === 's3') {
    const { makeS3BlobStore } = await import('./s3.js');
    blobStore = await makeS3BlobStore(env);
  } else throw new Error(`unknown blob store: ${blobKind} (expected fs|s3)`);

  return { metaStore, blobStore, spec: `${metaKind}+${blobKind}` };
}
