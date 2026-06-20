/**
 * Core domain barrel — the only entry point other layers import from. Keeps the
 * module boundary explicit (no deep imports into core internals).
 */
export * from './comment.types.js';
export { stampAids, cyrb53 } from './stamp.js';
export type { StampResult } from './stamp.js';
export {
  appendEvent,
  backfillEids,
  dedupEvents,
  ensureEventLog,
  ensureMigrated,
  compactComments,
  safeParseList,
  eventEid,
} from './comment-events.js';
export { snapshotAt, snapshotList, historyList } from './comment-fold.js';
export { reconcileAnchors } from './reconcile.js';
export { applyCommentOp } from './ops.js';
export type { CommentOp, OpResult } from './ops.js';
export {
  injectOverlayCfg,
  safeJsonForScript,
  escapeHtml,
  forHtmlComment,
  toOverlayIdentity,
} from './render.js';
export type { OverlayConfig, OverlayIdentity } from './render.js';
export { makeKeyedMutex } from './mutex.js';
export type { LockFn } from './mutex.js';
