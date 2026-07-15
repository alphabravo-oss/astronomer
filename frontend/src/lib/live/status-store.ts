/**
 * Reactive live-stream status + the poll-fallback interval helpers.
 *
 * `liveStatus` is a TanStack Store atom so components (status pills) can
 * subscribe reactively via `useStore`, while query options read it
 * non-reactively inside `refetchInterval` functions — React Query
 * re-evaluates those after every fetch, and the open→closed transition
 * invalidation in `stream.ts` forces a re-evaluation when the stream drops
 * so fallback polling actually restarts.
 */

import { Store } from '@tanstack/store';

export type LiveStatus = 'idle' | 'connecting' | 'open' | 'closed';

/** Current SSE connection status. Written only by `stream.ts`. */
export const liveStatus = new Store<LiveStatus>('idle');

/** Non-reactive read of the current stream status. */
export function liveEventsStatus(): LiveStatus {
  return liveStatus.state;
}

/** Internal — stream.ts publishes status transitions through this. */
export function setLiveStatus(next: LiveStatus): void {
  liveStatus.setState(() => next);
}

/**
 * Poll-elimination interval: while the live stream is open, events drive
 * freshness and polling is off; when the stream is not open, fall back to
 * the base interval. Use as `refetchInterval: liveFallback(baseMs)`.
 */
export function liveFallback(baseMs: number): () => number | false {
  return () => (liveStatus.state === 'open' ? false : baseMs);
}

/**
 * UX-04 legacy: lengthen (not eliminate) polls while the bus is open.
 * Still used by the pre-conversion `lib/hooks.ts` sites; deleted in P4.4
 * when every k8s-domain site converts to `liveFallback`.
 */
export function liveAwareRefetchInterval(baseMs: number): number {
  if (liveStatus.state === 'open') {
    return Math.max(baseMs * 4, 60_000);
  }
  return baseMs;
}
