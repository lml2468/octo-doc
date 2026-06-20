/**
 * Storage key derivation. Shared by the blob adapters so the path-traversal
 * defense (hash the slug) is defined exactly once — if the algorithm or length
 * changes, both stores stay in lockstep.
 */
import { createHash } from 'node:crypto';

/** Hash a slug to a fixed-length hex key safe to use as a path/prefix segment. */
export function hashSlug(slug: string): string {
  return createHash('sha256').update(slug).digest('hex').slice(0, 32);
}
