'use client';

import { useState, useEffect, useMemo } from 'react';
import { usePathname, useRouter } from '@/lib/navigation';
import { Link } from '@/lib/link';
import {
  LayoutDashboard,
  Server,
  BarChart3,
  GitBranch,
  Shield,
  ShieldCheck,
  Settings,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  ChevronDown,
  Orbit,
  HardDrive,
  Network,
  Bell,
  ScrollText,
  FolderKanban,
  Package,
  Archive,
  ArrowLeft,
  Box,
  Database,
  Container,
  Globe,
  Layers,
  FolderOpen,
  Activity,
  Clock,
  FileText,
  Lock,
  Scale,
  Puzzle,
  UserCircle,
  KeyRound,
  Link2,
  Copy,
  Timer,
  ShieldAlert,
  Gauge,
  Wrench,
  ExternalLink,
  BookOpen,
  TerminalSquare,
  Boxes,
  Camera,
  Route,
  Waypoints,
  Rocket,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import { APP_VERSION } from '@/lib/env';
import { ExtensionNavItems } from '@/components/extensions/ExtensionNavItems';
import { useAuthStore, useUIStore } from '@/lib/store';
import { can, isSuperuser, type PermissionVerb } from '@/lib/permissions';
import type { FeatureFlags, FeatureFlagKey } from '@/lib/api';
import {
  useCluster,
  useClusters,
  useClusterNodes,
  useTools,
  useClusterToolsStatus,
  useClusterNamespaces,
  useClusterEvents,
  useClusterPods,
  useWorkloads,
  useServices,
  useIngresses,
  useNetworkPolicies,
  usePersistentVolumes,
  usePersistentVolumeClaims,
  useStorageClasses,
  useGenericResources,
  useFeatureFlags,
} from '@/lib/hooks';

// APP_VERSION is baked at build time via the vite `define` (VERSION env →
// __APP_VERSION__), which the release workflow stamps from the git tag; see
// src/lib/env.ts. Falls back to the current dev version for local/un-stamped
// builds — keep in sync with pkg/version.

type NavItem = {
  label: string;
  href: string;
  icon: typeof Box;
  exact?: boolean;
  countKey?: string;
  permission?: {
    resource: string;
    verb: PermissionVerb | '*';
  };
  superuserOnly?: boolean;
  featureFlag?: FeatureFlagKey;
};

type NavGroup = {
  label: string;
  items: NavItem[];
  defaultOpen?: boolean;
};

// Default (global) navigation groups
const globalNavGroups: NavGroup[] = [
  {
    label: 'Platform',
    defaultOpen: true,
    items: [
      { label: 'Overview', href: '/dashboard', icon: LayoutDashboard, exact: true },
      { label: 'Clusters', href: '/dashboard/clusters', icon: Server, permission: { resource: 'clusters', verb: 'list' } },
      { label: 'Agents', href: '/dashboard/agents', icon: Activity, permission: { resource: 'agents', verb: 'read' } },
      { label: 'Fleet Operations', href: '/dashboard/fleet', icon: Rocket, permission: { resource: 'fleet_operations', verb: 'list' } },
      { label: 'Onboarding Bundles', href: '/dashboard/cluster-templates', icon: Layers, permission: { resource: 'cluster_templates', verb: 'list' } },
    ],
  },
  {
    label: 'Observability',
    items: [
      { label: 'Monitoring', href: '/dashboard/monitoring', icon: BarChart3, permission: { resource: 'monitoring', verb: 'read' }, featureFlag: 'feature.monitoring' },
      { label: 'Alerting', href: '/dashboard/alerting', icon: Bell, permission: { resource: 'alerts', verb: 'read' } },
      { label: 'Logging', href: '/dashboard/logging', icon: ScrollText, permission: { resource: 'logging', verb: 'read' } },
    ],
  },
  {
    label: 'Continuous Delivery',
    items: [
      { label: 'ArgoCD', href: '/dashboard/argocd', icon: GitBranch, permission: { resource: 'argocd', verb: 'read' }, featureFlag: 'feature.argocd' },
      // Git-cluster-sources config lives under settings; surface it alongside
      // ArgoCD so GitOps is a single top-level destination.
      { label: 'Git Sources', href: '/dashboard/settings/gitops', icon: GitBranch, permission: { resource: 'settings', verb: 'read' } },
    ],
  },
  {
    label: 'Integrations',
    items: [
      { label: 'Cluster Tools', href: '/dashboard/tools', icon: Wrench, permission: { resource: 'catalog', verb: 'read' }, featureFlag: 'feature.catalog' },
      { label: 'Extensions', href: '/dashboard/extensions', icon: Puzzle, permission: { resource: 'settings', verb: 'read' } },
    ],
  },
  {
    label: 'Security',
    items: [
      { label: 'Security Policies', href: '/dashboard/security', icon: ShieldCheck, permission: { resource: 'security', verb: 'read' }, featureFlag: 'feature.security' },
    ],
  },
  {
    label: 'Administration',
    items: [
      { label: 'Projects', href: '/dashboard/projects', icon: FolderKanban, permission: { resource: 'projects', verb: 'list' }, featureFlag: 'feature.projects' },
      { label: 'RBAC', href: '/dashboard/rbac', icon: Shield, permission: { resource: 'rbac', verb: 'read' } },
      { label: 'Native RBAC', href: '/dashboard/settings/native-rbac', icon: KeyRound, permission: { resource: 'rbac', verb: 'read' } },
      { label: 'Audit Log', href: '/dashboard/audit', icon: FileText, permission: { resource: 'audit_logs', verb: 'read' } },
      { label: 'Catalog', href: '/dashboard/catalog', icon: Package, permission: { resource: 'catalog', verb: 'read' }, featureFlag: 'feature.catalog' },
      { label: 'Backups', href: '/dashboard/backups', icon: Archive, permission: { resource: 'backups', verb: 'read' }, featureFlag: 'feature.backups' },
      { label: 'Auth', href: '/dashboard/settings/auth', icon: KeyRound, superuserOnly: true },
      // Mark Settings as exact so /dashboard/settings/auth doesn't double-highlight
      // both rows (the active-route matcher otherwise prefix-matches both).
      // UX-07: Settings hub is superuser-only on the backend; align nav so
      // non-superusers with settings:read do not see a dead link.
      { label: 'Settings', href: '/dashboard/settings', icon: Settings, exact: true, superuserOnly: true },
    ],
  },
];

// Cluster-context navigation - Rancher-style resource browser
function getClusterNavGroups(clusterId: string, opts: { isLocal?: boolean } = {}): NavGroup[] {
  const base = `/dashboard/clusters/${clusterId}`;
  // Tabs that need a real outbound tunnel to a remote cluster agent.
  // Hidden for the management plane's own cluster (is_local=true) where
  // the in-cluster local-agent doesn't reliably support these flows.
  // UX-03: attach permission metadata so cluster nav filters like global nav.
  const agentRequiredItems = opts.isLocal
    ? []
    : [
        { label: 'Image Scans', href: `${base}/image-scans`, icon: ShieldAlert, permission: { resource: 'security', verb: 'read' as const } },
        { label: 'Shell', href: `${base}/shell`, icon: TerminalSquare, permission: { resource: 'shell', verb: 'exec' as const } },
        // Control-plane (etcd) DR snapshots. Tunnel + self-managed only; the
        // page itself renders a "not available" state for managed control
        // planes and degrades gracefully when the feature is off server-side.
        { label: 'Control-plane DR', href: `${base}/control-plane-snapshots`, icon: Database, permission: { resource: 'backups', verb: 'read' as const } },
        // Registries (image-pull secrets), Velero workload Snapshots, and the
        // apiserver Network & Access allow-list all drive the member cluster
        // through the outbound tunnel — same agent-required gating as the
        // items above (hidden for the management plane's local agent).
        { label: 'Registries', href: `${base}/registries`, icon: Boxes, permission: { resource: 'clusters', verb: 'read' as const } },
        { label: 'Snapshots', href: `${base}/snapshots`, icon: Camera, permission: { resource: 'backups', verb: 'read' as const } },
        { label: 'Network & Access', href: `${base}/network-access`, icon: Route, permission: { resource: 'security', verb: 'read' as const } },
      ];
  return [
    {
      label: 'Cluster',
      defaultOpen: true,
      items: [
        { label: 'Overview', href: base, icon: LayoutDashboard, exact: true },
        { label: 'Adoption', href: `${base}/adoption`, icon: Activity },
        { label: 'Nodes', href: `${base}/nodes`, icon: Server, countKey: 'nodes' },
        { label: 'Namespaces', href: `${base}/namespaces`, icon: Layers, countKey: 'namespaces' },
        // Event counts on a chatty cluster balloon into the
        // thousands and the literal number isn't actionable — what
        // operators want is "any Warning events recently?". Drop the
        // numeric count for now; a status-dot replacement (green/amber
        // driven by recent Warning count) is the better end state.
        { label: 'Events', href: `${base}/events`, icon: Activity },
        { label: 'Tools', href: `${base}/tools`, icon: Wrench },
        { label: 'Apps', href: `${base}/apps`, icon: Package },
        // Promoted from the overview badge pill to a first-class destination.
        // Reads mesh CRs over the k8s proxy, so it works for local + remote.
        { label: 'Service Mesh', href: `${base}/service-mesh`, icon: Waypoints },
        ...agentRequiredItems,
      ],
    },
    {
      label: 'Workloads',
      items: [
        { label: 'Deployments', href: `${base}/deployments`, icon: Box, countKey: 'deployments' },
        { label: 'DaemonSets', href: `${base}/daemonsets`, icon: Server, countKey: 'daemonsets' },
        { label: 'StatefulSets', href: `${base}/statefulsets`, icon: Database, countKey: 'statefulsets' },
        { label: 'Jobs', href: `${base}/jobs`, icon: Clock, countKey: 'jobs' },
        { label: 'CronJobs', href: `${base}/cronjobs`, icon: Timer, countKey: 'cronjobs' },
        { label: 'Pods', href: `${base}/pods`, icon: Container, countKey: 'pods' },
      ],
    },
    {
      label: 'Service Discovery',
      items: [
        { label: 'Services', href: `${base}/services`, icon: Network, countKey: 'services' },
        { label: 'Ingresses', href: `${base}/ingresses`, icon: Globe, countKey: 'ingresses' },
        { label: 'HPA', href: `${base}/hpa`, icon: Gauge, countKey: 'hpa' },
      ],
    },
    {
      label: 'Gateway API',
      items: [
        { label: 'Gateways', href: `${base}/gateways`, icon: Globe },
        { label: 'HTTPRoutes', href: `${base}/httproutes`, icon: Network },
        { label: 'GatewayClasses', href: `${base}/gatewayclasses`, icon: Layers },
        { label: 'GRPCRoutes', href: `${base}/grpcroutes`, icon: Network },
        { label: 'TLSRoutes', href: `${base}/tlsroutes`, icon: Network },
        { label: 'TCPRoutes', href: `${base}/tcproutes`, icon: Network },
        { label: 'UDPRoutes', href: `${base}/udproutes`, icon: Network },
        { label: 'ReferenceGrants', href: `${base}/referencegrants`, icon: KeyRound },
      ],
    },
    {
      label: 'Storage',
      items: [
        { label: 'PersistentVolumes', href: `${base}/persistentvolumes`, icon: HardDrive, countKey: 'pvs' },
        { label: 'PVCs', href: `${base}/persistentvolumeclaims`, icon: FolderOpen, countKey: 'pvcs' },
        { label: 'StorageClasses', href: `${base}/storageclasses`, icon: Database, countKey: 'storageclasses' },
        { label: 'ConfigMaps', href: `${base}/configmaps`, icon: FileText, countKey: 'configmaps' },
        { label: 'Secrets', href: `${base}/secrets`, icon: Lock, countKey: 'secrets', permission: { resource: 'secrets', verb: 'read' } },
      ],
    },
    {
      label: 'Policy',
      items: [
        { label: 'Network Policies', href: `${base}/networkpolicies`, icon: Shield, countKey: 'networkpolicies' },
        { label: 'Resource Quotas', href: `${base}/resourcequotas`, icon: Scale, countKey: 'resourcequotas' },
        { label: 'Limit Ranges', href: `${base}/limitranges`, icon: ShieldAlert, countKey: 'limitranges' },
        { label: 'PDB', href: `${base}/poddisruptionbudgets`, icon: ShieldCheck, countKey: 'poddisruptionbudgets' },
        // P-04 — Gatekeeper/OPA constraint authoring (bundle + custom).
        { label: 'Gatekeeper', href: `${base}/gatekeeper`, icon: ShieldCheck, permission: { resource: 'security', verb: 'read' } },
      ],
    },
    {
      label: 'RBAC',
      items: [
        { label: 'ServiceAccounts', href: `${base}/serviceaccounts`, icon: UserCircle, countKey: 'serviceaccounts' },
        { label: 'ClusterRoles', href: `${base}/k8s-clusterroles`, icon: KeyRound, countKey: 'k8sClusterroles' },
        { label: 'ClusterRoleBindings', href: `${base}/k8s-clusterrolebindings`, icon: Link2, countKey: 'k8sClusterrolebindings' },
        { label: 'Roles', href: `${base}/k8s-roles`, icon: KeyRound, countKey: 'k8sRoles' },
        { label: 'RoleBindings', href: `${base}/k8s-rolebindings`, icon: Link2, countKey: 'k8sRolebindings' },
      ],
    },
    {
      label: 'More Resources',
      items: [
        // GATE C: dynamic CR explorer (distinct from the static CRD-definition list).
        { label: 'Custom Resources', href: `${base}/custom-resources`, icon: Puzzle, permission: { resource: 'custom_resources', verb: 'read' } },
        { label: 'CRDs', href: `${base}/crds`, icon: Puzzle, countKey: 'crds' },
        { label: 'Endpoints', href: `${base}/endpoints`, icon: Globe, countKey: 'endpoints' },
        { label: 'ReplicaSets', href: `${base}/replicasets`, icon: Copy, countKey: 'replicasets' },
        // Read-only CRD-mirror view (quotas, policies, and other resources the
        // agent mirrors into the management plane).
        { label: 'Mirrored Resources', href: `${base}/resources`, icon: Layers },
      ],
    },
  ];
}

// Hook to fetch resource counts for a cluster.
//
// Counts are only shown inside a nav group when that group is expanded (a
// collapsed group renders no items). Fetching every group's list — including
// every Secret and ConfigMap — on 15-30s intervals for groups the operator
// isn't even looking at is sustained wasted load on the single per-cluster
// agent tunnel + member apiserver. So we lazily enable each group's queries
// only while its nav group is open: the underlying hooks all gate on
// `enabled: !!clusterId`, so passing an empty id for a collapsed group leaves
// its query disabled (and stops its refetch interval) until the operator
// expands it. Cached counts persist across collapses.
function useResourceCounts(clusterId: string, openGroups: Set<string>) {
  // Per-group cluster id: real id when the owning group is open, '' (disabled)
  // otherwise. Group labels mirror getClusterNavGroups().
  const forGroup = (group: string) => (clusterId && openGroups.has(group) ? clusterId : '');
  const clusterCid = forGroup('Cluster');
  const workloadsCid = forGroup('Workloads');
  const svcCid = forGroup('Service Discovery');
  const storageCid = forGroup('Storage');
  const policyCid = forGroup('Policy');
  const rbacCid = forGroup('RBAC');
  const moreCid = forGroup('More Resources');

  const { data: nodes } = useClusterNodes(clusterCid);
  const { data: namespaces } = useClusterNamespaces(clusterCid);
  const { data: events } = useClusterEvents(clusterCid, { limit: 200 });
  const { data: pods } = useClusterPods(workloadsCid);
  const { data: workloads } = useWorkloads(workloadsCid);
  const { data: services } = useServices(svcCid);
  const { data: ingresses } = useIngresses(svcCid);
  const { data: networkPolicies } = useNetworkPolicies(policyCid);
  const { data: pvs } = usePersistentVolumes(storageCid);
  const { data: pvcs } = usePersistentVolumeClaims(storageCid);
  const { data: storageClasses } = useStorageClasses(storageCid);
  // Generic resource counts
  const { data: configmaps } = useGenericResources(storageCid, 'configmaps');
  const { data: secrets } = useGenericResources(storageCid, 'secrets');
  const { data: hpa } = useGenericResources(svcCid, 'hpa');
  const { data: resourcequotas } = useGenericResources(policyCid, 'resourcequotas');
  const { data: limitranges } = useGenericResources(policyCid, 'limitranges');
  const { data: pdbs } = useGenericResources(policyCid, 'poddisruptionbudgets');
  const { data: crds } = useGenericResources(moreCid, 'crds');
  const { data: serviceaccounts } = useGenericResources(rbacCid, 'serviceaccounts');
  const { data: k8sClusterroles } = useGenericResources(rbacCid, 'k8s-clusterroles');
  const { data: k8sClusterrolebindings } = useGenericResources(rbacCid, 'k8s-clusterrolebindings');
  const { data: k8sRoles } = useGenericResources(rbacCid, 'k8s-roles');
  const { data: k8sRolebindings } = useGenericResources(rbacCid, 'k8s-rolebindings');
  const { data: endpoints } = useGenericResources(moreCid, 'endpoints');
  const { data: replicasets } = useGenericResources(moreCid, 'replicasets');

  const allWorkloads = workloads?.data || [];

  return {
    nodes: nodes?.length ?? 0,
    namespaces: namespaces?.length ?? 0,
    events: events?.length ?? 0,
    pods: pods?.length ?? 0,
    deployments: allWorkloads.filter((w) => w.kind === 'Deployment').length,
    daemonsets: allWorkloads.filter((w) => w.kind === 'DaemonSet').length,
    statefulsets: allWorkloads.filter((w) => w.kind === 'StatefulSet').length,
    jobs: allWorkloads.filter((w) => w.kind === 'Job').length,
    cronjobs: allWorkloads.filter((w) => w.kind === 'CronJob').length,
    services: services?.length ?? 0,
    ingresses: ingresses?.length ?? 0,
    hpa: hpa?.length ?? 0,
    networkpolicies: networkPolicies?.length ?? 0,
    pvs: pvs?.length ?? 0,
    pvcs: pvcs?.length ?? 0,
    storageclasses: storageClasses?.length ?? 0,
    configmaps: configmaps?.length ?? 0,
    secrets: secrets?.length ?? 0,
    resourcequotas: resourcequotas?.length ?? 0,
    limitranges: limitranges?.length ?? 0,
    poddisruptionbudgets: pdbs?.length ?? 0,
    crds: crds?.length ?? 0,
    serviceaccounts: serviceaccounts?.length ?? 0,
    k8sClusterroles: k8sClusterroles?.length ?? 0,
    k8sClusterrolebindings: k8sClusterrolebindings?.length ?? 0,
    k8sRoles: k8sRoles?.length ?? 0,
    k8sRolebindings: k8sRolebindings?.length ?? 0,
    endpoints: endpoints?.length ?? 0,
    replicasets: replicasets?.length ?? 0,
  };
}

// Persistent cluster switcher — hop from cluster A to cluster B while staying on
// the same sub-route (swap the id in the path, preserve the trailing segment) so
// you don't have to round-trip through "All Clusters". Only mounts inside a
// cluster context, so the clusters list isn't fetched on global pages.
function ClusterSwitcher({ clusterId, fallbackName }: { clusterId: string; fallbackName: string }) {
  const router = useRouter();
  const pathname = usePathname();
  const { data: clusters } = useClusters();
  const list = clusters?.data ?? [];
  // Everything after /dashboard/clusters/<id> (e.g. "/nodes", "" for overview).
  const subRoute = pathname.slice(`/dashboard/clusters/${clusterId}`.length);
  const knowsCurrent = list.some((c) => c.id === clusterId);

  return (
    <select
      value={clusterId}
      onChange={(e) => {
        const nextId = e.target.value;
        if (nextId !== clusterId) router.push(`/dashboard/clusters/${nextId}${subRoute}`);
      }}
      className="w-full h-7 px-2 rounded-md border border-sidebar-border bg-transparent
        text-sm font-medium text-foreground focus:outline-none focus:ring-1 focus:ring-ring
        hover:bg-accent/50 transition-colors cursor-pointer"
      title="Switch cluster"
      aria-label="Switch cluster"
    >
      {/* Keep the current cluster selectable even before the list resolves. */}
      {!knowsCurrent && <option value={clusterId}>{fallbackName}</option>}
      {list.map((c) => (
        <option key={c.id} value={c.id}>
          {c.displayName || c.name}
        </option>
      ))}
    </select>
  );
}

// Tool UI links for installed tools with web interfaces
function InstalledToolLinks({ clusterId, collapsed }: { clusterId: string; collapsed: boolean }) {
  const { data: tools } = useTools();
  const { data: statuses } = useClusterToolsStatus(clusterId);
  const [isOpen, setIsOpen] = useState(true);

  if (!tools || !statuses) return null;

  const statusMap = new Map<string, (typeof statuses)[number]>();
  statuses.forEach((s) => statusMap.set(s.slug, s));

  // Build list of available UI links
  const uiLinks: Array<{ name: string; url: string }> = [];

  tools.forEach((tool) => {
    const status = statusMap.get(tool.slug);
    if (!status || status.status !== 'installed') return;
    const ns = status.namespace || tool.default_namespace;
    if (!ns) return;

    // Main service UI
    if (tool.service_name && tool.service_port) {
      uiLinks.push({
        name: tool.name,
        url: `/api/v1/clusters/${clusterId}/proxy/service/${ns}/${tool.service_name}:${tool.service_port}${tool.service_path || '/'}`,
      });
    }

    // Sub-services (Prometheus, Alertmanager, Kiali, etc.)
    tool.sub_services?.forEach((sub) => {
      uiLinks.push({
        name: sub.name,
        url: `/api/v1/clusters/${clusterId}/proxy/service/${ns}/${sub.service}:${sub.port}/`,
      });
    });
  });

  if (uiLinks.length === 0) return null;

  if (collapsed) {
    return (
      <div className="space-y-0.5">
        {uiLinks.map((link) => (
          <a
            key={link.url}
            href={link.url}
            target="_blank"
            rel="noopener noreferrer"
            className="nav-item group justify-center px-0"
            title={`${link.name} (opens in new tab)`}
          >
            <ExternalLink className="h-4 w-4 text-muted-foreground group-hover:text-foreground" />
          </a>
        ))}
      </div>
    );
  }

  return (
    <div>
      <button
        onClick={() => setIsOpen(!isOpen)}
        className="w-full flex items-center justify-between px-3 py-2 text-sm font-semibold text-muted-foreground hover:text-foreground transition-colors"
      >
        <span>Tool UIs</span>
        {isOpen ? (
          <ChevronUp className="h-3.5 w-3.5" />
        ) : (
          <ChevronDown className="h-3.5 w-3.5" />
        )}
      </button>
      {isOpen && (
        <div className="space-y-px">
          {uiLinks.map((link) => (
            <a
              key={link.url}
              href={link.url}
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center gap-2 px-3 py-1.5 mx-1 rounded-md text-sm text-muted-foreground hover:text-foreground hover:bg-accent/50 transition-colors"
            >
              <ExternalLink className="h-3.5 w-3.5 flex-shrink-0" />
              <span className="truncate flex-1">{link.name}</span>
            </a>
          ))}
        </div>
      )}
    </div>
  );
}

// Collapsible nav group - Rancher style
function SidebarGroup({
  group,
  pathname,
  collapsed,
  counts,
  isOpen,
  onToggle,
}: {
  group: NavGroup;
  pathname: string;
  collapsed: boolean;
  counts?: Record<string, number>;
  isClusterContext: boolean;
  isOpen: boolean;
  onToggle: () => void;
}) {

  if (collapsed) {
    return (
      <div className="space-y-0.5">
        {group.items.map((item) => {
          const Icon = item.icon;
          const active = item.exact ? pathname === item.href : pathname.startsWith(item.href);
          return (
            <Link
              key={item.href}
              href={item.href}
              className={cn('nav-item group justify-center px-0', active && 'active')}
              title={item.label}
            >
              <Icon className={cn('h-4 w-4 flex-shrink-0', active ? 'text-foreground' : 'text-muted-foreground group-hover:text-foreground')} />
            </Link>
          );
        })}
      </div>
    );
  }

  return (
    <div>
      {/* Group header with chevron on the right (Rancher style) */}
      <button
        onClick={onToggle}
        className="w-full flex items-center justify-between px-3 py-2 text-sm font-semibold text-muted-foreground hover:text-foreground transition-colors"
      >
        <span>{group.label}</span>
        {isOpen ? (
          <ChevronUp className="h-3.5 w-3.5" />
        ) : (
          <ChevronDown className="h-3.5 w-3.5" />
        )}
      </button>
      {isOpen && (
        <div className="space-y-px">
          {group.items.map((item) => {
            const Icon = item.icon;
            const active = item.exact
              ? pathname === item.href
              : pathname.startsWith(item.href);
            const count = item.countKey && counts ? counts[item.countKey] : undefined;
            return (
              <Link
                key={item.href}
                href={item.href}
                className={cn(
                  'flex items-center gap-2 px-3 py-1.5 mx-1 rounded-md text-sm transition-colors',
                  active
                    ? 'bg-accent text-foreground font-medium'
                    : 'text-muted-foreground hover:text-foreground hover:bg-accent/50'
                )}
              >
                <Icon className={cn('h-4 w-4 flex-shrink-0', active ? 'text-foreground' : 'text-muted-foreground')} />
                <span className="truncate flex-1">{item.label}</span>
                {count !== undefined && (
                  <span className={cn(
                    'text-xs tabular-nums ml-auto',
                    active ? 'text-foreground/70' : 'text-muted-foreground/60'
                  )}>
                    {count}
                  </span>
                )}
              </Link>
            );
          })}
        </div>
      )}
    </div>
  );
}

export function Sidebar() {
  const pathname = usePathname();
  const { sidebarCollapsed, toggleSidebarCollapsed } = useUIStore();
  const user = useAuthStore((s) => s.user);
  const { data: featureFlags } = useFeatureFlags();

  // Detect cluster context from URL. Static sub-routes (new, register) are NOT
  // cluster ids — treating them as such fires cluster-detail queries with a
  // bogus id (e.g. /clusters/register/resources/... -> 400/503).
  const clusterMatch = pathname.match(/^\/dashboard\/clusters\/([^/]+)/);
  const clusterSegment = clusterMatch?.[1];
  const clusterId =
    clusterSegment && clusterSegment !== 'new' && clusterSegment !== 'register'
      ? clusterSegment
      : undefined;
  const isClusterContext = !!clusterId;

  // Fetch cluster name for header
  const { data: cluster } = useCluster(clusterId || '');

  const navGroups = useMemo(
    () => filterNavGroups(
      isClusterContext
        ? getClusterNavGroups(clusterId!, { isLocal: cluster?.isLocal })
        : globalNavGroups,
      user,
      featureFlags,
    ),
    [cluster?.isLocal, clusterId, featureFlags, isClusterContext, user],
  );

  // Multiple groups may stay open at once (the cluster nav has 7 groups; a
  // single-open accordion collapses your context every time you expand another).
  // Seed from each group's `defaultOpen` flag.
  const [openGroups, setOpenGroups] = useState<Set<string>>(
    () => new Set(navGroups.filter((g) => g.defaultOpen).map((g) => g.label)),
  );

  // Fetch resource counts when in cluster context — only for groups that are
  // currently expanded (see useResourceCounts).
  const counts = useResourceCounts(isClusterContext ? clusterId! : '', openGroups);

  // Keep `defaultOpen` groups open and auto-expand the group containing the
  // active route (e.g. after a context switch) without collapsing the rest.
  useEffect(() => {
    setOpenGroups((prev) => {
      const next = new Set(prev);
      let changed = false;
      for (const g of navGroups) {
        if (g.defaultOpen && !next.has(g.label)) {
          next.add(g.label);
          changed = true;
        }
      }
      const activeGroup = navGroups.find((g) =>
        g.items.some((item) =>
          item.exact ? pathname === item.href : pathname.startsWith(item.href)
        )
      );
      if (activeGroup && !next.has(activeGroup.label)) {
        next.add(activeGroup.label);
        changed = true;
      }
      return changed ? next : prev;
    });
  }, [navGroups, pathname]);

  return (
    <aside
      className={cn(
        'flex flex-col h-screen bg-sidebar border-r border-sidebar-border transition-all duration-200 ease-in-out',
        sidebarCollapsed ? 'w-16' : 'w-60'
      )}
    >
      {/* Logo + collapse toggle */}
      <div className="flex items-center h-14 px-4 border-b border-sidebar-border">
        {!sidebarCollapsed && (
          <Link href="/dashboard" className="flex items-center gap-2.5 min-w-0">
            <div className="flex-shrink-0 w-7 h-7 rounded-lg bg-gradient-to-br from-blue-500 to-violet-600 flex items-center justify-center">
              <Orbit className="h-4 w-4 text-white" />
            </div>
            <div className="flex flex-col min-w-0">
              <span className="text-sm font-semibold text-foreground tracking-tight truncate leading-tight">
                Astronomer
              </span>
              <span className="text-[10px] text-muted-foreground leading-tight">
                by AlphaBravo
              </span>
            </div>
          </Link>
        )}
        <button
          onClick={toggleSidebarCollapsed}
          className={cn('nav-item', sidebarCollapsed ? 'w-full justify-center px-0' : 'ml-auto')}
          title={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          aria-label={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
        >
          {sidebarCollapsed ? (
            <ChevronRight className="h-4 w-4" />
          ) : (
            <ChevronLeft className="h-4 w-4" />
          )}
        </button>
      </div>

      {/* Cluster context header */}
      {isClusterContext && !sidebarCollapsed && (
        <div className="px-2 py-2 border-b border-sidebar-border">
          <Link
            href="/dashboard/clusters"
            className="flex items-center gap-2 px-2 py-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors rounded-md hover:bg-accent/50"
          >
            <ArrowLeft className="h-3.5 w-3.5" />
            <span>All Clusters</span>
          </Link>
          <div className="px-2 mt-1">
            <ClusterSwitcher
              clusterId={clusterId!}
              fallbackName={cluster?.displayName || cluster?.name || 'Cluster'}
            />
            {cluster?.kubernetesVersion && (
              <p className="text-2xs text-muted-foreground mt-1 px-1">v{cluster.kubernetesVersion}</p>
            )}
          </div>
        </div>
      )}
      {isClusterContext && sidebarCollapsed && (
        <div className="px-2 py-2 border-b border-sidebar-border">
          <Link
            href="/dashboard/clusters"
            className="nav-item group justify-center px-0"
            title="Back to Clusters"
          >
            <ArrowLeft className="h-4 w-4 text-muted-foreground group-hover:text-foreground" />
          </Link>
        </div>
      )}

      {/* Navigation */}
      <nav className="flex-1 overflow-y-auto py-2 px-1 no-scrollbar">
        {navGroups.map((group) => (
          <SidebarGroup
            key={group.label}
            group={group}
            pathname={pathname}
            collapsed={sidebarCollapsed}
            counts={isClusterContext ? counts : undefined}
            isClusterContext={isClusterContext}
            isOpen={openGroups.has(group.label)}
            onToggle={() =>
              setOpenGroups((prev) => {
                const next = new Set(prev);
                if (next.has(group.label)) next.delete(group.label);
                else next.add(group.label);
                return next;
              })
            }
          />
        ))}
        {isClusterContext && (
          <InstalledToolLinks clusterId={clusterId!} collapsed={sidebarCollapsed} />
        )}
        {/* §HostMounts mount point 1 — enabled `sidebar` extensions append
            full-page nav links here (global context only; routes are
            host-fixed under /dashboard/extensions/{name}). The component
            renders nothing (header included) when no extension declares a
            sidebar point, so the nav is unchanged on a fresh install. */}
        {!isClusterContext && (
          <ExtensionNavItems pathname={pathname} collapsed={sidebarCollapsed} />
        )}
      </nav>

      {/* Bottom links */}
      <div className="mt-auto px-2 py-2 border-t border-sidebar-border space-y-1">
        <a
          href="/astronomer-docs/"
          target="_blank"
          rel="noopener noreferrer"
          className="nav-item w-full"
          title="Documentation"
        >
          <BookOpen className="h-4 w-4" />
          {!sidebarCollapsed && <span className="text-xs">Documentation</span>}
        </a>
        {!sidebarCollapsed && (
          <div className="px-3 py-1 space-y-0.5">
            <p className="text-[10px] text-muted-foreground">Astronomer {APP_VERSION}</p>
            <p className="text-[10px] text-muted-foreground">
              Built by{' '}
              <a
                href="https://alphabravo.io"
                target="_blank"
                rel="noopener noreferrer"
                className="hover:text-foreground underline-offset-2 hover:underline"
              >
                AlphaBravo
              </a>
            </p>
          </div>
        )}
      </div>

    </aside>
  );
}

function filterNavGroups(
  groups: NavGroup[],
  user: ReturnType<typeof useAuthStore.getState>['user'],
  featureFlags?: FeatureFlags
): NavGroup[] {
  return groups
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => {
        if (item.featureFlag && featureFlags?.[item.featureFlag] === false) return false;
        if (item.superuserOnly) return isSuperuser(user);
        if (!item.permission) return true;
        return can(user, item.permission.resource, item.permission.verb);
      }),
    }))
    .filter((group) => group.items.length > 0);
}
