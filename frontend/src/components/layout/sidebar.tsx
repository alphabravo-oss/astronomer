'use client';

import { useState, useEffect } from 'react';
import { usePathname } from 'next/navigation';
import Link from 'next/link';
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
} from 'lucide-react';
import { cn } from '@/lib/utils';
import { useUIStore } from '@/lib/store';
import {
  useCluster,
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
} from '@/lib/hooks';

type NavItem = {
  label: string;
  href: string;
  icon: typeof Box;
  exact?: boolean;
  countKey?: string;
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
      { label: 'Clusters', href: '/dashboard/clusters', icon: Server },
      // Top-level cluster templates page lives next to Clusters since both
      // are concerned with the cluster lifecycle. Read-gating is enforced
      // inside the page itself; we keep the link unconditionally visible
      // so the URL remains stable while RBAC is still being rolled out.
      { label: 'Cluster Templates', href: '/dashboard/cluster-templates', icon: Layers },
    ],
  },
  {
    label: 'Observability',
    items: [
      { label: 'Monitoring', href: '/dashboard/monitoring', icon: BarChart3 },
      { label: 'Alerting', href: '/dashboard/alerting', icon: Bell },
      { label: 'Logging', href: '/dashboard/logging', icon: ScrollText },
    ],
  },
  {
    label: 'Integrations',
    items: [
      { label: 'Cluster Tools', href: '/dashboard/tools', icon: Wrench },
      { label: 'ArgoCD', href: '/dashboard/argocd', icon: GitBranch },
    ],
  },
  {
    label: 'Security',
    items: [
      { label: 'Security Policies', href: '/dashboard/security', icon: ShieldCheck },
    ],
  },
  {
    label: 'Administration',
    items: [
      { label: 'Projects', href: '/dashboard/projects', icon: FolderKanban },
      { label: 'RBAC', href: '/dashboard/rbac', icon: Shield },
      { label: 'Catalog', href: '/dashboard/catalog', icon: Package },
      { label: 'Backups', href: '/dashboard/backups', icon: Archive },
      { label: 'Auth', href: '/dashboard/settings/auth', icon: KeyRound },
      // Mark Settings as exact so /dashboard/settings/auth doesn't double-highlight
      // both rows (the active-route matcher otherwise prefix-matches both).
      { label: 'Settings', href: '/dashboard/settings', icon: Settings, exact: true },
    ],
  },
];

// Cluster-context navigation - Rancher-style resource browser
function getClusterNavGroups(clusterId: string): NavGroup[] {
  const base = `/dashboard/clusters/${clusterId}`;
  return [
    {
      label: 'Cluster',
      defaultOpen: true,
      items: [
        { label: 'Overview', href: base, icon: LayoutDashboard, exact: true },
        { label: 'Nodes', href: `${base}/nodes`, icon: Server, countKey: 'nodes' },
        { label: 'Namespaces', href: `${base}/namespaces`, icon: Layers, countKey: 'namespaces' },
        { label: 'Events', href: `${base}/events`, icon: Activity, countKey: 'events' },
        { label: 'Tools', href: `${base}/tools`, icon: Wrench },
        // Sprint 17 / migration 065: in-browser kubectl shell. The
        // backend gates the routes on clusters:update; the link is
        // always rendered (the page itself shows a polite 403/disabled
        // state when the feature is off or the operator lacks the
        // permission).
        { label: 'Shell', href: `${base}/shell`, icon: TerminalSquare },
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
        { label: 'Secrets', href: `${base}/secrets`, icon: Lock, countKey: 'secrets' },
      ],
    },
    {
      label: 'Policy',
      items: [
        { label: 'Network Policies', href: `${base}/networkpolicies`, icon: Shield, countKey: 'networkpolicies' },
        { label: 'Resource Quotas', href: `${base}/resourcequotas`, icon: Scale, countKey: 'resourcequotas' },
        { label: 'Limit Ranges', href: `${base}/limitranges`, icon: ShieldAlert, countKey: 'limitranges' },
        { label: 'PDB', href: `${base}/poddisruptionbudgets`, icon: ShieldCheck, countKey: 'poddisruptionbudgets' },
      ],
    },
    {
      label: 'More Resources',
      items: [
        { label: 'CRDs', href: `${base}/crds`, icon: Puzzle, countKey: 'crds' },
        { label: 'ServiceAccounts', href: `${base}/serviceaccounts`, icon: UserCircle, countKey: 'serviceaccounts' },
        { label: 'ClusterRoles', href: `${base}/k8s-clusterroles`, icon: KeyRound, countKey: 'k8sClusterroles' },
        { label: 'ClusterRoleBindings', href: `${base}/k8s-clusterrolebindings`, icon: Link2, countKey: 'k8sClusterrolebindings' },
        { label: 'Roles', href: `${base}/k8s-roles`, icon: KeyRound, countKey: 'k8sRoles' },
        { label: 'RoleBindings', href: `${base}/k8s-rolebindings`, icon: Link2, countKey: 'k8sRolebindings' },
        { label: 'Endpoints', href: `${base}/endpoints`, icon: Globe, countKey: 'endpoints' },
        { label: 'ReplicaSets', href: `${base}/replicasets`, icon: Copy, countKey: 'replicasets' },
      ],
    },
  ];
}

// Hook to fetch resource counts for a cluster
function useResourceCounts(clusterId: string) {
  const { data: nodes } = useClusterNodes(clusterId);
  const { data: namespaces } = useClusterNamespaces(clusterId);
  const { data: events } = useClusterEvents(clusterId, { limit: 200 });
  const { data: pods } = useClusterPods(clusterId);
  const { data: workloads } = useWorkloads(clusterId);
  const { data: services } = useServices(clusterId);
  const { data: ingresses } = useIngresses(clusterId);
  const { data: networkPolicies } = useNetworkPolicies(clusterId);
  const { data: pvs } = usePersistentVolumes(clusterId);
  const { data: pvcs } = usePersistentVolumeClaims(clusterId);
  const { data: storageClasses } = useStorageClasses(clusterId);
  // Generic resource counts
  const { data: configmaps } = useGenericResources(clusterId, 'configmaps');
  const { data: secrets } = useGenericResources(clusterId, 'secrets');
  const { data: hpa } = useGenericResources(clusterId, 'hpa');
  const { data: resourcequotas } = useGenericResources(clusterId, 'resourcequotas');
  const { data: limitranges } = useGenericResources(clusterId, 'limitranges');
  const { data: pdbs } = useGenericResources(clusterId, 'poddisruptionbudgets');
  const { data: crds } = useGenericResources(clusterId, 'crds');
  const { data: serviceaccounts } = useGenericResources(clusterId, 'serviceaccounts');
  const { data: k8sClusterroles } = useGenericResources(clusterId, 'k8s-clusterroles');
  const { data: k8sClusterrolebindings } = useGenericResources(clusterId, 'k8s-clusterrolebindings');
  const { data: k8sRoles } = useGenericResources(clusterId, 'k8s-roles');
  const { data: k8sRolebindings } = useGenericResources(clusterId, 'k8s-rolebindings');
  const { data: endpoints } = useGenericResources(clusterId, 'endpoints');
  const { data: replicasets } = useGenericResources(clusterId, 'replicasets');

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
  isClusterContext,
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

  // Detect cluster context from URL
  const clusterMatch = pathname.match(/^\/dashboard\/clusters\/([^/]+)/);
  const clusterId = clusterMatch?.[1];
  const isClusterContext = !!clusterId && clusterId !== 'new';

  // Fetch cluster name for header
  const { data: cluster } = useCluster(clusterId || '');

  // Fetch resource counts when in cluster context
  const counts = useResourceCounts(isClusterContext ? clusterId! : '');

  const navGroups = isClusterContext
    ? getClusterNavGroups(clusterId!)
    : globalNavGroups;

  // Accordion state: only one group open at a time
  const [openGroup, setOpenGroup] = useState<string | null>('Cluster');

  // Auto-expand the group containing the active route
  useEffect(() => {
    const activeGroup = navGroups.find(g =>
      g.items.some(item =>
        item.exact ? pathname === item.href : pathname.startsWith(item.href)
      )
    );
    if (activeGroup) setOpenGroup(activeGroup.label);
  }, [pathname]);

  return (
    <aside
      className={cn(
        'flex flex-col h-screen bg-sidebar border-r border-sidebar-border transition-all duration-200 ease-in-out',
        sidebarCollapsed ? 'w-16' : 'w-60'
      )}
    >
      {/* Logo */}
      <div className="flex items-center h-14 px-4 border-b border-sidebar-border">
        <Link href="/dashboard" className="flex items-center gap-2.5 min-w-0">
          <div className="flex-shrink-0 w-7 h-7 rounded-lg bg-gradient-to-br from-blue-500 to-violet-600 flex items-center justify-center">
            <Orbit className="h-4 w-4 text-white" />
          </div>
          {!sidebarCollapsed && (
            <div className="flex flex-col min-w-0">
              <span className="text-sm font-semibold text-foreground tracking-tight truncate leading-tight">
                Astronomer
              </span>
              <span className="text-[10px] text-muted-foreground leading-tight">
                by AlphaBravo
              </span>
            </div>
          )}
        </Link>
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
            <p className="text-sm font-medium text-foreground truncate">
              {cluster?.displayName || cluster?.name || 'Cluster'}
            </p>
            {cluster?.kubernetesVersion && (
              <p className="text-2xs text-muted-foreground">v{cluster.kubernetesVersion}</p>
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
            isOpen={openGroup === group.label}
            onToggle={() => setOpenGroup(openGroup === group.label ? null : group.label)}
          />
        ))}
        {isClusterContext && (
          <InstalledToolLinks clusterId={clusterId!} collapsed={sidebarCollapsed} />
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
          <div className="px-3 py-1">
            <span className="text-[10px] text-muted-foreground">Astronomer v0.1.0</span>
          </div>
        )}
        <button
          onClick={toggleSidebarCollapsed}
          className="nav-item w-full justify-center"
          title={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
        >
          {sidebarCollapsed ? (
            <ChevronRight className="h-4 w-4" />
          ) : (
            <>
              <ChevronLeft className="h-4 w-4" />
              <span className="text-xs">Collapse</span>
            </>
          )}
        </button>
      </div>

    </aside>
  );
}
