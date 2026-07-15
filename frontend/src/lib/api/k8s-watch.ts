// Kubernetes proxy watch transport (P-02).
//
// Streams any curated list kind through the generic k8s passthrough
//   GET /api/v1/clusters/{id}/k8s/{path}?watch=true
// which the management plane chunk-forwards from the API server as
// newline-delimited JSON `{ "type": <verb>, "object": {...} }` frames.
// Auth is the session cookie (`credentials: 'include'`), so no ticket.
//
// This is the transport half of hooks/use-resource-watch.ts; it lives under
// lib/api/ so the raw streaming fetch stays inside the fetch-containment
// boundary.

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
