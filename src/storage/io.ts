/**
 * I/O boundary helpers: timeout + bounded retry with backoff. Applied by the
 * storage adapters so a hung or transiently-failing dependency surfaces as a
 * typed {@link UpstreamError} rather than an unbounded await.
 */
import { UpstreamError } from '../errors.js';

/** Reject if `promise` does not settle within `ms`. */
export async function withTimeout<T>(promise: Promise<T>, ms: number, label: string): Promise<T> {
  let timer: NodeJS.Timeout | undefined;
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(
      () => reject(new UpstreamError(`${label} timed out after ${ms}ms`, 'io_timeout')),
      ms,
    );
  });
  try {
    return await Promise.race([promise, timeout]);
  } finally {
    clearTimeout(timer);
  }
}

/** Options for {@link withRetry}. */
export interface RetryOptions {
  retries: number;
  timeoutMs: number;
  label: string;
  /** Decide whether an error is worth retrying (default: always). */
  retryable?: (err: unknown) => boolean;
}

/**
 * Run `fn` with a per-attempt timeout and bounded retries (exponential backoff:
 * 50ms, 100ms, 200ms…). Throws an {@link UpstreamError} after exhausting retries.
 */
export async function withRetry<T>(fn: () => Promise<T>, opts: RetryOptions): Promise<T> {
  const { retries, timeoutMs, label, retryable = () => true } = opts;
  let lastErr: unknown;
  for (let attempt = 0; attempt <= retries; attempt++) {
    try {
      return await withTimeout(fn(), timeoutMs, label);
    } catch (err) {
      lastErr = err;
      if (attempt === retries || !retryable(err)) break;
      await delay(50 * 2 ** attempt);
    }
  }
  throw lastErr instanceof Error
    ? new UpstreamError(
        `${label} failed after ${retries + 1} attempt(s): ${lastErr.message}`,
        'io_failed',
        lastErr,
      )
    : new UpstreamError(`${label} failed`, 'io_failed', lastErr);
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
