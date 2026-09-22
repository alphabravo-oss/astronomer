import {
  Cable,
  Globe,
  KeyRound,
  Layers,
  Lock,
  Radio,
  Route,
  Waypoints,
} from "lucide-react";
import type { NavGroup, NavItem } from "./sidebar-navigation";

// Explicit identities avoid collisions between core and CRD kinds with the
// same plural, and account for route slugs that differ from Kubernetes names.
const resourceTypes: Record<string, string> = {
  nodes: "core/nodes",
  namespaces: "core/namespaces",
  events: "core/events",
  pods: "core/pods",
  services: "core/services",
  endpoints: "core/endpoints",
  configmaps: "core/configmaps",
  secrets: "core/secrets",
  persistentvolumes: "core/persistentvolumes",
  persistentvolumeclaims: "core/persistentvolumeclaims",
  serviceaccounts: "core/serviceaccounts",
  resourcequotas: "core/resourcequotas",
  limitranges: "core/limitranges",
  deployments: "apps/deployments",
  daemonsets: "apps/daemonsets",
  statefulsets: "apps/statefulsets",
  replicasets: "apps/replicasets",
  jobs: "batch/jobs",
  cronjobs: "batch/cronjobs",
  hpa: "autoscaling/horizontalpodautoscalers",
  ingresses: "networking.k8s.io/ingresses",
  "network-policies": "networking.k8s.io/networkpolicies",
  storageclasses: "storage.k8s.io/storageclasses",
  poddisruptionbudgets: "policy/poddisruptionbudgets",
  "k8s-roles": "rbac.authorization.k8s.io/roles",
  "k8s-rolebindings": "rbac.authorization.k8s.io/rolebindings",
  "k8s-clusterroles": "rbac.authorization.k8s.io/clusterroles",
  "k8s-clusterrolebindings": "rbac.authorization.k8s.io/clusterrolebindings",
  crds: "apiextensions.k8s.io/customresourcedefinitions",
};

export function withClusterResourceTypes(groups: NavGroup[]): NavGroup[] {
  const identify = (item: NavItem): NavItem => ({
    ...item,
    resourceType:
      item.resourceType ?? resourceTypes[item.href.split("/").at(-1) ?? ""],
  });
  return groups.map((group) => ({
    ...group,
    items: group.items.map(identify),
    subgroups: group.subgroups?.map((subgroup) => ({
      ...subgroup,
      items: subgroup.items.map(identify),
    })),
  }));
}

export function gatewayNavItems(base: string): NavItem[] {
  const gatewayGroup = "gateway.networking.k8s.io";
  return [
    { label: "Gateways", plural: "gateways", kind: "Gateway", icon: Globe },
    {
      label: "HTTPRoutes",
      plural: "httproutes",
      kind: "HTTPRoute",
      icon: Route,
    },
    {
      label: "GatewayClasses",
      plural: "gatewayclasses",
      kind: "GatewayClass",
      icon: Layers,
    },
    {
      label: "GRPCRoutes",
      plural: "grpcroutes",
      kind: "GRPCRoute",
      icon: Waypoints,
    },
    { label: "TLSRoutes", plural: "tlsroutes", kind: "TLSRoute", icon: Lock },
    { label: "TCPRoutes", plural: "tcproutes", kind: "TCPRoute", icon: Cable },
    { label: "UDPRoutes", plural: "udproutes", kind: "UDPRoute", icon: Radio },
    {
      label: "ReferenceGrants",
      plural: "referencegrants",
      kind: "ReferenceGrant",
      icon: KeyRound,
    },
  ].map(({ plural, kind, ...item }) => ({
    ...item,
    href: `${base}/${plural}`,
    resourceType: `${gatewayGroup}/${plural}`,
    ifHaveGroup: gatewayGroup,
    ifHaveKind: `${gatewayGroup}/${kind}`,
  }));
}
