/**
 * Storage factory + I/O-resilience decorators. Selects the adapter pair from
 * `config.storage` and wraps every method with timeout + bounded retry so a
 * hung or transiently-failing backend surfaces as a typed error.
 *
 * This is the storage module's public boundary — other layers import from here.
 */
import type { Config } from '../config.js';
import { withRetry } from './io.js';
import { makeSqliteMetadataStore } from './sqlite.js';
import { makeFsBlobStore } from './fs.js';
import type { BlobStore, MetadataStore, Stores } from './types.js';

export type { BlobStore, MetadataStore, DocMeta, Session, TokenRecord, Stores } from './types.js';

/** Wrap each method of `obj` so it runs under timeout + retry. */
/** Methods that are lifecycle, not I/O — passed through without retry/timeout. */
const NON_IO_METHODS = new Set(['close']);

/** Wrap each I/O method of `obj` so it runs under timeout + retry. */
function resilient<T extends object>(obj: T, config: Config): T {
  const wrapped = {} as Record<string, unknown>;
  for (const key of Object.keys(obj) as (keyof T)[]) {
    const fn = obj[key];
    if (typeof fn !== 'function' || NON_IO_METHODS.has(key as string)) {
      wrapped[key as string] = typeof fn === 'function' ? (fn as () => unknown).bind(obj) : fn;
      continue;
    }
    wrapped[key as string] = (...args: unknown[]) =>
      withRetry(() => (fn as (...a: unknown[]) => Promise<unknown>).apply(obj, args), {
        retries: config.ioRetries,
        timeoutMs: config.ioTimeoutMs,
        label: `storage.${String(key)}`,
      });
  }
  return wrapped as T;
}

/** Build the configured metadata + blob stores, decorated for resilience. */
export async function makeStores(config: Config): Promise<Stores> {
  const [metaKind, blobKind] = config.storage.split('+') as ['sqlite' | 'postgres', 'fs' | 's3'];

  const rawMeta: MetadataStore =
    metaKind === 'postgres'
      ? await (await import('./postgres.js')).makePostgresMetadataStore(config)
      : makeSqliteMetadataStore(config);

  const rawBlob: BlobStore =
    blobKind === 's3'
      ? await (await import('./s3.js')).makeS3BlobStore(config)
      : makeFsBlobStore(config);

  return {
    metaStore: resilient(rawMeta, config),
    blobStore: resilient(rawBlob, config),
    spec: `${metaKind}+${blobKind}`,
  };
}
