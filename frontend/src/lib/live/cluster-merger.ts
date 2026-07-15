/**
 * Convenience: subscribe to `cluster.metrics` / `cluster.status_changed`
 * and merge each tick into the cached cluster list/detail queries. Used by
 * widgets that want continuous percentage updates without paying a re-fetch
 * round-trip on every tick.
 *
 * `setQueryData` patching stays ONLY for these two tick types — every other
 * domain goes through invalidation (see P4.4 dispatcher); do not extend
 * patching to new domains.
 */

import { useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
// Aliased to `qk` to match the rest of the live module (the hooks file has a
// `queryKeys` parameter that would otherwise shadow the factory import).
import { queryKeys as qk } from '@/lib/query-keys';
import type {
  ClusterMetricsPayload,
  ClusterStatusChangedPayload,
  LiveEvent,
} from './envelope';
import { acquireLiveStream, liveTarget, releaseLiveStream } from './stream';

/**
 * Mount alongside `useLiveEvents()` in the dashboard layout. The optimistic
 * cache update is bounded — if the cluster row isn't found (e.g. the cluster
 * was just deleted), the tick is silently dropped.
 */
export function useLiveClusterMetricsMerger(): void {
  const queryClient = useQueryClient();

  useEffect(() => {
    acquireLiveStream();
    const target = liveTarget();

    const onMetrics = (e: Event) => {
      const detail = (e as CustomEvent<LiveEvent<ClusterMetricsPayload>>).detail;
      const payload = detail?.data;
      if (!payload?.clusterId) return;

      // Walk every cached `clusters list` query (across param variants) and
      // patch the affected row in place. queryKeys.clusters.list((params))
      // produces ['clusters', 'list', params] — the prefix match below
      // catches all of them at once.
      queryClient.setQueriesData(
        { queryKey: qk.clusters.listAll },
        (old: unknown) => mergeClusterMetricsIntoListResponse(old, payload),
      );

      // Detail page cache: ['clusters', 'detail', id]
      queryClient.setQueryData(qk.clusters.detail(payload.clusterId), (old: unknown) =>
        mergeClusterMetricsIntoDetail(old, payload),
      );
    };

    const onStatus = (e: Event) => {
      const detail = (e as CustomEvent<LiveEvent<ClusterStatusChangedPayload>>).detail;
      const payload = detail?.data;
      if (!payload?.clusterId || !payload?.newStatus) return;
      // Patch any cached list / detail responses to flip the status field
      // without waiting for the next refetch.
      queryClient.setQueriesData(
        { queryKey: qk.clusters.listAll },
        (old: unknown) => mergeClusterStatusIntoListResponse(old, payload.clusterId, payload.newStatus!),
      );
      queryClient.setQueryData(qk.clusters.detail(payload.clusterId), (old: unknown) =>
        mergeClusterStatusIntoDetail(old, payload.newStatus!),
      );
      // NOTE: we deliberately do NOT invalidateQueries here. The optimistic
      // patch above already reflects the new status; an unconditional
      // invalidate on every SSE tick queued an immediate refetch that could
      // race the patch and flip the row back to its stale server value. Any
      // dependent caches catch up via the query's own periodic refetch.
    };

    target.addEventListener('cluster.metrics', onMetrics as EventListener);
    target.addEventListener('cluster.status_changed', onStatus as EventListener);

    return () => {
      target.removeEventListener('cluster.metrics', onMetrics as EventListener);
      target.removeEventListener('cluster.status_changed', onStatus as EventListener);
      releaseLiveStream();
    };
  }, [queryClient]);
}

// --- Cache merge helpers ---
//
// We patch React Query caches in-place rather than firing invalidations on
// every metrics tick; invalidation would queue a network refetch every
// 10 seconds across every cluster, which defeats the point of the bus.
// Payloads arrive camelCase (envelope.ts camelizes centrally), so the old
// manual cpu_percentage→cpuPercentage mapping has collapsed.

interface ClusterListShape {
  data?: Array<{
    id: string;
    cpuPercentage?: number;
    memoryPercentage?: number;
    podCount?: number;
    status?: string;
  }>;
  total?: number;
  page?: number;
  pageSize?: number;
  totalPages?: number;
}

interface ClusterDetailShape {
  id: string;
  cpuPercentage?: number;
  memoryPercentage?: number;
  podCount?: number;
  status?: string;
}

function mergeClusterMetricsIntoListResponse(
  old: unknown,
  m: ClusterMetricsPayload,
): unknown {
  if (!old || typeof old !== 'object') return old;
  const list = old as ClusterListShape;
  if (!Array.isArray(list.data)) return old;
  const idx = list.data.findIndex((c) => c.id === m.clusterId);
  if (idx < 0) return old;
  const next = list.data.slice();
  next[idx] = {
    ...next[idx],
    cpuPercentage: m.cpuPercentage,
    memoryPercentage: m.memoryPercentage,
    podCount: m.podCount,
    // If a metrics tick arrives for a cluster the list still shows as
    // `disconnected`, the cluster is clearly active — flip the status
    // optimistically (the cluster.status_changed sweep will confirm
    // shortly).
    status: next[idx].status === 'disconnected' ? 'active' : next[idx].status,
  };
  return { ...list, data: next };
}

function mergeClusterMetricsIntoDetail(old: unknown, m: ClusterMetricsPayload): unknown {
  if (!old || typeof old !== 'object') return old;
  const c = old as ClusterDetailShape;
  if (c.id !== m.clusterId) return old;
  return {
    ...c,
    cpuPercentage: m.cpuPercentage,
    memoryPercentage: m.memoryPercentage,
    podCount: m.podCount,
    status: c.status === 'disconnected' ? 'active' : c.status,
  };
}

function mergeClusterStatusIntoListResponse(
  old: unknown,
  clusterId: string,
  newStatus: string,
): unknown {
  if (!old || typeof old !== 'object') return old;
  const list = old as ClusterListShape;
  if (!Array.isArray(list.data)) return old;
  const idx = list.data.findIndex((c) => c.id === clusterId);
  if (idx < 0) return old;
  const next = list.data.slice();
  next[idx] = { ...next[idx], status: newStatus };
  return { ...list, data: next };
}

function mergeClusterStatusIntoDetail(old: unknown, newStatus: string): unknown {
  if (!old || typeof old !== 'object') return old;
  const c = old as ClusterDetailShape;
  return { ...c, status: newStatus };
}
