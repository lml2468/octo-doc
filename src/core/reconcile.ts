/**
 * Publish-time anchor reconciliation.
 *
 * When a new version is published, an element anchor may no longer resolve (the
 * artifact moved or changed). For each comment alive at the version, we try to
 * re-bind it to the right aid by fingerprint + nearest-heading, appending an
 * `anchor_changed` event. If no confident match exists we mark it `lost` so it
 * renders unanchored rather than silently pointing at the wrong artifact.
 * Older versions keep their own anchors (events are never mutated).
 */
import type {
  Anchor,
  AnchorFallback,
  Comment,
  CommentEvent,
  StampedArtifact,
} from './comment.types.js';
import { appendEvent, ensureMigrated } from './comment-events.js';
import { snapshotAt } from './comment-fold.js';

/** A drifted anchor that may need re-binding. */
type DriftableAnchor = Extract<Anchor, { kind: 'element' | 'lost' }>;

/** Extract the aid an anchor currently targets, if any. */
function knownAid(a: Anchor): string | null {
  if (a.kind === 'element' && a.aid) return a.aid;
  if (a.kind === 'element' && a.selector) {
    return /\[data-tdoc-aid="([\w]+)"\]/.exec(a.selector)?.[1] ?? null;
  }
  return null;
}

/** Find the single confident re-bind target for a drifted anchor, or null. */
function findRebindAid(a: DriftableAnchor, aids: StampedArtifact[]): string | null {
  const wantTag = a.fingerprint?.tag ?? a.label?.toLowerCase() ?? '';
  const wantHead = a.fallback?.nearestHeading?.text;
  const matches = aids.filter(
    (x) =>
      (!wantTag || x.tag === wantTag) &&
      (!wantHead || (x.heading ?? '').toLowerCase() === wantHead.toLowerCase()),
  );
  if (matches.length === 1) return matches[0]!.aid;
  if (matches.length === 0) {
    const tagOnly = aids.filter((x) => !wantTag || x.tag === wantTag);
    if (tagOnly.length === 1) return tagOnly[0]!.aid;
  }
  return null;
}

/** Carry over a drifted anchor's optional fingerprint/fallback metadata. */
function carry(a: DriftableAnchor): { fingerprint?: { tag?: string }; fallback?: AnchorFallback } {
  return {
    ...(a.fingerprint ? { fingerprint: a.fingerprint } : {}),
    ...(a.fallback ? { fallback: a.fallback } : {}),
  };
}

/** The new anchor to record for a drifted comment: a rebind, a lost marker, or none. */
function nextAnchor(a: DriftableAnchor, aids: StampedArtifact[]): Anchor | null {
  const newAid = findRebindAid(a, aids);
  if (newAid) {
    return {
      kind: 'element',
      aid: newAid,
      selector: `[data-tdoc-aid="${newAid}"]`,
      label: a.label ?? a.fingerprint?.tag ?? 'element',
      ...carry(a),
    };
  }
  if (a.kind === 'lost') return null; // already lost, no candidate — don't churn the log
  return {
    kind: 'lost',
    reason: 'no_candidate',
    ...(a.label ? { label: a.label } : {}),
    ...carry(a),
  };
}

/** Build the `anchor_changed` event recording a reconciled anchor. */
function reconcileEvent(anchor: Anchor, version: number, at: string): CommentEvent {
  return {
    kind: 'anchor_changed',
    at_version: version,
    at,
    by: 'reconcile',
    reset_status: false,
    anchor,
  };
}

/** Reconcile a single comment's drifted anchor at `version`, if needed. */
function reconcileComment(
  c: Comment,
  aids: StampedArtifact[],
  byAid: Set<string>,
  version: number,
  at: string,
): void {
  const snap = snapshotAt(c, version);
  if (!snap || snap.deleted) return;
  const a = snap.anchor;
  if (!a || (a.kind !== 'element' && a.kind !== 'lost')) return;

  const aid = knownAid(a);
  if (aid && byAid.has(aid)) return; // still valid

  const anchor = nextAnchor(a, aids);
  if (anchor) appendEvent(c, reconcileEvent(anchor, version, at));
}

/**
 * Reconcile open comment anchors against the freshly-stamped artifact set for a
 * version. Mutates `comments` in place (appending events) and returns it.
 */
export function reconcileAnchors(
  comments: Comment[],
  aidsInVersion: StampedArtifact[],
  v: number,
): Comment[] {
  ensureMigrated(comments);
  const byAid = new Set(aidsInVersion.map((a) => a.aid));
  const version = Number(v) || 1;
  const at = new Date().toISOString();
  for (const c of comments) reconcileComment(c, aidsInVersion, byAid, version, at);
  return comments;
}
