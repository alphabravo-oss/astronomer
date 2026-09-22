import { useQuery } from "@tanstack/react-query";
import { getClusterStackStatus } from "@/lib/api/monitoring-stack";
import { queryKeys } from "@/lib/query-keys";

export function useClusterStackStatus(clusterId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.monitoringStack.status(`cluster:${clusterId ?? ""}`),
    queryFn: () => getClusterStackStatus(clusterId as string),
    enabled: !!clusterId,
  });
}
