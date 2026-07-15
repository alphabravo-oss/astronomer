/**
 * Live resource watch hooks (P-02).
 *
 * Turns the polling workload/resource tables into live views by folding a
 * Kubernetes watch stream into the same React Query cache the table already
 * reads. Two transports, one reducer:
 *
 *  1. `pods`  — the dedicated Server-Sent Events endpoint
 *       GET /api/v1/clusters/{id}/pods/watch/?namespace=<ns>&ticket=<t>
 *     Each frame is one SSE event whose `event:` is the watch verb
 *     (ADDED/MODIFIED/DELETED) and whose `data:` is the raw pod object JSON
 *     (see internal/handler/pods_watch.go). Auth is a one-use stream ticket
 *     (StreamKindLogs, scoped to the cluster) because EventSource cannot send
 *     headers — the same posture as the pod-logs / exec streams.
 *
 *  2. `proxy` — any curated list kind through the generic k8s passthrough
 *       GET /api/v1/clusters/{id}/k8s/{path}?watch=true
 *     which the management plane chunk-forwards from the API server as
 *     newline-delimited JSON `{ "type": <verb>, "object": {...} }` frames.
 *     Auth is the session cookie (`credentials: 'include'`), so no ticket.
 *
 * Both fold each frame into a `{ items: T[] }` list cache keyed by
 * metadata.uid (falling back to namespace/name). A watch that cannot open —
 * ticket mint fails, proxy 5xx, browser without ReadableStream — leaves the
 * cache untouched and reports `live: false` so the caller keeps polling.
 */

'use client';

import { useEffect, useState } from 'react';
import { useQueryClient, type QueryKey } from '@tanstack/react-query';
import { createStreamTicket } from '@/lib/api';
import { openProxyWatch, type WatchVerb } from '@/lib/api/k8s-watch';
import { API_BASE } from '@/lib/env';

export type { WatchVerb } from '@/lib/api/k8s-watch';

interface K8sObjectMeta {
  name?: string;
  namespace?: string;
  uid?: string;
}

export interface K8sObject {
  metadata?: K8sObjectMeta;
}

/** The list-response shape both the workloads and resources tables cache. */
export interface K8sList<T extends K8sObject = K8sObject> {
  items?: T[];
  metadata?: { resourceVersion?: string };
}

/**
 * Stable identity for a k8s object across watch frames. uid is the canonical
 * key; namespace/name is the fallback for frames that (rarely) omit uid.
 */
export function identityOf(obj: K8sObject | undefined): string {
  const m = obj?.metadata;
  if (m?.uid) return m.uid;
  return `${m?.namespace ?? ''}/${m?.name ?? ''}`;
}

/**
 * Pure reducer: fold one watch frame into a list cache. ADDED/MODIFIED upsert
 * by identity; DELETED removes. Returns a new object (never mutates `prev`) so
 * React Query's structural sharing sees the change. A frame with no object is
 * a no-op.
 */
export function foldWatchFrame<T extends K8sObject>(
  prev: K8sList<T> | undefined,
  verb: WatchVerb,
  obj: T | undefined | null,
): K8sList<T> {
  const base: K8sList<T> = prev ?? {};
  if (!obj || !obj.metadata) return base;
  const items = base.items ? base.items.slice() : [];
  const key = identityOf(obj);
  const idx = items.findIndex((i) => identityOf(i) === key);
  if (verb === 'DELETED') {
    if (idx >= 0) items.splice(idx, 1);
    return { ...base, items };
  }
  // ADDED or MODIFIED — upsert.
  if (idx >= 0) items[idx] = obj;
  else items.push(obj);
  return { ...base, items };
}

/** Watch source: the dedicated pods SSE stream, or a generic proxy list path. */
export type WatchSource =
  | { kind: 'pods'; namespace?: string }
  | { kind: 'proxy'; path: string };

export interface UseResourceWatchOptions<_T extends K8sObject> {
  clusterId: string;
  /** React Query key of the `{ items: T[] }` list cache to fold frames into. */
  queryKey: QueryKey;
  source: WatchSource;
  /** Gate the watch (e.g. until the cluster id resolves). Defaults to true. */
  enabled?: boolean;
}

export type WatchStatus = 'idle' | 'connecting' | 'live' | 'fallback';

export interface ResourceWatchResult {
  status: WatchStatus;
  /** True once frames are streaming — the caller can lengthen its poll. */
  live: boolean;
}

/**
 * Open a live watch for `source` and fold every frame into `queryKey`'s list
 * cache. Returns the connection status; `live` flips true once the stream is
 * open so the caller can drop or lengthen its `refetchInterval`. On any open
 * failure the cache is left alone and status settles on `fallback`.
 */
export function useResourceWatch<T extends K8sObject = K8sObject>(
  opts: UseResourceWatchOptions<T>,
): ResourceWatchResult {
  const { clusterId, queryKey, source, enabled = true } = opts;
  const queryClient = useQueryClient();
  const [status, setStatus] = useState<WatchStatus>('idle');

  // Serialise the inputs so the effect only re-runs on a real change and not on
  // every render (queryKey / source are commonly inline literals).
  const keyStr = JSON.stringify(queryKey);
  const sourceStr = JSON.stringify(source);

  useEffect(() => {
    if (!enabled || !clusterId) {
      setStatus('idle');
      return;
    }
    setStatus('connecting');

    const src = JSON.parse(sourceStr) as WatchSource;
    const key = JSON.parse(keyStr) as QueryKey;
    let cancelled = false;

    const apply = (verb: WatchVerb, obj: T | undefined) => {
      queryClient.setQueryData<K8sList<T>>(key, (prev) => foldWatchFrame(prev, verb, obj));
    };

    if (src.kind === 'pods') {
      // --- SSE transport (ticket-authenticated EventSource) ---
      let es: EventSource | null = null;
      createStreamTicket('logs', clusterId)
        .then(({ ticket }) => {
          if (cancelled) return;
          const nsQ = src.namespace ? `namespace=${encodeURIComponent(src.namespace)}&` : '';
          const url = `${API_BASE}/clusters/${clusterId}/pods/watch/?${nsQ}ticket=${encodeURIComponent(ticket)}`;
          try {
            es = new EventSource(url, { withCredentials: false });
          } catch {
            if (!cancelled) setStatus('fallback');
            return;
          }
          es.onopen = () => {
            if (!cancelled) setStatus('live');
          };
          const onFrame = (verb: WatchVerb) => (ev: MessageEvent) => {
            if (cancelled) return;
            setStatus('live');
            let obj: T | undefined;
            try {
              obj = ev.data ? (JSON.parse(ev.data as string) as T) : undefined;
            } catch {
              return;
            }
            if (obj) apply(verb, obj);
          };
          es.addEventListener('ADDED', onFrame('ADDED') as EventListener);
          es.addEventListener('MODIFIED', onFrame('MODIFIED') as EventListener);
          es.addEventListener('DELETED', onFrame('DELETED') as EventListener);
          es.onerror = () => {
            // The stream dropped (or never opened). Fall back to polling rather
            // than hammering reconnects — the caller's query resumes on the
            // fallback interval.
            try {
              es?.close();
            } catch {
              /* ignore */
            }
            es = null;
            if (!cancelled) setStatus('fallback');
          };
        })
        .catch(() => {
          if (!cancelled) setStatus('fallback');
        });

      return () => {
        cancelled = true;
        try {
          es?.close();
        } catch {
          /* ignore */
        }
        es = null;
      };
    }

    // --- Proxy transport (chunked NDJSON via fetch streaming) ---
    return openProxyWatch(
      clusterId,
      src.path,
      (verb, obj) => {
        if (!cancelled) apply(verb, obj as T);
      },
      (s) => {
        if (!cancelled) setStatus(s);
      },
    );
  }, [clusterId, keyStr, sourceStr, enabled, queryClient]);

  return { status, live: status === 'live' };
}

/**
 * Live-watch a curated list kind as a *change signal* (P-02, explorer tables).
 *
 * The workloads page folds raw watch frames straight into its `{ items }`
 * cache. The explorer tables can't do that: they cache a server-*transformed*
 * shape (computed status, age, restarts), so reproducing it from a raw watch
 * object client-side would duplicate server logic and drift. Instead this hook
 * opens the same proxy watch and, on any add/modify/delete, debounce-
 * invalidates the given React Query keys so the table refetches its normal
 * transformed list — per-kind liveness with no transform duplication.
 *
 * It augments the coarse `cluster.k8s_changed` signal (see useLiveQueryInvalidation)
 * with a precise, immediate signal for the kind actually on screen, and quietly
 * settles on `fallback` (the caller's poll keeps it fresh) if the watch can't open.
 */
export function useResourceWatchInvalidation(opts: {
  clusterId: string;
  /** k8s list path, e.g. 'api/v1/pods'. Empty disables the watch. */
  path: string;
  /** Query keys (prefix-matched) to invalidate when the kind changes. */
  queryKeys: QueryKey[];
  enabled?: boolean;
  /** Collapse a burst of frames (e.g. the initial list sync) into one refetch. */
  debounceMs?: number;
}): ResourceWatchResult {
  const { clusterId, path, queryKeys, enabled = true, debounceMs = 400 } = opts;
  const queryClient = useQueryClient();
  const [status, setStatus] = useState<WatchStatus>('idle');
  const keysStr = JSON.stringify(queryKeys);

  useEffect(() => {
    if (!enabled || !clusterId || !path) {
      setStatus('idle');
      return;
    }
    setStatus('connecting');
    const keys = JSON.parse(keysStr) as QueryKey[];
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const stop = openProxyWatch(
      clusterId,
      path,
      () => {
        if (cancelled || timer) return; // one refetch per debounce window
        timer = setTimeout(() => {
          timer = null;
          for (const k of keys) queryClient.invalidateQueries({ queryKey: k });
        }, debounceMs);
      },
      (s) => {
        if (!cancelled) setStatus(s);
      },
    );

    return () => {
      cancelled = true;
      stop();
      if (timer) clearTimeout(timer);
    };
  }, [clusterId, path, keysStr, enabled, debounceMs, queryClient]);

  return { status, live: status === 'live' };
}
