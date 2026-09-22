import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { k8sGet } from "@/lib/api/kubernetes-proxy";
import { k8sQueryKeys } from "@/lib/hooks/kubernetes-proxy";
import { can } from "@/lib/permissions";
import { useAuthStore } from "@/lib/store";
import {
  CRD_DISCOVERY_PATH,
  clusterDiscoveryFromDefinitions,
  type ClusterDiscovery,
} from "./cluster-discovery-model";

export function useClusterDiscovery(clusterId?: string): ClusterDiscovery {
  const user = useAuthStore((state) => state.user);
  const allowed =
    !!clusterId &&
    can(user, "custom_resources", "read", {
      type: "cluster",
      id: clusterId,
    });
  const query = useQuery({
    queryKey: k8sQueryKeys.resource(clusterId ?? "", CRD_DISCOVERY_PATH),
    queryFn: ({ signal }) => k8sGet(clusterId!, CRD_DISCOVERY_PATH, signal),
    enabled: allowed,
    staleTime: 5 * 60_000,
    retry: false,
    throwOnError: false,
  });
  return useMemo(
    () => ({
      ...clusterDiscoveryFromDefinitions(
        allowed && !query.isError ? (query.data?.items ?? []) : [],
      ),
      isLoading: allowed && query.isPending,
      // A denied or failed discovery request is not evidence of absent CRDs.
      isError: !allowed || query.isError,
    }),
    [allowed, query.data, query.isError, query.isPending],
  );
}
