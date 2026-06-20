/**
 * Per-key async mutex. Serializes per-slug comment writes — the role the
 * Cloudflare Durable Object played upstream.
 *
 * Why: a comment mutation does read → apply → write. With async stores an
 * `await` between read and write yields the event loop, so two writers on the
 * same slug could interleave and lose an update. This makes the sequence atomic
 * per key. Correct for a single instance (the default deployment); multi-instance
 * would use a Postgres advisory lock instead (see DESIGN.md).
 */
export type LockFn = <T>(key: string, fn: () => Promise<T>) => Promise<T>;

/** Create a keyed mutex. Each key has an independent serialization chain. */
export function makeKeyedMutex(): LockFn {
  const tails = new Map<string, Promise<void>>();

  return function withLock<T>(key: string, fn: () => Promise<T>): Promise<T> {
    const prev = tails.get(key) ?? Promise.resolve();
    // Run fn after prev settles (success OR failure) so one rejection can't
    // wedge the chain for this key.
    const run = prev.then(fn, fn);
    const tail = run.then(
      () => undefined,
      () => undefined,
    );
    tails.set(key, tail);
    // GC: drop the entry once this tail is the current one and has settled, so
    // the map can't grow unbounded across many distinct keys.
    void tail.then(() => {
      if (tails.get(key) === tail) tails.delete(key);
    });
    return run;
  };
}
