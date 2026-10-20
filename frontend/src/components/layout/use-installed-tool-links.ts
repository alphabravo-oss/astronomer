import { useMemo } from "react";
import { useClusterToolsStatus, useTools } from "@/lib/hooks/tools";

export interface InstalledToolLink {
  name: string;
  url: string;
}

/** Proxied UI links for every installed tool (and its sub-services). */
export function useInstalledToolLinks(clusterId: string): InstalledToolLink[] {
  const { data: tools } = useTools();
  const { data: statuses } = useClusterToolsStatus(clusterId);
  return useMemo(() => {
    if (!tools || !statuses) return [];
    const statusMap = new Map<string, (typeof statuses)[number]>();
    statuses.forEach((status) => statusMap.set(status.slug, status));
    const links: InstalledToolLink[] = [];
    tools.forEach((tool) => {
      const status = statusMap.get(tool.slug);
      if (!status || status.status !== "installed") return;
      const ns = status.namespace || tool.defaultNamespace;
      if (!ns) return;
      if (tool.serviceName && tool.servicePort) {
        links.push({
          name: tool.name,
          url: `/api/v1/clusters/${clusterId}/proxy/service/${ns}/${tool.serviceName}:${tool.servicePort}${tool.servicePath || "/"}`,
        });
      }
      tool.subServices?.forEach((sub) => {
        links.push({
          name: sub.name,
          url: `/api/v1/clusters/${clusterId}/proxy/service/${ns}/${sub.service}:${sub.port}/`,
        });
      });
    });
    return links;
  }, [clusterId, statuses, tools]);
}
