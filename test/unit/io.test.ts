import { describe, it, expect } from 'vitest';
import { withTimeout, withRetry } from '../../src/storage/io.js';
import { UpstreamError } from '../../src/errors.js';

describe('withTimeout', () => {
  it('resolves a fast promise', async () => {
    await expect(withTimeout(Promise.resolve(42), 1000, 'op')).resolves.toBe(42);
  });

  it('rejects with UpstreamError when slow', async () => {
    const slow = new Promise((r) => setTimeout(r, 1000));
    await expect(withTimeout(slow, 10, 'op')).rejects.toBeInstanceOf(UpstreamError);
  });
});

describe('withRetry', () => {
  it('returns on first success without retrying', async () => {
    let calls = 0;
    const r = await withRetry(
      () => {
        calls++;
        return Promise.resolve('ok');
      },
      { retries: 2, timeoutMs: 100, label: 'op' },
    );
    expect(r).toBe('ok');
    expect(calls).toBe(1);
  });

  it('retries then succeeds', async () => {
    let calls = 0;
    const r = await withRetry(
      () => {
        calls++;
        return calls < 3 ? Promise.reject(new Error('transient')) : Promise.resolve('ok');
      },
      { retries: 3, timeoutMs: 100, label: 'op' },
    );
    expect(r).toBe('ok');
    expect(calls).toBe(3);
  });

  it('throws UpstreamError after exhausting retries', async () => {
    let calls = 0;
    await expect(
      withRetry(
        () => {
          calls++;
          return Promise.reject(new Error('always'));
        },
        { retries: 1, timeoutMs: 100, label: 'op' },
      ),
    ).rejects.toBeInstanceOf(UpstreamError);
    expect(calls).toBe(2); // initial + 1 retry
  });

  it('does not retry when retryable() returns false', async () => {
    let calls = 0;
    await expect(
      withRetry(
        () => {
          calls++;
          return Promise.reject(new Error('fatal'));
        },
        { retries: 5, timeoutMs: 100, label: 'op', retryable: () => false },
      ),
    ).rejects.toBeInstanceOf(UpstreamError);
    expect(calls).toBe(1);
  });
});
