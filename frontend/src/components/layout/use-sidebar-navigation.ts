import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { getVeleroStatus } from "@/lib/api/cluster-velero";
import {
  useCharlieActivated,
  useCluster,
  useFeatureFlags,
} from "@/lib/hooks/clusters";
import { useClusterStackStatus } from "@/components/monitoring/use-cluster-stack-status";
import { queryKeys } from "@/lib/query-keys";
import { useAuthStore } from "@/lib/store";
import { useUserPreferences } from "@/lib/user-preferences";
import {
  filterNavGroups,
  getClusterNavGroups,
  globalNavGroups,
  withFavoriteNavigation,
} from "./sidebar-navigation";
import {
  filterNavGroupsByDiscovery,
  withDiscoveredNavigation,
  withStarredTypes,
} from "./cluster-discovery-navigation";
import { useClusterDiscovery } from "./use-cluster-discovery-nav";

export function useSidebarNavigation(
  clusterId?: string,
  completeIndex = false,
) {
  const user = useAuthStore((state) => state.user);
  const { data: cluster } = useCluster(clusterId ?? "");
  const { data: featureFlags } = useFeatureFlags();
  const { activated: charlieActivated } = useCharlieActivated();
  const { preferences } = useUserPreferences();
  const discovery = useClusterDiscovery(clusterId);
  const { data: veleroStatus } = useQuery({
    queryKey: queryKeys.clusterPages.veleroStatus(clusterId ?? ""),
    queryFn: ({ signal }) => getVeleroStatus(clusterId!, signal),
    enabled: !!clusterId && !cluster?.isLocal,
    staleTime: 30_000,
  });
  const { data: monitoringStatus } = useClusterStackStatus(clusterId);
  const navGroups = useMemo(() => {
    const baseGroups = clusterId
      ? getClusterNavGroups(clusterId, {
          isLocal: cluster?.isLocal,
          veleroInstalled: !!veleroStatus?.installed,
          grafanaAvailable: monitoringStatus?.grafanaAvailable === true,
        })
      : globalNavGroups;
    const groups = clusterId
      ? withDiscoveredNavigation(
          baseGroups,
          discovery,
          clusterId,
          preferences.starred_types,
          completeIndex ? Infinity : 40,
        )
      : withFavoriteNavigation(baseGroups, preferences.favorites);
    const visible = filterNavGroups(
      groups,
      user,
      featureFlags,
      charlieActivated,
      clusterId ? { type: "cluster", id: clusterId } : { type: "global" },
    );
    return clusterId
      ? withStarredTypes(
          filterNavGroupsByDiscovery(visible, discovery),
          preferences.starred_types ?? [],
        )
      : visible;
  }, [
    completeIndex,
    charlieActivated,
    cluster?.isLocal,
    clusterId,
    featureFlags,
    preferences.favorites,
    monitoringStatus?.grafanaAvailable,
    user,
    veleroStatus?.installed,
    discovery,
    preferences.starred_types,
  ]);
  return { cluster, discovery, navGroups };
}
