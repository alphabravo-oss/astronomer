/**
 * Central paced invalidator — ALL live-event invalidations flow through it.
 *
 * One trailing throttler per stringified query key, `wait: 400ms,
 * leading + trailing`: leading keeps single events instant (a lone
 * `cluster.updated` refetches immediately), trailing coalesces informer
 * storms (a Deployment rollout emitting dozens of `cluster.k8s_changed`
 * frames collapses to exactly two refetches — one leading, one trailing).
 *
 * The map is cleared on stream close (`stream.ts` calls
 * `clearPacedInvalidations()`): pending trailing invalidations are pointless
 * once the event source is gone — the open→closed bulk invalidate already
 * kicks every active query.
 */

import { Throttler } from '@tanstack/react-pacer';
import type { QueryClient, QueryKey } from '@tanstack/react-query';

const WAIT_MS = 400;

type InvalidateThrottler = Throttler<(qc: QueryClient, key: QueryKey) => void>;

const throttlers = new Map<string, InvalidateThrottler>();

/**
 * Invalidate `key` (prefix-matching, like `invalidateQueries`) through the
 * per-key throttle. Non-mounted queries are only marked stale, so routing an
 * event nobody is looking at costs one Map lookup — no network.
 */
export function pacedInvalidate(queryClient: QueryClient, key: QueryKey): void {
  const mapKey = JSON.stringify(key);
  let throttler = throttlers.get(mapKey);
  if (!throttler) {
    throttler = new Throttler(
      (qc: QueryClient, k: QueryKey) => {
        qc.invalidateQueries({ queryKey: k });
      },
      { wait: WAIT_MS, leading: true, trailing: true },
    );
    throttlers.set(mapKey, throttler);
  }
  throttler.maybeExecute(queryClient, key);
}

/** Cancel every pending trailing invalidation and drop the throttler map. */
export function clearPacedInvalidations(): void {
  for (const throttler of throttlers.values()) {
    throttler.cancel();
  }
  throttlers.clear();
}
