import {
  Activity,
  BarChart3,
  Bell,
  Blocks,
  Box,
  Boxes,
  Camera,
  Clock,
  Container,
  Copy,
  Database,
  FileText,
  FolderOpen,
  Gauge,
  Gavel,
  Globe,
  HardDrive,
  History,
  Key,
  KeyRound,
  Layers,
  LayoutDashboard,
  LayoutGrid,
  Link,
  Link2,
  LineChart,
  Lock,
  Network,
  Package,
  Puzzle,
  Rocket,
  Route,
  Scale,
  ScrollText,
  Server,
  Shield,
  ShieldAlert,
  ShieldCheck,
  TerminalSquare,
  Timer,
  UserCircle,
  Waypoints,
  Wrench,
  Compass,
  Settings2,
  SlidersHorizontal,
} from "lucide-react";

import type { NavGroup } from "@/components/layout/sidebar-navigation";
import {
  gatewayNavItems,
  withClusterResourceTypes,
} from "./cluster-resource-types";

/** The group that receives discovered CRD subgroups. */
export const CLUSTER_MANAGE_GROUP = "Manage";

/**
 * Cluster panel: six groups (A1). Overview · Workloads · Networking ·
 * Storage · Policy & access · Manage. No counts here — the cluster overview
 * page shows the inventory counts in group headers.
 */
export function getClusterNavGroups(
  clusterId: string,
  opts: {
    isLocal?: boolean;
    veleroInstalled?: boolean;
    grafanaAvailable?: boolean;
  } = {},
): NavGroup[] {
  const base = `/dashboard/clusters/${clusterId}`;
  // Items that need a real outbound tunnel to a remote cluster agent. Hidden
  // for the management plane's own cluster (is_local=true).
  const agentRequiredItems = opts.isLocal
    ? []
    : [
        {
          label: "Image scans",
          href: `${base}/image-scans`,
          icon: ShieldAlert,
          permission: { resource: "security", verb: "read" as const },
        },
        {
          label: "Shell",
          href: `${base}/shell`,
          icon: TerminalSquare,
          permission: { resource: "shell", verb: "exec" as const },
        },
        {
          label: "Control-plane DR",
          href: `${base}/control-plane-snapshots`,
          icon: Database,
          permission: { resource: "backups", verb: "read" as const },
        },
        {
          label: "Registries",
          href: `${base}/registries`,
          icon: Boxes,
          permission: { resource: "clusters", verb: "read" as const },
        },
        ...(opts.veleroInstalled
          ? [
              {
                label: "Snapshots",
                href: `${base}/snapshots`,
                icon: Camera,
                permission: { resource: "backups", verb: "read" as const },
              },
            ]
          : []),
        {
          label: "Network & access",
          href: `${base}/network-access`,
          icon: Route,
          permission: { resource: "security", verb: "read" as const },
        },
      ];
  const gateway = gatewayNavItems(base);
  return withClusterResourceTypes([
    {
      label: "Overview",
      icon: LayoutDashboard,
      href: base,
      defaultOpen: true,
      items: [
        { label: "Overview", href: base, icon: LayoutDashboard, exact: true },
        {
          label: "Nodes",
          href: `${base}/nodes`,
          icon: Server,
          countKey: "nodes",
        },
        {
          label: "Namespaces",
          href: `${base}/namespaces`,
          icon: Layers,
          countKey: "namespaces",
        },
        { label: "Events", href: `${base}/events`, icon: History },
        {
          label: "Metrics",
          href: `${base}/metrics`,
          icon: Gauge,
          permission: { resource: "monitoring", verb: "read" as const },
          featureFlag: "feature.monitoring",
        },
        {
          label: "Alerting",
          href: `${base}/alerting`,
          icon: Bell,
          permission: { resource: "alerts", verb: "read" as const },
        },
        {
          label: "Logging",
          href: `${base}/logging`,
          icon: ScrollText,
          permission: { resource: "logging", verb: "read" as const },
        },
      ],
    },
    {
      label: "Workloads",
      icon: Box,
      href: `${base}/workloads`,
      items: [
        {
          label: "Overview",
          href: `${base}/workloads`,
          icon: LayoutGrid,
          exact: true,
        },
        {
          label: "Deployments",
          href: `${base}/deployments`,
          icon: Box,
          countKey: "deployments",
        },
        {
          label: "DaemonSets",
          href: `${base}/daemonsets`,
          icon: Boxes,
          countKey: "daemonsets",
        },
        {
          label: "StatefulSets",
          href: `${base}/statefulsets`,
          icon: Database,
          countKey: "statefulsets",
        },
        { label: "Jobs", href: `${base}/jobs`, icon: Clock, countKey: "jobs" },
        {
          label: "CronJobs",
          href: `${base}/cronjobs`,
          icon: Timer,
          countKey: "cronjobs",
        },
        {
          label: "Pods",
          href: `${base}/pods`,
          icon: Container,
          countKey: "pods",
        },
        {
          label: "Autoscalers",
          href: `${base}/hpa`,
          icon: Gauge,
          countKey: "hpa",
        },
      ],
    },
    {
      label: "Networking",
      icon: Network,
      href: `${base}/services`,
      items: [
        {
          label: "Services",
          href: `${base}/services`,
          icon: Network,
          countKey: "services",
        },
        {
          label: "Ingresses",
          href: `${base}/ingresses`,
          icon: Compass,
          countKey: "ingresses",
        },
        ...gateway,
        {
          label: "Network policies",
          href: `${base}/network-policies`,
          icon: Shield,
          countKey: "networkpolicies",
        },
      ],
    },
    {
      label: "Storage",
      icon: HardDrive,
      href: `${base}/persistentvolumes`,
      items: [
        {
          label: "Persistent volumes",
          href: `${base}/persistentvolumes`,
          icon: HardDrive,
          countKey: "pvs",
        },
        {
          label: "Volume claims",
          href: `${base}/persistentvolumeclaims`,
          icon: FolderOpen,
          countKey: "pvcs",
        },
        {
          label: "Storage classes",
          href: `${base}/storageclasses`,
          icon: Database,
          countKey: "storageclasses",
        },
        {
          label: "ConfigMaps",
          href: `${base}/configmaps`,
          icon: FileText,
          countKey: "configmaps",
        },
        {
          label: "Secrets",
          href: `${base}/secrets`,
          icon: Lock,
          countKey: "secrets",
          permission: { resource: "secrets", verb: "read" },
        },
      ],
    },
    {
      label: "Policy & access",
      icon: Scale,
      href: `${base}/resourcequotas`,
      items: [
        {
          label: "Resource quotas",
          href: `${base}/resourcequotas`,
          icon: Scale,
          countKey: "resourcequotas",
        },
        {
          label: "Limit ranges",
          href: `${base}/limitranges`,
          icon: SlidersHorizontal,
          countKey: "limitranges",
        },
        {
          label: "Disruption budgets",
          href: `${base}/poddisruptionbudgets`,
          icon: ShieldCheck,
          countKey: "poddisruptionbudgets",
        },
        {
          label: "Gatekeeper",
          ifHaveGroup: "constraints.gatekeeper.sh",
          href: `${base}/gatekeeper`,
          icon: Gavel,
          permission: { resource: "security", verb: "read" },
        },
        {
          label: "Service accounts",
          href: `${base}/serviceaccounts`,
          icon: UserCircle,
          countKey: "serviceaccounts",
        },
        {
          label: "Roles",
          href: `${base}/k8s-roles`,
          icon: Key,
          countKey: "k8sRoles",
        },
        {
          label: "Role bindings",
          href: `${base}/k8s-rolebindings`,
          icon: Link,
          countKey: "k8sRolebindings",
        },
        {
          label: "Cluster roles",
          href: `${base}/k8s-clusterroles`,
          icon: KeyRound,
          countKey: "k8sClusterroles",
        },
        {
          label: "Cluster role bindings",
          href: `${base}/k8s-clusterrolebindings`,
          icon: Link2,
          countKey: "k8sClusterrolebindings",
        },
      ],
    },
    {
      label: CLUSTER_MANAGE_GROUP,
      icon: Settings2,
      href: `${base}/tools`,
      items: [
        { label: "Tools", href: `${base}/tools`, icon: Wrench },
        { label: "Applications", href: `${base}/apps`, icon: Package },
        {
          label: "Delivery",
          href: `${base}/delivery`,
          icon: Rocket,
          permission: { resource: "delivery_inventory", verb: "read" as const },
        },
        {
          label: "Monitoring stack",
          href: `${base}/monitoring-stack`,
          icon: BarChart3,
          permission: { resource: "monitoring", verb: "read" as const },
          featureFlag: "feature.monitoring",
        },
        ...(opts.grafanaAvailable
          ? [
              {
                label: "Grafana",
                href: `${base}/grafana`,
                icon: LineChart,
                permission: {
                  resource: "monitoring",
                  verb: "read" as const,
                },
                featureFlag: "feature.monitoring" as const,
              },
            ]
          : []),
        { label: "Adoption", href: `${base}/adoption`, icon: Activity },
        {
          label: "Onboarding template",
          href: `${base}/template`,
          icon: Settings2,
          permission: { resource: "clusters", verb: "read" },
        },
        {
          label: "Service mesh",
          href: `${base}/service-mesh`,
          icon: Waypoints,
        },
        ...agentRequiredItems,
        {
          label: "Custom resources",
          href: `${base}/custom-resources`,
          icon: Puzzle,
          permission: { resource: "custom_resources", verb: "read" },
          exact: true,
        },
        {
          label: "CRDs",
          href: `${base}/crds`,
          icon: Blocks,
          countKey: "crds",
        },
        {
          label: "Endpoints",
          href: `${base}/endpoints`,
          icon: Globe,
          countKey: "endpoints",
        },
        {
          label: "ReplicaSets",
          href: `${base}/replicasets`,
          icon: Copy,
          countKey: "replicasets",
        },
        {
          label: "Mirrored resources",
          href: `${base}/resources`,
          icon: Layers,
        },
      ],
    },
  ]);
}
