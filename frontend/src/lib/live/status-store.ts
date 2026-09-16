/**
 * Reactive live-stream status + the poll-fallback interval helpers.
 *
 * Components subscribe through React's external-store primitive, while query
 * options read the status non-reactively inside `refetchInterval` functions — React Query
 * re-evaluates those after every fetch, and the open→closed transition
 * invalidation in `stream.ts` forces a re-evaluation when the stream drops
 * so fallback polling actually restarts.
 */

import { useSyncExternalStore } from "react";

export type LiveStatus = "idle" | "connecting" | "open" | "closed";

let status: LiveStatus = "idle";
const listeners = new Set<() => void>();

/** Reactive connection status for diagnostics and status pills. */
export function useLiveStatus(): LiveStatus {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    () => status,
    () => status,
  );
}

/** Non-reactive read of the current stream status. */
export function liveEventsStatus(): LiveStatus {
  return status;
}

/** Internal — stream.ts publishes status transitions through this. */
export function setLiveStatus(next: LiveStatus): void {
  if (status === next) return;
  status = next;
  listeners.forEach((listener) => listener());
}

/**
 * Poll-elimination interval: while the live stream is open, events drive
 * freshness and polling is off; when the stream is not open, fall back to
 * the base interval. Use as `refetchInterval: liveFallback(baseMs)`.
 */
export function liveFallback(baseMs: number): () => number | false {
  return () => (status === "open" ? false : baseMs);
}
