import {
  Activity,
  BarChart3,
  Bell,
  Blocks,
  Box,
  Boxes,
  Cable,
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
  Link,
  Link2,
  LineChart,
  Lock,
  Network,
  Package,
  Puzzle,
  Radio,
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
} from "lucide-react";

import type { NavGroup } from "@/components/layout/sidebar-navigation";

// Cluster-context navigation - Rancher-style resource browser
export function getClusterNavGroups(
  clusterId: string,
  opts: {
    isLocal?: boolean;
    veleroInstalled?: boolean;
    grafanaAvailable?: boolean;
  } = {},
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
      icon: LayoutDashboard,
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
        { label: "Events", href: `${base}/events`, icon: History },
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
      icon: Gauge,
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
      items: [
        {
          label: "Overview",
          href: `${base}/workloads`,
          icon: LayoutDashboard,
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
      icon: Network,
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
      icon: Globe,
      items: [
        { label: "Gateways", href: `${base}/gateways`, icon: Globe },
        {
          label: "GatewayClasses",
          href: `${base}/gatewayclasses`,
          icon: Layers,
        },
        { label: "HTTPRoutes", href: `${base}/httproutes`, icon: Route },
        { label: "GRPCRoutes", href: `${base}/grpcroutes`, icon: Waypoints },
        { label: "TLSRoutes", href: `${base}/tlsroutes`, icon: Lock },
        { label: "TCPRoutes", href: `${base}/tcproutes`, icon: Cable },
        { label: "UDPRoutes", href: `${base}/udproutes`, icon: Radio },
        {
          label: "ReferenceGrants",
          href: `${base}/referencegrants`,
          icon: KeyRound,
        },
      ],
    },
    {
      label: "Storage",
      icon: HardDrive,
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
      icon: Shield,
      items: [
        {
          label: "Network Policies",
          href: `${base}/network-policies`,
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
          icon: Gavel,
          permission: { resource: "security", verb: "read" },
        },
      ],
    },
    {
      label: "RBAC",
      icon: UserCircle,
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
          icon: Key,
          countKey: "k8sRoles",
        },
        {
          label: "RoleBindings",
          href: `${base}/k8s-rolebindings`,
          icon: Link,
          countKey: "k8sRolebindings",
        },
      ],
    },
    {
      label: "More Resources",
      icon: Puzzle,
      items: [
        // GATE C: dynamic CR explorer (distinct from the static CRD-definition list).
        {
          label: "Custom Resources",
          href: `${base}/custom-resources`,
          icon: Puzzle,
          permission: { resource: "custom_resources", verb: "read" },
        },
        { label: "CRDs", href: `${base}/crds`, icon: Blocks, countKey: "crds" },
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
