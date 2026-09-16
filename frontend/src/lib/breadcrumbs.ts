const routeLabels: Record<string, string> = {
  dashboard: "Dashboard",
  clusters: "Clusters",
  workloads: "Workloads",
  monitoring: "Monitoring",
  alerting: "Alerting",
  logging: "Logging",
  storage: "Storage",
  networking: "Networking",
  delivery: "Continuous Delivery",
  rbac: "RBAC",
  projects: "Projects",
  settings: "Settings",
  register: "Register",
  audit: "Audit",
  catalog: "Catalog",
  security: "Security",
  fleet: "Fleet Operations",
  search: "Search",
  resources: "Resources",
  shell: "Shell",
  adoption: "Adoption",
  tools: "Tools",
  apps: "Apps",
  registries: "Registries",
  snapshots: "Snapshots",
  extensions: "Extensions",
  agents: "Agents",
  pods: "Pods",
  deployments: "Deployments",
  daemonsets: "DaemonSets",
  statefulsets: "StatefulSets",
  jobs: "Jobs",
  cronjobs: "CronJobs",
  services: "Services",
  ingresses: "Ingresses",
  configmaps: "ConfigMaps",
  secrets: "Secrets",
  hpa: "HPA",
  "network-policies": "Network Policies",
  "persistent-volumes": "Persistent Volumes",
  "persistent-volume-claims": "PVCs",
  "storage-classes": "Storage Classes",
  resourcequotas: "Resource Quotas",
  limitranges: "Limit Ranges",
  poddisruptionbudgets: "PDBs",
  crds: "CRDs",
  serviceaccounts: "Service Accounts",
  "k8s-clusterroles": "Cluster Roles",
  "k8s-clusterrolebindings": "Cluster Role Bindings",
  "k8s-roles": "Roles",
  "k8s-rolebindings": "Role Bindings",
  endpoints: "Endpoints",
  replicasets: "ReplicaSets",
  namespaces: "Namespaces",
  nodes: "Nodes",
  events: "Events",
  charlie: "Charlie",
};

const opaqueSegment = /^[0-9a-f]{8}-[0-9a-f-]{27,}$/i;

export function breadcrumbLabel(segment: string): string {
  let decoded = segment;
  try {
    decoded = decodeURIComponent(segment);
  } catch {
    // Keep malformed URL segments legible instead of breaking dashboard chrome.
  }
  if (routeLabels[decoded]) return routeLabels[decoded];
  if (opaqueSegment.test(decoded)) return decoded;
  return decoded
    .replace(/[-_]+/g, " ")
    .replace(/\b\w/g, (character) => character.toUpperCase());
}

export type Breadcrumb = { label: string; href: string };

export function generateBreadcrumbs(
  pathname: string,
  clusterMap?: Record<string, string>,
): Breadcrumb[] {
  const segments = pathname.split("/").filter(Boolean);
  const crumbs: Breadcrumb[] = [];
  let path = "";

  for (let i = 0; i < segments.length; i++) {
    const segment = segments[i];
    path += `/${segment}`;
    const label =
      segments[i - 1] === "clusters" && clusterMap?.[segment]
        ? clusterMap[segment]
        : breadcrumbLabel(segment);
    crumbs.push({ label, href: path });
  }

  return crumbs;
}
