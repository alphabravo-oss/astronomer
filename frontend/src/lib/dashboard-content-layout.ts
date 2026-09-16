export type DashboardContentLayout = "contained" | "full-bleed";

const DENSE_COLLECTION_SEGMENTS = new Set([
  "agents",
  "audit",
  "clusters",
  "cronjobs",
  "daemonsets",
  "deployments",
  "endpoints",
  "events",
  "gatewayclasses",
  "gateways",
  "grpcroutes",
  "httproutes",
  "ingresses",
  "jobs",
  "namespaces",
  "networkpolicies",
  "nodes",
  "persistentvolumeclaims",
  "persistentvolumes",
  "poddisruptionbudgets",
  "pods",
  "replicasets",
  "resourcequotas",
  "resources",
  "rolebindings",
  "roles",
  "secrets",
  "serviceaccounts",
  "services",
  "statefulsets",
  "storageclasses",
  "tcproutes",
  "tlsroutes",
  "udproutes",
  "workloads",
]);

/**
 * Dense operator surfaces opt out of the centered reading-width container.
 * Keeping this policy next to the dashboard shell makes width behavior stable
 * across route transitions and gives new table/log/YAML routes one reviewable
 * place to declare their intent.
 */
export function dashboardContentLayout(
  pathname: string,
  searchString = "",
): DashboardContentLayout {
  const search = new URLSearchParams(searchString);
  const activeView = search.get("tab") ?? search.get("view");
  if (activeView === "yaml" || activeView === "logs") return "full-bleed";

  const segments = pathname.split("/").filter(Boolean);
  const leaf = segments.at(-1) ?? "";
  if (leaf === "logging" || leaf === "logs") return "full-bleed";
  return DENSE_COLLECTION_SEGMENTS.has(leaf) ? "full-bleed" : "contained";
}
