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

import { useEffect, useRef, useState } from 'react';
import { useQueryClient, type QueryKey } from '@tanstack/react-query';
import { createStreamTicket } from '@/lib/api';

const API_BASE = process.env.NEXT_PUBLIC_API_URL || '/api/v1';

/** The Kubernetes watch verbs the reducer folds. BOOKMARK/ERROR are ignored. */
export type WatchVerb = 'ADDED' | 'MODIFIED' | 'DELETED';

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

function isWatchVerb(v: string): v is WatchVerb {
  return v === 'ADDED' || v === 'MODIFIED' || v === 'DELETED';
}

/** Watch source: the dedicated pods SSE stream, or a generic proxy list path. */
export type WatchSource =
  | { kind: 'pods'; namespace?: string }
  | { kind: 'proxy'; path: string };

export interface UseResourceWatchOptions<T extends K8sObject> {
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
    const controller = new AbortController();
    const sep = src.path.includes('?') ? '&' : '?';
    const url = `${API_BASE}/clusters/${clusterId}/k8s/${src.path}${sep}watch=true`;

    (async () => {
      let res: Response;
      try {
        res = await fetch(url, {
          method: 'GET',
          credentials: 'include',
          signal: controller.signal,
          headers: { Accept: 'application/json' },
        });
      } catch {
        if (!cancelled) setStatus('fallback');
        return;
      }
      if (!res.ok || !res.body) {
        if (!cancelled) setStatus('fallback');
        return;
      }
      if (!cancelled) setStatus('live');

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = '';
      try {
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });
          let nl = buf.indexOf('\n');
          while (nl >= 0) {
            const line = buf.slice(0, nl).trim();
            buf = buf.slice(nl + 1);
            nl = buf.indexOf('\n');
            if (!line) continue;
            let frame: { type?: string; object?: T };
            try {
              frame = JSON.parse(line);
            } catch {
              continue;
            }
            if (frame.type && isWatchVerb(frame.type) && frame.object) {
              if (!cancelled) apply(frame.type, frame.object);
            }
          }
        }
        // Stream closed cleanly (server ended the watch) — resume polling.
        if (!cancelled) setStatus('fallback');
      } catch {
        // Aborted on unmount, or the connection dropped mid-stream.
        if (!cancelled) setStatus('fallback');
      }
    })();

    return () => {
      cancelled = true;
      controller.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clusterId, keyStr, sourceStr, enabled, queryClient]);

  return { status, live: status === 'live' };
}
