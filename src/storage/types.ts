/**
 * Storage adapter contracts. The service layer depends ONLY on these
 * interfaces — never on SQLite, Postgres, FS, or S3 types. Swapping
 * `STORAGE=sqlite+fs` → `postgres+s3` changes zero application code.
 *
 * All methods are async even where the reference SQLite/FS impl is synchronous,
 * so the Postgres/S3 adapters drop in without rippling sync→async into callers.
 */
import type { Comment } from '../core/comment.types.js';

/** Document metadata persisted per slug. */
export interface DocMeta {
  slug: string;
  title: string;
  versions: { n: number; created?: string | null }[];
  [extra: string]: unknown;
}

/** A signed-in viewer session. */
export interface Session {
  login: string;
  name?: string;
  avatar_url?: string | null;
  created: string;
}

/** A provisioned write token record. */
export interface TokenRecord {
  token: string;
  created: string;
  label?: string;
}

/**
 * Small structured records: doc metadata, comment logs, sessions, write tokens.
 * Implementations must return plain JS values (no driver row types).
 */
export interface MetadataStore {
  getMeta(slug: string): Promise<DocMeta | null>;
  putMeta(slug: string, meta: DocMeta): Promise<void>;
  deleteMeta(slug: string): Promise<void>;
  listMeta(): Promise<{ slug: string; meta: DocMeta }[]>;

  /** Always resolves to an array; corrupt/absent values fold to `[]`. */
  getComments(slug: string): Promise<Comment[]>;
  putComments(slug: string, list: Comment[]): Promise<void>;
  deleteComments(slug: string): Promise<void>;

  getSession(sid: string): Promise<Session | null>;
  putSession(sid: string, data: Session, ttlSeconds: number): Promise<void>;
  deleteSession(sid: string): Promise<void>;

  getToken(token: string): Promise<TokenRecord | null>;
  putToken(token: string, record: TokenRecord): Promise<void>;
  anyToken(): Promise<boolean>;

  close(): Promise<void>;
}

/** Immutable HTML documents keyed by (slug, version). */
export interface BlobStore {
  /** Write a document version. Implementations MUST be atomic (no half-writes). */
  putDoc(slug: string, version: number, html: string): Promise<{ size: number }>;
  getDoc(slug: string, version: number): Promise<string | null>;
  headDoc(slug: string, version: number): Promise<{ size: number } | null>;
  /** Versions present for a slug, ascending. */
  listVersions(slug: string): Promise<number[]>;
  /** Delete all versions for a slug. */
  deleteDoc(slug: string): Promise<void>;
}

/** A bound storage pair plus its human-readable spec. */
export interface Stores {
  metaStore: MetadataStore;
  blobStore: BlobStore;
  spec: string;
}
