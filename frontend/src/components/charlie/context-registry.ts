import type { CharlieContextOption } from "@/lib/api/charlie";

const safeId = (value: string) => {
  try {
    return decodeURIComponent(value)
      .replace(/[^a-zA-Z0-9._:@/-]/g, "")
      .slice(0, 255);
  } catch {
    return "";
  }
};
const item = (
  type: CharlieContextOption["type"],
  id: string,
  label: string,
  summary: string,
): CharlieContextOption => ({
  type,
  id: safeId(id),
  requiredVerb: "read",
  label,
  summary: summary.slice(0, 160),
});

export interface CharlieRouteContextAdapter {
  id: string;
  match: (parts: string[]) => boolean;
  contexts: (parts: string[]) => CharlieContextOption[];
}

/**
 * Explicit, auditable page adapters. Empty adapters are deliberate: global
 * metrics, logs, and audit pages do not identify one bounded resource, so they
 * attach nothing and leave evidence retrieval to an authorized MCP call.
 */
export const charlieContextRegistry: CharlieRouteContextAdapter[] = [
  {
    id: "downstream_agent_record_from_component_page",
    match: (p) =>
      p[1] === "clusters" && !!p[2] && (p[3] === "tools" || p[3] === "apps"),
    contexts: (p) => [
      item(
        "agent_connection_record",
        p[2],
        "Cluster agent connection",
        "Astronomer-owned connection metadata only; no downstream Kubernetes access",
      ),
    ],
  },
  {
    id: "monitoring",
    match: (p) =>
      p[1] === "monitoring" ||
      (p[1] === "clusters" && !!p[2] && p[3] === "monitoring-stack"),
    contexts: (p) =>
      p[1] === "clusters" && p[2]
        ? [
            item(
              "agent_connection_record",
              p[2],
              "Cluster agent connection",
              "Astronomer-owned connection metadata only; no downstream monitoring data",
            ),
          ]
        : [],
  },
  { id: "logging", match: (p) => p[1] === "logging", contexts: () => [] },
  { id: "audit", match: (p) => p[1] === "audit", contexts: () => [] },
  {
    id: "alerts",
    match: (p) => p[1] === "alerting",
    contexts: () => [
      item("alert", "active", "Alerts", "Current authorized alert scope"),
    ],
  },
  {
    id: "backups",
    match: (p) =>
      p[1] === "backups" || (p[1] === "settings" && p[2] === "backup"),
    contexts: (p) => [
      item(
        "backup",
        p[1] === "settings" ? "management" : p[2] || "overview",
        "Astronomer backup",
        "Management-plane dump status; workload snapshots stay on the cluster",
      ),
    ],
  },
  {
    id: "continuous_delivery",
    match: (p) => p[1] === "delivery",
    contexts: (p) => [
      item(
        "self_management_application",
        p[3] || p[2] || "overview",
        "Continuous delivery",
        "Flux-native delivery scope",
      ),
    ],
  },
  {
    id: "agent_tunnel",
    match: (p) => p[1] === "agents" && !!p[2] && p[3] === "tunnel",
    contexts: (p) => [
      item(
        "tunnel",
        p[2],
        "Agent tunnel",
        "Selected product tunnel connection",
      ),
    ],
  },
  {
    id: "agent_record",
    match: (p) => p[1] === "agents" && !!p[2],
    contexts: (p) => [
      item(
        "agent_connection_record",
        p[2],
        "Cluster agent",
        "Selected Astronomer cluster-agent connection record",
      ),
    ],
  },
  {
    id: "cluster_agents",
    match: (p) => p[1] === "agents",
    contexts: (p) => [
      item(
        "cluster_agents",
        p[2] || "all",
        "Cluster agents",
        "Cluster agent connection health",
      ),
    ],
  },
  {
    id: "downstream_agent_record",
    match: (p) => p[1] === "clusters" && !!p[2],
    contexts: (p) => [
      item(
        "agent_connection_record",
        p[2],
        "Cluster agent connection",
        "Astronomer-owned connection health only; no downstream Kubernetes access",
      ),
    ],
  },
];

export function contextForRoute(pathname: string): CharlieContextOption[] {
  const parts = pathname.split("/").filter(Boolean);
  return (
    charlieContextRegistry
      .find((adapter) => adapter.match(parts))
      ?.contexts(parts) ?? []
  );
}
