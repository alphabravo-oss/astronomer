/**
 * Declarative routing metadata for the cluster resource explorer.
 *
 * Keeping this data outside the page component makes supported routes,
 * workload adapters, and create affordances independently testable. The
 * objects are intentionally immutable so a render cannot mutate the route
 * contract shared by navigation and table adapters.
 */

export interface CreatableGenericResource {
  readonly templateKey: string;
  readonly label: string;
}

export const WORKLOAD_TEMPLATE_BY_KIND: Readonly<Record<string, string>> =
  Object.freeze({
    Deployment: "deployment",
    StatefulSet: "statefulset",
    DaemonSet: "daemonset",
  });

export const CREATABLE_GENERIC_RESOURCES: Readonly<
  Record<string, CreatableGenericResource>
> = Object.freeze({
  configmaps: { templateKey: "configmap", label: "Create ConfigMap" },
  secrets: { templateKey: "secret", label: "Create Secret" },
  jobs: { templateKey: "job", label: "Create Job" },
  cronjobs: { templateKey: "cronjob", label: "Create CronJob" },
  hpa: { templateKey: "hpa", label: "Create HPA" },
  poddisruptionbudgets: {
    templateKey: "poddisruptionbudget",
    label: "Create Pod Disruption Budget",
  },
  serviceaccounts: {
    templateKey: "serviceaccount",
    label: "Create Service Account",
  },
  "k8s-roles": { templateKey: "role", label: "Create Role" },
  "k8s-rolebindings": {
    templateKey: "rolebinding",
    label: "Create Role Binding",
  },
});

export const RESOURCE_TITLES: Readonly<Record<string, string>> = Object.freeze({
  nodes: "Nodes",
  namespaces: "Namespaces",
  events: "Events",
  deployments: "Deployments",
  daemonsets: "DaemonSets",
  statefulsets: "StatefulSets",
  jobs: "Jobs",
  cronjobs: "CronJobs",
  pods: "Pods",
  services: "Services",
  ingresses: "Ingresses",
  networkpolicies: "Network Policies",
  hpa: "Horizontal Pod Autoscalers",
  persistentvolumes: "Persistent Volumes",
  persistentvolumeclaims: "Persistent Volume Claims",
  storageclasses: "Storage Classes",
  configmaps: "ConfigMaps",
  secrets: "Secrets",
  resourcequotas: "Resource Quotas",
  limitranges: "Limit Ranges",
  poddisruptionbudgets: "Pod Disruption Budgets",
  crds: "Custom Resource Definitions",
  serviceaccounts: "Service Accounts",
  "k8s-clusterroles": "ClusterRoles",
  "k8s-clusterrolebindings": "ClusterRoleBindings",
  "k8s-roles": "Roles",
  "k8s-rolebindings": "RoleBindings",
  endpoints: "Endpoints",
  replicasets: "ReplicaSets",
  gateways: "Gateways",
  httproutes: "HTTPRoutes",
  gatewayclasses: "Gateway Classes",
  grpcroutes: "GRPCRoutes",
  tlsroutes: "TLSRoutes",
  tcproutes: "TCPRoutes",
  udproutes: "UDPRoutes",
  referencegrants: "Reference Grants",
});

export const WORKLOAD_KINDS: Readonly<Record<string, string>> = Object.freeze({
  deployments: "Deployment",
  daemonsets: "DaemonSet",
  statefulsets: "StatefulSet",
  jobs: "Job",
  cronjobs: "CronJob",
});

const GENERIC_RESOURCE_TYPES: ReadonlySet<string> = new Set([
  "jobs",
  "cronjobs",
  "configmaps",
  "secrets",
  "hpa",
  "resourcequotas",
  "limitranges",
  "poddisruptionbudgets",
  "crds",
  "serviceaccounts",
  "k8s-clusterroles",
  "k8s-clusterrolebindings",
  "k8s-roles",
  "k8s-rolebindings",
  "endpoints",
  "replicasets",
]);

export function isGenericResourceType(resourceType: string): boolean {
  return GENERIC_RESOURCE_TYPES.has(resourceType);
}
