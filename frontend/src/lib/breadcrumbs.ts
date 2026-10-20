import { navLabelForHref } from "@/components/layout/sidebar-navigation";

/**
 * Segment → label registry for breadcrumbs. Sentence case everywhere;
 * Kubernetes kinds keep their canonical casing (ConfigMaps, DaemonSets, CRDs).
 * Nav-registered hrefs are resolved through `navLabelForHref` first, so a
 * destination never carries a different name in different chrome.
 */
const routeLabels: Record<string, string> = {
  dashboard: "Overview",
  clusters: "Clusters",
  workloads: "Workloads",
  monitoring: "Metrics",
  stacks: "Shared stacks",
  alerting: "Alerting",
  logging: "Logging",
  storage: "Storage",
  networking: "Networking",
  delivery: "Delivery",
  rbac: "Access control",
  projects: "Projects",
  settings: "Settings",
  register: "Register",
  audit: "Audit log",
  catalog: "Catalog",
  security: "Security",
  search: "Search",
  resources: "Mirrored resources",
  shell: "Shell",
  adoption: "Adoption",
  tools: "Tools",
  apps: "Applications",
  registries: "Registries",
  snapshots: "Snapshots",
  extensions: "Extensions",
  agents: "Agents",
  pods: "Pods",
  deployments: "Deployments",
  daemonsets: "DaemonSets",
  statefulsets: "StatefulSets",
  replicasets: "ReplicaSets",
  jobs: "Jobs",
  cronjobs: "CronJobs",
  services: "Services",
  ingresses: "Ingresses",
  configmaps: "ConfigMaps",
  secrets: "Secrets",
  hpa: "Autoscalers",
  "network-policies": "Network policies",
  persistentvolumes: "Persistent volumes",
  persistentvolumeclaims: "Volume claims",
  storageclasses: "Storage classes",
  resourcequotas: "Resource quotas",
  limitranges: "Limit ranges",
  poddisruptionbudgets: "Disruption budgets",
  crds: "CRDs",
  "custom-resources": "Custom resources",
  serviceaccounts: "Service accounts",
  "k8s-clusterroles": "Cluster roles",
  "k8s-clusterrolebindings": "Cluster role bindings",
  "k8s-roles": "Roles",
  "k8s-rolebindings": "Role bindings",
  endpoints: "Endpoints",
  namespaces: "Namespaces",
  nodes: "Nodes",
  events: "Events",
  charlie: "Charlie",
  "cluster-templates": "Onboarding templates",
  "configuration-templates": "Templates",
  "override-sets": "Overrides",
  "image-scans": "Image scans",
  "monitoring-stack": "Monitoring stack",
  "service-mesh": "Service mesh",
  "control-plane-snapshots": "Control-plane DR",
  "network-access": "Network & access",
  template: "Onboarding template",
  gatekeeper: "Gatekeeper",
  grafana: "Grafana",
  metrics: "Metrics",
  account: "Account",
  preferences: "Preferences",
  new: "New",
  edit: "Edit",
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
  const words = decoded.replace(/[-_]+/g, " ").trim();
  return words.charAt(0).toUpperCase() + words.slice(1);
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
        : (navLabelForHref(path) ?? breadcrumbLabel(segment));
    crumbs.push({ label, href: path });
  }

  return crumbs;
}
