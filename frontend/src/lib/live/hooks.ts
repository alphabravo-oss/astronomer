/**
 * React hook surface over the shared live-event stream.
 *
 * In P4.3 these hooks wrap the raw stream EventTarget directly; P4.4
 * re-points them at the dispatcher (`lib/live/dispatch.ts`) with no
 * signature change.
 */

import { useEffect, useMemo, useRef } from 'react';
import { useQueryClient, type QueryKey } from '@tanstack/react-query';
import type { LiveEvent, LiveEventType, Unsubscribe } from './envelope';
import { liveEventsStatus, type LiveStatus } from './status-store';
import {
  acquireLiveStream,
  liveTarget,
  registerLiveQueryClient,
  releaseLiveStream,
  reopenIfClosed,
} from './stream';

/**
 * Public live-events surface. Pages call `subscribe(type, handler)` to
 * react to specific events; the connection itself is held open by mounting
 * `useLiveEvents()` once at the dashboard root.
 */
export interface LiveEventsAPI {
  subscribe<T = unknown>(
    types: LiveEventType | LiveEventType[] | '*',
    handler: (ev: LiveEvent<T>) => void,
  ): Unsubscribe;
  /** Connection liveness for diagnostics / status pills. */
  status(): LiveStatus;
}

/**
 * Mount this hook once near the top of the authenticated tree (the dashboard
 * layout) to keep one EventSource open for the whole session. Subsequent
 * calls cheaply share the same connection via refcount.
 */
export function useLiveEvents(): LiveEventsAPI {
  const queryClient = useQueryClient();

  useEffect(() => {
    // Register the client for the stream's transition catch-up
    // invalidations (closed→re-open and open→closed kicks).
    registerLiveQueryClient(queryClient);
    acquireLiveStream();

    // Re-open whenever the tab returns to focus — handles laptop-sleep
    // scenarios where the underlying TCP connection was reset by the OS.
    const onVisibility = () => {
      if (document.visibilityState === 'visible') {
        reopenIfClosed();
      }
    };
    document.addEventListener('visibilitychange', onVisibility);

    return () => {
      document.removeEventListener('visibilitychange', onVisibility);
      releaseLiveStream();
    };
  }, [queryClient]);

  return useMemo<LiveEventsAPI>(
    () => ({
      subscribe<T = unknown>(
        types: LiveEventType | LiveEventType[] | '*',
        handler: (ev: LiveEvent<T>) => void,
      ) {
        const target = liveTarget();
        const list = Array.isArray(types) ? types : [types];
        const wrap = (e: Event) => {
          handler((e as CustomEvent<LiveEvent<T>>).detail);
        };
        for (const t of list) {
          target.addEventListener(t, wrap as EventListener);
        }
        return () => {
          for (const t of list) {
            target.removeEventListener(t, wrap as EventListener);
          }
        };
      },
      status() {
        return liveEventsStatus();
      },
    }),
    [],
  );
}

/**
 * Subscribe to a set of event types for the lifetime of the component.
 * The handler is kept in a ref so callers can pass an inline closure
 * without resubscribing on every render.
 */
export function useLiveSubscribe<T = unknown>(
  types: LiveEventType | LiveEventType[] | '*',
  handler: (ev: LiveEvent<T>) => void,
): void {
  const handlerRef = useRef(handler);
  handlerRef.current = handler;
  // Stable hash so an inline array literal doesn't churn the effect.
  const typesKey = JSON.stringify(types);

  useEffect(() => {
    acquireLiveStream();
    const target = liveTarget();
    const list: string[] = Array.isArray(types) ? types : [types];
    const wrap = (e: Event) => {
      handlerRef.current((e as CustomEvent<LiveEvent<T>>).detail);
    };
    for (const t of list) {
      target.addEventListener(t, wrap as EventListener);
    }
    return () => {
      for (const t of list) {
        target.removeEventListener(t, wrap as EventListener);
      }
      releaseLiveStream();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [typesKey]);
}

/**
 * Subscribe to a set of event types and invalidate the listed React Query
 * keys whenever any of them fire. This is the standard "make this page
 * live" hook — drop it into any page component alongside its data hooks.
 *
 * Call sites only need a stable reference to the keys they want refreshed;
 * we deep-compare against the previous keys array so callers can pass an
 * inline literal without remounting on every render.
 */
export function useLiveQueryInvalidation(
  eventTypes: LiveEventType | LiveEventType[],
  queryKeys: QueryKey | QueryKey[],
): void {
  const queryClient = useQueryClient();
  // Hash the inputs into a stable string so the effect only re-runs when the
  // caller actually passes a different set. Using JSON.stringify keeps the
  // implementation tiny — query keys are small arrays of strings/objects so
  // serialisation cost is negligible.
  const typesKey = JSON.stringify(eventTypes);
  const keysKey = JSON.stringify(queryKeys);

  useEffect(() => {
    registerLiveQueryClient(queryClient);
    acquireLiveStream();
    const target = liveTarget();

    const types = Array.isArray(eventTypes) ? eventTypes : [eventTypes];
    const allKeys: QueryKey[] = isQueryKeyArray(queryKeys)
      ? (queryKeys as QueryKey[])
      : [queryKeys as QueryKey];

    const handler = () => {
      for (const key of allKeys) {
        // Prefix-match invalidation: passing ['clusters'] also nukes
        // ['clusters', 'list', ...] so callers don't need every variant.
        queryClient.invalidateQueries({ queryKey: key });
      }
    };
    const wrap = () => handler();
    for (const t of types) {
      target.addEventListener(t, wrap as EventListener);
    }

    return () => {
      for (const t of types) {
        target.removeEventListener(t, wrap as EventListener);
      }
      releaseLiveStream();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [typesKey, keysKey, queryClient]);
}

/** Accept either a single QueryKey or an array of them. */
function isQueryKeyArray(v: QueryKey | QueryKey[]): boolean {
  // A QueryKey is itself a readonly array; distinguish "array of keys" from
  // "a single key" by checking that the first element is itself array-like.
  if (!Array.isArray(v)) return false;
  if (v.length === 0) return false;
  const first = v[0];
  return Array.isArray(first);
}
