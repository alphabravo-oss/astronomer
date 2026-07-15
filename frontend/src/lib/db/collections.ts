/**
 * TanStack DB collections for the two stream-fed k8s surfaces (P4.7).
 *
 * `k8sCollection({ clusterId, source })` returns a cached collection of raw
 * Kubernetes objects kept live by folding watch frames:
 *
 *  - `{ kind: 'pods', namespace? }` — the dedicated pods SSE watch (raw pod
 *    objects, per cluster + optional namespace).
 *  - `{ kind: 'proxy', path }` — a curated list kind via the generic k8s
 *    passthrough's NDJSON `?watch=true` stream (the 5 workload kinds).
 *
 * These are the only streams carrying full objects with ADDED/MODIFIED/DELETED
 * verbs; everything else stays plain React Query + event invalidation. Do not
 * add per-page proxy watches here — the proxy rate limiter is shared.
 *
 * Sync lifecycle (starts on first subscriber, cleaned up by collection GC):
 *  1. Seed: REST list via lib/api (`/k8s/` paths are camel-exempt raw JSON).
 *  2. Stream: fold each frame as insert/update/delete (upsert by identity —
 *     a reconnecting watch replays synthetic ADDEDs for existing objects).
 *  3. Drop: mark the handle's status `fallback` and retry with exponential
 *     backoff; each retry re-seeds (truncate + fresh list) so deletions missed
 *     while disconnected cannot leave ghost rows.
 */

import { createCollection, type Collection } from '@tanstack/db';
import { Store } from '@tanstack/store';
import { k8sGet } from '@/lib/api';
import { openPodsWatch, openProxyWatch, type WatchVerb } from '@/lib/api/k8s-watch';
import { formatRelativeTime } from '@/lib/utils';
import type { Container, Pod, PodPhase } from '@/types';

interface K8sObjectMeta {
  name?: string;
  namespace?: string;
  uid?: string;
}

export interface K8sObject {
  metadata?: K8sObjectMeta;
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

/** Watch source: the dedicated pods SSE stream, or a generic proxy list path. */
export type WatchSource =
  | { kind: 'pods'; namespace?: string }
  | { kind: 'proxy'; path: string };

/**
 * Connection status of a collection's watch stream. `error` means the list
 * seed itself failed (proxy 5xx, agent offline) — the collection is marked
 * ready but empty so consumers render rather than spin; `fallback` means the
 * seed worked but the stream is down. Both retry with backoff.
 */
export type K8sWatchStatus = 'idle' | 'connecting' | 'live' | 'fallback' | 'error';

export interface K8sCollectionHandle<T extends K8sObject> {
  collection: Collection<T, string>;
  /** Reactive stream status — subscribe with `useStore` for Live badges. */
  status: Store<K8sWatchStatus>;
}

const RETRY_BASE_MS = 1_000;
const RETRY_MAX_MS = 30_000;

const handles = new Map<string, K8sCollectionHandle<K8sObject>>();

function sourceKey(clusterId: string, source: WatchSource): string {
  return source.kind === 'pods'
    ? `${clusterId}|pods|${source.namespace ?? ''}`
    : `${clusterId}|proxy|${source.path}`;
}

function seedPath(source: WatchSource): string {
  if (source.kind === 'proxy') return source.path;
  return source.namespace
    ? `api/v1/namespaces/${encodeURIComponent(source.namespace)}/pods`
    : 'api/v1/pods';
}

/**
 * Get (or create) the live collection for one cluster + watch source. Handles
 * are cached per (cluster, source) so every consumer folds the same stream;
 * sync starts on first subscriber and is torn down by collection GC when the
 * last subscriber leaves.
 */
export function k8sCollection<T extends K8sObject>(opts: {
  clusterId: string;
  source: WatchSource;
}): K8sCollectionHandle<T> {
  const { clusterId, source } = opts;
  const key = sourceKey(clusterId, source);
  const hit = handles.get(key);
  if (hit) return hit as unknown as K8sCollectionHandle<T>;

  const status = new Store<K8sWatchStatus>('idle');
  const collection = createCollection<T, string>({
    id: `k8s:${key}`,
    getKey: (obj) => identityOf(obj),
    sync: {
      // Watch frames carry whole objects, never patches.
      rowUpdateMode: 'full',
      sync: ({ collection: col, begin, write, commit, markReady, truncate }) => {
        let stopped = false;
        let stopStream: (() => void) | null = null;
        let retryTimer: ReturnType<typeof setTimeout> | null = null;
        let attempt = 0;

        const setStatus = (s: K8sWatchStatus) => status.setState(() => s);

        const apply = (verb: WatchVerb, obj: T | undefined) => {
          if (stopped || !obj || !obj.metadata) return;
          const id = identityOf(obj);
          begin();
          if (verb === 'DELETED') {
            if (col.has(id)) write({ type: 'delete', key: id });
          } else {
            // Upsert: a fresh watch replays existing objects as ADDED.
            write({ type: col.has(id) ? 'update' : 'insert', value: obj });
          }
          commit();
        };

        const scheduleRetry = () => {
          if (stopped || retryTimer) return;
          const delay = Math.min(RETRY_MAX_MS, RETRY_BASE_MS * 2 ** attempt);
          attempt += 1;
          retryTimer = setTimeout(() => {
            retryTimer = null;
            void run();
          }, delay);
        };

        const onStreamStatus = (s: 'live' | 'fallback') => {
          if (stopped) return;
          if (s === 'live') {
            attempt = 0;
            setStatus('live');
            return;
          }
          // Stream dropped (or never opened): retry with backoff; the next
          // run() re-seeds so deletes missed while down cannot ghost.
          stopStream?.();
          stopStream = null;
          setStatus('fallback');
          scheduleRetry();
        };

        const run = async () => {
          if (stopped) return;
          setStatus('connecting');
          let list: { items?: T[] } | undefined;
          try {
            list = (await k8sGet(clusterId, seedPath(source))) as { items?: T[] };
          } catch {
            if (stopped) return;
            // Seed failed — surface the error but mark ready so consumers
            // render (empty) instead of spinning forever, then retry.
            setStatus('error');
            markReady();
            scheduleRetry();
            return;
          }
          if (stopped) return;
          begin();
          truncate();
          for (const obj of list?.items ?? []) {
            if (obj && obj.metadata) write({ type: 'insert', value: obj });
          }
          commit();
          markReady();
          stopStream =
            source.kind === 'pods'
              ? openPodsWatch(clusterId, source.namespace, (verb, obj) => apply(verb, obj as T), onStreamStatus)
              : openProxyWatch(clusterId, source.path, (verb, obj) => apply(verb, obj as T), onStreamStatus);
        };

        void run();

        return () => {
          stopped = true;
          if (retryTimer) clearTimeout(retryTimer);
          retryTimer = null;
          stopStream?.();
          stopStream = null;
          setStatus('idle');
        };
      },
    },
  });

  const handle: K8sCollectionHandle<T> = { collection, status };
  handles.set(key, handle as unknown as K8sCollectionHandle<K8sObject>);
  return handle;
}

// ---------------------------------------------------------------------------
// Raw pod → display row (client-side port of internal/handler/workloads.go
// podToMap, which shaped the retired transformed /clusters/{id}/pods list).
// ---------------------------------------------------------------------------

export interface RawPod {
  metadata: {
    name: string;
    namespace: string;
    uid?: string;
    creationTimestamp?: string;
  };
  spec?: {
    nodeName?: string;
    containers?: Array<{
      name: string;
      image: string;
      ports?: Array<{ name?: string; containerPort: number; protocol: string }>;
    }>;
  };
  status?: {
    phase?: string;
    podIP?: string;
    containerStatuses?: Array<{
      name: string;
      ready?: boolean;
      restartCount?: number;
      state?: { running?: unknown; waiting?: unknown; terminated?: unknown };
    }>;
    conditions?: Array<{
      type: string;
      status: string;
      reason?: string;
      message?: string;
      lastTransitionTime?: string;
    }>;
  };
}

/** kubectl-style short age ("5m" / "3h" / "2d") — mirrors the server's humanAge. */
function podAge(createdAt: string): string {
  const ts = Date.parse(createdAt);
  if (Number.isNaN(ts)) return createdAt ? formatRelativeTime(createdAt) : '';
  const mins = Math.max(0, Math.floor((Date.now() - ts) / 60_000));
  if (mins < 60) return `${mins}m`;
  if (mins < 24 * 60) return `${Math.floor(mins / 60)}h`;
  return `${Math.floor(mins / (24 * 60))}d`;
}

/** Shape a raw pod object (from the pods collection) into the `Pod` row the tables render. */
export function podRowFromRaw(clusterId: string, pod: RawPod): Pod {
  const specContainers = pod.spec?.containers ?? [];
  let readyCount = 0;
  let restarts = 0;
  const containers: Container[] = specContainers.map((c) => {
    const cs = pod.status?.containerStatuses?.find((s) => s.name === c.name);
    let state: Container['status'] = 'waiting';
    if (cs?.state?.running) state = 'running';
    else if (cs?.state?.terminated) state = 'terminated';
    if (cs?.ready) readyCount += 1;
    restarts += cs?.restartCount ?? 0;
    return {
      name: c.name,
      image: c.image,
      status: state,
      ready: !!cs?.ready,
      restartCount: cs?.restartCount ?? 0,
      ports: (c.ports ?? []).map((p) => ({
        name: p.name,
        containerPort: p.containerPort,
        protocol: p.protocol,
      })),
    };
  });
  const phase = (pod.status?.phase ?? 'Unknown') as PodPhase;
  const createdAt = pod.metadata.creationTimestamp ?? '';
  return {
    name: pod.metadata.name,
    namespace: pod.metadata.namespace,
    clusterId,
    phase,
    status: phase,
    ready: `${readyCount}/${specContainers.length}`,
    restarts,
    node: pod.spec?.nodeName ?? '',
    ip: pod.status?.podIP ?? '',
    containers,
    conditions: (pod.status?.conditions ?? []).map((c) => ({
      type: c.type,
      status: c.status,
      reason: c.reason,
      message: c.message,
      lastTransition: c.lastTransitionTime ?? '',
    })),
    createdAt,
    age: podAge(createdAt),
  };
}
