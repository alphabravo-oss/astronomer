/**
 * Live-event dispatcher: `onmessage` → envelope → route lookup →
 * `pacedInvalidate` / merger.
 *
 * The stream transport (`stream.ts`) hands every raw SSE frame here. The
 * dispatcher parses + camelizes it once (envelope.ts), fans it out on the
 * shared EventTarget the live hooks and the cluster-metrics merger listen
 * on, then resolves the routing table (`routes.ts`) and pushes each
 * produced query key through the central paced invalidator.
 */

import type { QueryClient } from '@tanstack/react-query';
import { parseFrame } from './envelope';
import { pacedInvalidate } from './paced-invalidate';
import { resolveEventRoute, type LiveEventData } from './routes';

/**
 * Handle one raw SSE frame. `target` is the stream's fan-out EventTarget;
 * `queryClient` is the client registered by the live hooks (null before the
 * first hook mounts — nothing is cached yet, so routing is a no-op).
 */
export function dispatchLiveFrame(
  raw: unknown,
  target: EventTarget,
  queryClient: QueryClient | null,
): void {
  const detail = parseFrame(raw);
  if (!detail) return;

  // Fan out to subscribers (useLiveSubscribe / useLiveQueryInvalidation /
  // the cluster-metrics merger) by envelope type, plus a `*` wildcard so
  // "subscribe to everything" consumers don't have to enumerate types.
  target.dispatchEvent(new CustomEvent(detail.type, { detail }));
  target.dispatchEvent(new CustomEvent('*', { detail }));

  if (!queryClient) return;
  const route = resolveEventRoute(detail.type);
  if (!route) return;
  const data = (detail.data ?? {}) as LiveEventData;
  for (const key of route(data)) {
    pacedInvalidate(queryClient, key);
  }
}
