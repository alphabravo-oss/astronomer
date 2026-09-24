import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { getCompleteResourceDiscovery } from "@/lib/api/resources";
import { queryKeys } from "@/lib/query-keys";
import { can } from "@/lib/permissions";
import { useAuthStore } from "@/lib/store";
import {
  clusterDiscoveryFromSummaries,
  type ClusterDiscovery,
} from "./cluster-discovery-model";

export function useClusterDiscovery(clusterId?: string): ClusterDiscovery {
  const user = useAuthStore((state) => state.user);
  const allowed =
    !!clusterId &&
    can(user, "clusters", "read", {
      type: "cluster",
      id: clusterId,
    });
  const query = useQuery({
    queryKey: [...queryKeys.generic.discovery(clusterId ?? ""), "complete"],
    queryFn: ({ signal }) => getCompleteResourceDiscovery(clusterId!, signal),
    enabled: allowed,
    staleTime: 5 * 60_000,
    retry: false,
    throwOnError: false,
  });
  return useMemo(
    () => ({
      ...clusterDiscoveryFromSummaries(
        allowed && !query.isError ? (query.data?.crds ?? []) : [],
      ),
      isLoading: allowed && query.isPending,
      // A denied or failed discovery request is not evidence of absent CRDs.
      isError: !allowed || query.isError,
      retry: query.refetch,
    }),
    [allowed, query.data, query.isError, query.isPending, query.refetch],
  );
}
