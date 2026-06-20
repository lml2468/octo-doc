// Per-key async mutex. Replaces the Cloudflare Durable Object that serialized
// per-slug comment writes.
//
// WHY: every comment mutation does read(list) → mutate → write(list). Two
// concurrent writers on the SAME slug could each read the same base, append
// independently, and the second write clobbers the first — a lost update that
// defeats the append-only event log. The Worker solved this with a DO
// (idFromName(slug), single-threaded). A single Node process is already
// single-threaded for synchronous code, but `await` between read and write
// yields the event loop, so an interleaving is still possible with async stores
// (Postgres/S3). This mutex makes get→mutate→put atomic per slug.
//
// This is an in-process lock: correct for a single app instance (the default
// deployment). Horizontal scaling across instances would need a Postgres
// advisory lock instead — documented in DESIGN.md.

export function makeKeyedMutex() {
  const tails = new Map(); // key -> Promise of the last-enqueued task

  return function withLock(key, fn) {
    const prev = tails.get(key) || Promise.resolve();
    // Run fn strictly after prev settles (success OR failure), so one rejected
    // task never wedges the chain for that key.
    const run = prev.then(() => fn(), () => fn());
    // The tail tracks completion (swallow errors so the chain stays alive).
    const tail = run.then(() => {}, () => {});
    tails.set(key, tail);
    // GC: when this tail is the current one and has settled, drop the entry so
    // the map can't grow without bound across many distinct slugs.
    tail.then(() => { if (tails.get(key) === tail) tails.delete(key); });
    return run;
  };
}
