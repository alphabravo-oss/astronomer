import { useNavigate, useLocation } from "@tanstack/react-router";
import type { DeliveryEstateCluster } from "@/lib/api/delivery-system";
import { clusterDeliveryPath } from "@/components/delivery/shared";

export function clusterHref(clusterId: string): string {
  return clusterDeliveryPath(clusterId);
}

export function clusterMatchesFocus(
  cluster: DeliveryEstateCluster,
  focus: string,
): boolean {
  switch (focus) {
    case "adopted":
      return !cluster.isLocal;
    case "flux_ready":
      return (
        !cluster.isLocal &&
        cluster.connected &&
        cluster.inventoryReady &&
        cluster.compatibilityStatus === "compatible"
      );
    case "incompatible":
      return (
        !cluster.isLocal &&
        ["incompatible", "upgrade_required", "degraded"].includes(
          cluster.compatibilityStatus,
        )
      );
    case "disconnected":
      return !cluster.isLocal && !cluster.connected;
    case "assignments":
      return cluster.assignmentCount > 0;
    case "failed":
      return cluster.failedCount > 0;
    case "drifted":
      return cluster.driftedCount > 0;
    default:
      if (focus.startsWith("compatibility:")) {
        return (
          !cluster.isLocal &&
          cluster.compatibilityStatus === focus.slice("compatibility:".length)
        );
      }
      if (focus.startsWith("privilege:")) {
        return (
          !cluster.isLocal &&
          cluster.privilegeProfile === focus.slice("privilege:".length)
        );
      }
      if (focus.startsWith("phase:")) {
        const phase = focus.slice("phase:".length);
        if (phase === "ready") return cluster.readyCount > 0;
        if (phase === "failed") return cluster.failedCount > 0;
        if (phase === "degraded") return cluster.degradedCount > 0;
        return false;
      }
      return true;
  }
}

export function useEstateFocus(clusters: DeliveryEstateCluster[]) {
  const navigate = useNavigate();
  const pathname = useLocation({ select: (location) => location.pathname });
  const search = new URLSearchParams(
    useLocation({ select: (location) => location.searchStr }),
  );
  const focus = search.get("focus") ?? "";
  const visible = focus
    ? clusters.filter((cluster) => clusterMatchesFocus(cluster, focus))
    : clusters;

  const setFocus = (next: string) => {
    const matches = clusters.filter((cluster) =>
      clusterMatchesFocus(cluster, next),
    );
    if (matches.length === 1) {
      void navigate({ to: clusterHref(matches[0].id) });
      return;
    }
    const params = new URLSearchParams(search);
    if (next) params.set("focus", next);
    else params.delete("focus");
    void navigate({
      to: `${pathname}${params.size ? `?${params.toString()}` : ""}`,
      replace: true,
    });
    requestAnimationFrame(() => {
      document
        .getElementById("estate-clusters")
        ?.scrollIntoView({ behavior: "smooth", block: "start" });
    });
  };
  return { focus, visible, setFocus, navigate };
}
