/**
 * Transport for the generic Kubernetes proxy watch endpoint.
 *
 * This module deliberately owns only HTTP and NDJSON concerns. React
 * lifecycle, cache updates, debounce behavior, and polling fallback decisions
 * remain in the resource-watch hooks.
 */

const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL || '/api/v1';

/** Kubernetes watch verbs that carry objects the UI can apply to its cache. */
export type ResourceWatchVerb = 'ADDED' | 'MODIFIED' | 'DELETED';

export type ResourceWatchTransportStatus = 'live' | 'fallback';

export interface ConsumeResourceWatchOptions {
  clusterId: string;
  /** Kubernetes API path below the management-plane `/k8s/` proxy. */
  path: string;
  onFrame: (verb: ResourceWatchVerb, object: unknown) => void;
  onStatus: (status: ResourceWatchTransportStatus) => void;
}

function isResourceWatchVerb(value: string): value is ResourceWatchVerb {
  return value === 'ADDED' || value === 'MODIFIED' || value === 'DELETED';
}

/**
 * Consume a chunked NDJSON Kubernetes watch and return a cancellation handle.
 *
 * The request uses the browser session cookie and accepts JSON exactly as the
 * previous hook-local transport did. Invalid and unsupported frames are
 * ignored so one bad upstream line does not terminate an otherwise healthy
 * watch. A cleanly ended or failed stream reports `fallback`, allowing the
 * React layer to resume polling.
 */
export function consumeResourceWatch({
  clusterId,
  path,
  onFrame,
  onStatus,
}: ConsumeResourceWatchOptions): () => void {
  const controller = new AbortController();
  const separator = path.includes('?') ? '&' : '?';
  const url = `${API_BASE_URL}/clusters/${clusterId}/k8s/${path}${separator}watch=true`;
  let cancelled = false;

  void (async () => {
    let response: Response;
    try {
      response = await fetch(url, {
        method: 'GET',
        credentials: 'include',
        signal: controller.signal,
        headers: { Accept: 'application/json' },
      });
    } catch {
      if (!cancelled) onStatus('fallback');
      return;
    }

    if (!response.ok || !response.body) {
      if (!cancelled) onStatus('fallback');
      return;
    }

    if (!cancelled) onStatus('live');

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';

    try {
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        let newline = buffer.indexOf('\n');
        while (newline >= 0) {
          const line = buffer.slice(0, newline).trim();
          buffer = buffer.slice(newline + 1);
          newline = buffer.indexOf('\n');
          if (!line) continue;

          let frame: { type?: string; object?: unknown };
          try {
            frame = JSON.parse(line) as { type?: string; object?: unknown };
          } catch {
            continue;
          }

          if (frame.type && isResourceWatchVerb(frame.type) && frame.object) {
            if (!cancelled) onFrame(frame.type, frame.object);
          }
        }
      }

      if (!cancelled) onStatus('fallback');
    } catch {
      if (!cancelled) onStatus('fallback');
    }
  })();

  return () => {
    cancelled = true;
    controller.abort();
  };
}
