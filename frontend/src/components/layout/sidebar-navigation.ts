import {
  Activity,
  BarChart3,
  Bell,
  Box,
  Boxes,
  Camera,
  Clock,
  Container,
  Copy,
  Database,
  FileText,
  FolderKanban,
  FolderOpen,
  Gauge,
  Globe,
  HardDrive,
  KeyRound,
  Layers,
  LayoutDashboard,
  Link2,
  Lock,
  Network,
  Package,
  Puzzle,
  Rocket,
  Route,
  Scale,
  ScrollText,
  Server,
  Settings,
  Shield,
  ShieldAlert,
  ShieldCheck,
  Sparkles,
  Star,
  TerminalSquare,
  Timer,
  UserCircle,
  Waypoints,
  Wrench,
} from "lucide-react";

import { can, isSuperuser, type PermissionVerb } from "@/lib/permissions";
import type { FeatureFlags, FeatureFlagKey } from "@/lib/api/feature-flags";
import type { User } from "@/types";

export type NavItem = {
  label: string;
  href: string;
  icon: typeof Box;
  exact?: boolean;
  countKey?: string;
  permission?: {
    resource: string;
    verb: PermissionVerb | "*";
  };
  superuserOnly?: boolean;
  featureFlag?: FeatureFlagKey;
  // Opt-in flags stay hidden until the flags payload explicitly enables them
  // (missing/loading counts as off). Default-on flags still use === false so
  // they remain visible while the flags query hydrates.
  optIn?: boolean;
  requiresCharlieActivated?: boolean;
};

export type NavGroup = {
  label: string;
  items: NavItem[];
  defaultOpen?: boolean;
};

// Default (global) navigation groups
export const globalNavGroups: NavGroup[] = [
  {
    label: "Platform",
    defaultOpen: true,
    items: [
      {
        label: "Overview",
        href: "/dashboard",
        icon: LayoutDashboard,
        exact: true,
      },
      {
        label: "Charlie",
        href: "/dashboard/charlie",
        icon: Sparkles,
        permission: { resource: "charlie", verb: "read" },
        featureFlag: "feature.charlie",
        requiresCharlieActivated: true,
      },
      {
        label: "Clusters",
        href: "/dashboard/clusters",
        icon: Server,
        permission: { resource: "clusters", verb: "list" },
      },
      {
        label: "Workloads",
        href: "/dashboard/workloads",
        icon: Box,
        permission: { resource: "clusters", verb: "list" },
      },
      {
        label: "Cluster Agents",
        href: "/dashboard/agents",
        icon: Activity,
        permission: { resource: "cluster_agents", verb: "read" },
      },
      {
        label: "Onboarding Bundles",
        href: "/dashboard/cluster-templates",
        icon: Layers,
        permission: { resource: "cluster_templates", verb: "list" },
      },
    ],
  },
  {
    label: "Observability",
    items: [
      {
        label: "Shared metrics",
        href: "/dashboard/monitoring",
        icon: BarChart3,
        permission: { resource: "monitoring", verb: "read" },
        featureFlag: "feature.monitoring",
      },
      // Shared Thanos / Alertmanager lifecycle. It lives under the settings URL
      // because the API does (/settings/monitoring/...), but it is surfaced
      // here rather than only on the settings hub: the hub is superuser-only,
      // while these endpoints authorize on monitoring:read/update, so a
      // monitoring admin who is not a superuser would otherwise never find it.
      {
        label: "Shared stacks",
        href: "/dashboard/settings/monitoring",
        icon: Layers,
        permission: { resource: "monitoring", verb: "read" },
        featureFlag: "feature.monitoring",
      },
      {
        label: "Alerting",
        href: "/dashboard/alerting",
        icon: Bell,
        permission: { resource: "alerts", verb: "read" },
      },
      {
        label: "Logging",
        href: "/dashboard/logging",
        icon: ScrollText,
        permission: { resource: "logging", verb: "read" },
      },
    ],
  },
  {
    label: "Continuous Delivery",
    items: [
      {
        label: "Estate",
        href: "/dashboard/delivery",
        icon: Rocket,
        permission: { resource: "delivery_targets", verb: "list" },
        exact: true,
      },
    ],
  },
  {
    label: "Integrations",
    items: [
      {
        label: "Cluster Tools",
        href: "/dashboard/tools",
        icon: Wrench,
        permission: { resource: "catalog", verb: "read" },
        featureFlag: "feature.catalog",
      },
      // Helm marketplace lives on the cluster (Apps), matching Rancher Apps.
      // Estate-wide repos stay reachable from Apps → Repositories.
      {
        label: "Extensions",
        href: "/dashboard/extensions",
        icon: Puzzle,
        permission: { resource: "settings", verb: "read" },
        featureFlag: "feature.extensions",
        optIn: true,
      },
    ],
  },
  {
    label: "Security",
    items: [
      {
        label: "Security Policies",
        href: "/dashboard/security",
        icon: ShieldCheck,
        permission: { resource: "security", verb: "read" },
        featureFlag: "feature.security",
      },
    ],
  },
  {
    label: "Administration",
    items: [
      {
        label: "Projects",
        href: "/dashboard/projects",
        icon: FolderKanban,
        permission: { resource: "projects", verb: "list" },
        featureFlag: "feature.projects",
      },
      {
        label: "RBAC",
        href: "/dashboard/rbac",
        icon: Shield,
        permission: { resource: "rbac", verb: "read" },
      },
      {
        label: "Audit Log",
        href: "/dashboard/audit",
        icon: FileText,
        permission: { resource: "audit_logs", verb: "read" },
      },
      // Superuser-only hub. Prefix-match so Dex/SSO under /settings/auth
      // highlights Settings instead of a sibling Auth row.
      {
        label: "Settings",
        href: "/dashboard/settings",
        icon: Settings,
        superuserOnly: true,
      },
    ],
  },
];

export function withFavoriteNavigation(
  groups: NavGroup[],
  favorites: readonly string[],
): NavGroup[] {
  if (favorites.length === 0) return groups;
  const byHref = new Map(
    groups.flatMap((group) => group.items).map((item) => [item.href, item]),
  );
  const items = favorites.flatMap((href) => {
    const item = byHref.get(href);
    return item ? [{ ...item, icon: Star }] : [];
  });
  return items.length > 0
    ? [{ label: "Favorites", items, defaultOpen: true }, ...groups]
    : groups;
}

// Cluster-context navigation - Rancher-style resource browser
export function getClusterNavGroups(
  clusterId: string,
  opts: { isLocal?: boolean; veleroInstalled?: boolean } = {},
): NavGroup[] {
  const base = `/dashboard/clusters/${clusterId}`;
  // Tabs that need a real outbound tunnel to a remote cluster agent.
  // Hidden for the management plane's own cluster (is_local=true) where
  // the in-cluster local-agent doesn't reliably support these flows.
  // UX-03: attach permission metadata so cluster nav filters like global nav.
  const agentRequiredItems = opts.isLocal
    ? []
    : [
        {
          label: "Image Scans",
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
        // Control-plane (etcd) DR snapshots. Tunnel + self-managed only; the
        // page itself renders a "not available" state for managed control
        // planes and degrades gracefully when the feature is off server-side.
        {
          label: "Control-plane DR",
          href: `${base}/control-plane-snapshots`,
          icon: Database,
          permission: { resource: "backups", verb: "read" as const },
        },
        // Registries (image-pull secrets) and the apiserver Network & Access
        // allow-list drive the member cluster through the outbound tunnel.
        {
          label: "Registries",
          href: `${base}/registries`,
          icon: Boxes,
          permission: { resource: "clusters", verb: "read" as const },
        },
        // Velero workload snapshots: only listed when Velero is actually
        // installed on this cluster. The snapshots route still renders an
        // empty-state install CTA if someone hits the URL directly.
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
          label: "Network & Access",
          href: `${base}/network-access`,
          icon: Route,
          permission: { resource: "security", verb: "read" as const },
        },
      ];
  return [
    {
      label: "Cluster",
      defaultOpen: true,
      items: [
        { label: "Overview", href: base, icon: LayoutDashboard, exact: true },
        { label: "Adoption", href: `${base}/adoption`, icon: Activity },
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
        // Event counts on a chatty cluster balloon into the
        // thousands and the literal number isn't actionable — what
        // operators want is "any Warning events recently?". Drop the
        // numeric count for now; a status-dot replacement (green/amber
        // driven by recent Warning count) is the better end state.
        { label: "Events", href: `${base}/events`, icon: Activity },
        { label: "Tools", href: `${base}/tools`, icon: Wrench },
        { label: "Apps", href: `${base}/apps`, icon: Package },
        {
          label: "Delivery",
          href: `${base}/delivery`,
          icon: Rocket,
          permission: { resource: "delivery_inventory", verb: "read" as const },
        },
        // Promoted from the overview badge pill to a first-class destination.
        // Reads mesh CRs over the k8s proxy, so it works for local + remote.
        {
          label: "Service Mesh",
          href: `${base}/service-mesh`,
          icon: Waypoints,
        },
        ...agentRequiredItems,
      ],
    },
    {
      label: "Observability",
      defaultOpen: true,
      items: [
        {
          label: "Metrics",
          href: `${base}/metrics`,
          icon: Gauge,
          permission: { resource: "monitoring", verb: "read" as const },
          featureFlag: "feature.monitoring",
        },
        // Lifecycle for this cluster's kube-prometheus-stack (install /
        // upgrade / replace / uninstall). Gated on monitoring:read, which is
        // what the status + preview routes require; the mutating controls on
        // the page gate themselves on create/update/delete.
        {
          label: "Monitoring Stack",
          href: `${base}/monitoring-stack`,
          icon: BarChart3,
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
      items: [
        {
          label: "Deployments",
          href: `${base}/deployments`,
          icon: Box,
          countKey: "deployments",
        },
        {
          label: "DaemonSets",
          href: `${base}/daemonsets`,
          icon: Server,
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
      ],
    },
    {
      label: "Service Discovery",
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
          icon: Globe,
          countKey: "ingresses",
        },
        { label: "HPA", href: `${base}/hpa`, icon: Gauge, countKey: "hpa" },
      ],
    },
    {
      label: "Gateway API",
      items: [
        { label: "Gateways", href: `${base}/gateways`, icon: Globe },
        { label: "HTTPRoutes", href: `${base}/httproutes`, icon: Network },
        {
          label: "GatewayClasses",
          href: `${base}/gatewayclasses`,
          icon: Layers,
        },
        { label: "GRPCRoutes", href: `${base}/grpcroutes`, icon: Network },
        { label: "TLSRoutes", href: `${base}/tlsroutes`, icon: Network },
        { label: "TCPRoutes", href: `${base}/tcproutes`, icon: Network },
        { label: "UDPRoutes", href: `${base}/udproutes`, icon: Network },
        {
          label: "ReferenceGrants",
          href: `${base}/referencegrants`,
          icon: KeyRound,
        },
      ],
    },
    {
      label: "Storage",
      items: [
        {
          label: "PersistentVolumes",
          href: `${base}/persistentvolumes`,
          icon: HardDrive,
          countKey: "pvs",
        },
        {
          label: "PVCs",
          href: `${base}/persistentvolumeclaims`,
          icon: FolderOpen,
          countKey: "pvcs",
        },
        {
          label: "StorageClasses",
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
      label: "Policy",
      items: [
        {
          label: "Network Policies",
          href: `${base}/networkpolicies`,
          icon: Shield,
          countKey: "networkpolicies",
        },
        {
          label: "Resource Quotas",
          href: `${base}/resourcequotas`,
          icon: Scale,
          countKey: "resourcequotas",
        },
        {
          label: "Limit Ranges",
          href: `${base}/limitranges`,
          icon: ShieldAlert,
          countKey: "limitranges",
        },
        {
          label: "PDB",
          href: `${base}/poddisruptionbudgets`,
          icon: ShieldCheck,
          countKey: "poddisruptionbudgets",
        },
        // P-04 — Gatekeeper/OPA constraint authoring (bundle + custom).
        {
          label: "Gatekeeper",
          href: `${base}/gatekeeper`,
          icon: ShieldCheck,
          permission: { resource: "security", verb: "read" },
        },
      ],
    },
    {
      label: "RBAC",
      items: [
        {
          label: "ServiceAccounts",
          href: `${base}/serviceaccounts`,
          icon: UserCircle,
          countKey: "serviceaccounts",
        },
        {
          label: "ClusterRoles",
          href: `${base}/k8s-clusterroles`,
          icon: KeyRound,
          countKey: "k8sClusterroles",
        },
        {
          label: "ClusterRoleBindings",
          href: `${base}/k8s-clusterrolebindings`,
          icon: Link2,
          countKey: "k8sClusterrolebindings",
        },
        {
          label: "Roles",
          href: `${base}/k8s-roles`,
          icon: KeyRound,
          countKey: "k8sRoles",
        },
        {
          label: "RoleBindings",
          href: `${base}/k8s-rolebindings`,
          icon: Link2,
          countKey: "k8sRolebindings",
        },
      ],
    },
    {
      label: "More Resources",
      items: [
        // GATE C: dynamic CR explorer (distinct from the static CRD-definition list).
        {
          label: "Custom Resources",
          href: `${base}/custom-resources`,
          icon: Puzzle,
          permission: { resource: "custom_resources", verb: "read" },
        },
        { label: "CRDs", href: `${base}/crds`, icon: Puzzle, countKey: "crds" },
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
        // Read-only CRD-mirror view (quotas, policies, and other resources the
        // agent mirrors into the management plane).
        {
          label: "Mirrored Resources",
          href: `${base}/resources`,
          icon: Layers,
        },
      ],
    },
  ];
}

export function filterNavGroups(
  groups: NavGroup[],
  user: User | null,
  featureFlags?: FeatureFlags,
  charlieActivated = false,
): NavGroup[] {
  return groups
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => {
        if (item.featureFlag) {
          if (item.optIn) {
            if (featureFlags?.[item.featureFlag] !== true) return false;
          } else if (featureFlags?.[item.featureFlag] === false) {
            return false;
          }
        }
        if (item.requiresCharlieActivated && !charlieActivated) return false;
        if (item.superuserOnly) return isSuperuser(user);
        if (!item.permission) return true;
        return can(user, item.permission.resource, item.permission.verb);
      }),
    }))
    .filter((group) => group.items.length > 0);
}
