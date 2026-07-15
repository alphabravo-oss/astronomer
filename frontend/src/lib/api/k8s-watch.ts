// Kubernetes watch transports (P-02 / P4.7).
//
// Two streams, one frame shape:
//
//  1. `openProxyWatch` — any curated list kind through the generic k8s
//     passthrough `GET /api/v1/clusters/{id}/k8s/{path}?watch=true`, which the
//     management plane chunk-forwards from the API server as newline-delimited
//     JSON `{ "type": <verb>, "object": {...} }` frames. Auth is the session
//     cookie (`credentials: 'include'`), so no ticket.
//
//  2. `openPodsWatch` — the dedicated pods Server-Sent Events endpoint
//     `GET /api/v1/clusters/{id}/pods/watch/?namespace=<ns>&ticket=<t>`. Each
//     frame is one SSE event whose `event:` is the watch verb and whose
//     `data:` is the raw pod object JSON (internal/handler/pods_watch.go).
//     Auth is a one-use stream ticket because EventSource cannot send headers.
//
// These are the transport half of lib/db/collections.ts; they live under
// lib/api/ so the raw streaming fetch stays inside the fetch-containment
// boundary.

import { createStreamTicket } from '../api';
import { API_BASE } from '@/lib/env';

/** The Kubernetes watch verbs the reducer folds. BOOKMARK/ERROR are ignored. */
export type WatchVerb = 'ADDED' | 'MODIFIED' | 'DELETED';

function isWatchVerb(v: string): v is WatchVerb {
  return v === 'ADDED' || v === 'MODIFIED' || v === 'DELETED';
}

/**
 * Open a proxy watch (`GET /clusters/{id}/k8s/{path}?watch=true`) and invoke
 * `onFrame` for every NDJSON `{ type, object }` frame, reporting connection
 * state via `onStatus`. Returns a cleanup that aborts the stream. Shared by the
 * cache-folding hook and the invalidation hook so the streaming/parse logic
 * lives in exactly one place.
 */
export function openProxyWatch(
  clusterId: string,
  path: string,
  onFrame: (verb: WatchVerb, obj: unknown) => void,
  onStatus: (s: 'live' | 'fallback') => void,
): () => void {
  const controller = new AbortController();
  const sep = path.includes('?') ? '&' : '?';
  const url = `${API_BASE}/clusters/${clusterId}/k8s/${path}${sep}watch=true`;
  let cancelled = false;

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
      if (!cancelled) onStatus('fallback');
      return;
    }
    if (!res.ok || !res.body) {
      if (!cancelled) onStatus('fallback');
      return;
    }
    if (!cancelled) onStatus('live');

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
          let frame: { type?: string; object?: unknown };
          try {
            frame = JSON.parse(line);
          } catch {
            continue;
          }
          if (frame.type && isWatchVerb(frame.type) && frame.object) {
            if (!cancelled) onFrame(frame.type, frame.object);
          }
        }
      }
      // Stream closed cleanly (server ended the watch) — resume polling.
      if (!cancelled) onStatus('fallback');
    } catch {
      // Aborted on unmount, or the connection dropped mid-stream.
      if (!cancelled) onStatus('fallback');
    }
  })();

  return () => {
    cancelled = true;
    controller.abort();
  };
}

/**
 * Open the dedicated pods SSE watch (`GET /clusters/{id}/pods/watch/`) and
 * invoke `onFrame` for every ADDED/MODIFIED/DELETED event, reporting connection
 * state via `onStatus`. Returns a cleanup that closes the stream. On any open
 * failure (ticket mint, EventSource error) it reports `fallback` and stops —
 * reconnect policy belongs to the caller.
 */
export function openPodsWatch(
  clusterId: string,
  namespace: string | undefined,
  onFrame: (verb: WatchVerb, obj: unknown) => void,
  onStatus: (s: 'live' | 'fallback') => void,
): () => void {
  let cancelled = false;
  let es: EventSource | null = null;

  createStreamTicket('logs', clusterId)
    .then(({ ticket }) => {
      if (cancelled) return;
      const nsQ = namespace ? `namespace=${encodeURIComponent(namespace)}&` : '';
      const url = `${API_BASE}/clusters/${clusterId}/pods/watch/?${nsQ}ticket=${encodeURIComponent(ticket)}`;
      try {
        es = new EventSource(url, { withCredentials: false });
      } catch {
        if (!cancelled) onStatus('fallback');
        return;
      }
      es.onopen = () => {
        if (!cancelled) onStatus('live');
      };
      const onEvent = (verb: WatchVerb) => (ev: MessageEvent) => {
        if (cancelled) return;
        let obj: unknown;
        try {
          obj = ev.data ? JSON.parse(ev.data as string) : undefined;
        } catch {
          return;
        }
        if (obj) onFrame(verb, obj);
      };
      es.addEventListener('ADDED', onEvent('ADDED') as EventListener);
      es.addEventListener('MODIFIED', onEvent('MODIFIED') as EventListener);
      es.addEventListener('DELETED', onEvent('DELETED') as EventListener);
      es.onerror = () => {
        // The stream dropped (or never opened). Close rather than hammering
        // EventSource auto-reconnects — the ticket is one-use anyway.
        try {
          es?.close();
        } catch {
          /* ignore */
        }
        es = null;
        if (!cancelled) onStatus('fallback');
      };
    })
    .catch(() => {
      if (!cancelled) onStatus('fallback');
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
