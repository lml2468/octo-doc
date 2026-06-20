/**
 * Random id + token generation. Centralized so every id format and the token
 * entropy live in one place.
 */
import { randomBytes } from 'node:crypto';

/** N random bytes as a hex string (2·N hex chars). */
export function rand(bytes: number): string {
  return randomBytes(bytes).toString('hex');
}

/** A fresh opaque write token (256 bits of entropy). */
export function newToken(): string {
  return rand(32);
}

/** A fresh session id. */
export function newSessionId(): string {
  return rand(24);
}
