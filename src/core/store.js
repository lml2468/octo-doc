// Data-access layer: the serialized comment store. This is the seam the routes
// call instead of the Worker's mutateComments()/readComments() (which talked to
// a Durable Object). Same names, same { status, body } return shape — so the
// route bodies port over almost unchanged.
import { applyCommentOp } from './ops.js';
import { safeParseList, ensureMigrated } from './comments.js';
import { makeKeyedMutex } from './mutex.js';

export function makeCommentStore(metaStore) {
  const withLock = makeKeyedMutex();

  // Run a comment mutation for `slug`, serialized per-slug. Returns
  // { status, body }. The read→apply→write is atomic under withLock so
  // concurrent same-slug writers can't lose updates.
  async function mutateComments(slug, op) {
    return withLock(slug, async () => {
      const list = safeParseList(await metaStore.getComments(slug));
      const res = applyCommentOp(list, op);
      if (res.status === 200) {
        if (res.__wipe) await metaStore.deleteComments(slug);
        else await metaStore.putComments(slug, list);
      }
      const { __wipe, ...clean } = res;
      return clean;
    });
  }

  // Read the comment list for `slug` (the raw event-log array; callers fold it
  // with snapshotList / historyList).
  async function readComments(slug) {
    const list = safeParseList(await metaStore.getComments(slug));
    ensureMigrated(list);
    return list;
  }

  return { mutateComments, readComments, withLock };
}
