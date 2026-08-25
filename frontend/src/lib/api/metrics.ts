import {
  getClustersByClusterIdWorkloadsByKindByNamespaceByNameMetrics,
  getClustersByIdMetrics,
  getClustersByIdMetricsSummary,
} from "@/lib/api/generated/client";
import type { MetricsData, MetricsSummary } from "@/types";

export async function getClusterMetrics(
  clusterId: string,
  params?: { range?: string },
  signal?: AbortSignal,
): Promise<MetricsData> {
  const range = params?.range as "1h" | "6h" | "24h" | "7d" | undefined;
  const response = await getClustersByIdMetrics({
    path: { id: clusterId },
    query: range ? { range } : undefined,
    signal,
  });
  return response.data as unknown as MetricsData;
}

export async function getClusterMetricsSummary(
  clusterId: string,
  signal?: AbortSignal,
): Promise<MetricsSummary> {
  const response = await getClustersByIdMetricsSummary({ path: { id: clusterId }, signal });
  return response.data as unknown as MetricsSummary;
}

export async function getWorkloadMetrics(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
  params?: { range?: string },
  signal?: AbortSignal,
): Promise<MetricsData> {
  const range = params?.range as "1h" | "6h" | "24h" | "7d" | undefined;
  const response = await getClustersByClusterIdWorkloadsByKindByNamespaceByNameMetrics({
    path: { cluster_id: clusterId, kind, namespace, name },
    query: range ? { range } : undefined,
    signal,
  });
  return response.data as unknown as MetricsData;
}
