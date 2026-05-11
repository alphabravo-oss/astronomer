'use client';

import { usePathname, useRouter } from 'next/navigation';
import { useState, useRef, useEffect, useMemo } from 'react';
import { useTheme } from 'next-themes';
import {
  Bell,
  ChevronDown,
  ChevronRight,
  LogOut,
  Settings,
  User,
  Command,
  Sun,
  Moon,
  Monitor,
  AlertTriangle,
  AlertCircle,
  Info,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import { useUIStore, useAuthStore } from '@/lib/store';
import { useClusters, useAlertEvents } from '@/lib/hooks';
import { StatusBadge } from '@/components/ui/status-badge';
import { formatRelativeTime } from '@/lib/utils';
import { GlobalSearch } from '@/components/layout/global-search';

// --- Breadcrumb generation ---

const routeLabels: Record<string, string> = {
  dashboard: 'Dashboard',
  clusters: 'Clusters',
  workloads: 'Workloads',
  monitoring: 'Monitoring',
  alerting: 'Alerting',
  logging: 'Logging',
  storage: 'Storage',
  networking: 'Networking',
  argocd: 'ArgoCD',
  rbac: 'RBAC',
  projects: 'Projects',
  settings: 'Settings',
  register: 'Register',
  // Resource types
  pods: 'Pods',
  deployments: 'Deployments',
  daemonsets: 'DaemonSets',
  statefulsets: 'StatefulSets',
  jobs: 'Jobs',
  cronjobs: 'CronJobs',
  services: 'Services',
  ingresses: 'Ingresses',
  configmaps: 'ConfigMaps',
  secrets: 'Secrets',
  hpa: 'HPA',
  'network-policies': 'Network Policies',
  'persistent-volumes': 'Persistent Volumes',
  'persistent-volume-claims': 'PVCs',
  'storage-classes': 'Storage Classes',
  resourcequotas: 'Resource Quotas',
  limitranges: 'Limit Ranges',
  poddisruptionbudgets: 'PDBs',
  crds: 'CRDs',
  serviceaccounts: 'Service Accounts',
  'k8s-clusterroles': 'Cluster Roles',
  'k8s-clusterrolebindings': 'Cluster Role Bindings',
  'k8s-roles': 'Roles',
  'k8s-rolebindings': 'Role Bindings',
  endpoints: 'Endpoints',
  replicasets: 'ReplicaSets',
  namespaces: 'Namespaces',
  nodes: 'Nodes',
  events: 'Events',
};

function generateBreadcrumbs(pathname: string, clusterMap?: Record<string, string>) {
  const segments = pathname.split('/').filter(Boolean);
  const crumbs: { label: string; href: string }[] = [];
  let path = '';

  for (let i = 0; i < segments.length; i++) {
    const segment = segments[i];
    path += `/${segment}`;

    let label: string;
    if (segments[i - 1] === 'clusters' && clusterMap?.[segment]) {
      label = clusterMap[segment];
    } else {
      label = routeLabels[segment] || decodeURIComponent(segment);
    }

    crumbs.push({ label, href: path });
  }

  return crumbs;
}

const severityIcon: Record<string, React.ElementType> = {
  critical: AlertCircle,
  warning: AlertTriangle,
  info: Info,
};

const severityColor: Record<string, string> = {
  critical: 'text-status-error',
  warning: 'text-status-warning',
  info: 'text-status-info',
};

export function Topbar() {
  const pathname = usePathname();
  const router = useRouter();
  const { setCommandPaletteOpen } = useUIStore();
  const { user, logout } = useAuthStore();
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const [notificationOpen, setNotificationOpen] = useState(false);
  const userRef = useRef<HTMLDivElement>(null);
  const notificationRef = useRef<HTMLDivElement>(null);
  // Clusters are still fetched here so breadcrumbs can resolve the
  // /dashboard/clusters/{id}/... slug into the human-readable cluster name.
  const { data: clustersData } = useClusters({ pageSize: 50 });
  const { data: alertEvents } = useAlertEvents({ status: 'firing' });

  const { theme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);

  const clusterMap = useMemo(() => {
    // Breadcrumbs use the technical RFC-1123 cluster name rather than the
    // pretty displayName — it matches the URL slug, the kubeconfig context
    // name, and the kubectl prompt the user is likely to compare against.
    const map: Record<string, string> = {};
    for (const c of clustersData?.data || []) {
      map[c.id] = c.name || c.displayName;
    }
    return map;
  }, [clustersData?.data]);

  const breadcrumbs = generateBreadcrumbs(pathname, clusterMap);

  const firingAlerts = alertEvents?.filter((e) => e.status === 'firing') || [];
  const recentAlerts = (alertEvents || []).slice(0, 5);

  useEffect(() => {
    setMounted(true);
  }, []);

  // Close dropdowns on outside click
  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (userRef.current && !userRef.current.contains(e.target as Node)) {
        setUserMenuOpen(false);
      }
      if (notificationRef.current && !notificationRef.current.contains(e.target as Node)) {
        setNotificationOpen(false);
      }
    }
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, []);

  const cycleTheme = () => {
    if (theme === 'light') setTheme('dark');
    else if (theme === 'dark') setTheme('system');
    else setTheme('light');
  };

  const ThemeIcon = !mounted ? Monitor : theme === 'dark' ? Moon : theme === 'light' ? Sun : Monitor;

  return (
    <header className="sticky top-0 z-30 flex items-center justify-between h-14 px-6 border-b border-border bg-background/80 backdrop-blur-lg">
      {/* Left: Breadcrumbs */}
      <nav className="flex items-center gap-1.5 text-sm min-w-0">
        {breadcrumbs.map((crumb, i) => (
          <div key={crumb.href} className="flex items-center gap-1.5 min-w-0">
            {i > 0 && <ChevronRight className="h-3.5 w-3.5 text-muted-foreground flex-shrink-0" />}
            {i === breadcrumbs.length - 1 ? (
              <span className="text-foreground font-medium truncate">{crumb.label}</span>
            ) : (
              <button
                onClick={() => router.push(crumb.href)}
                className="text-muted-foreground hover:text-foreground transition-colors truncate"
              >
                {crumb.label}
              </button>
            )}
          </div>
        ))}
      </nav>

      {/* Center: Cross-cluster Global Search (Phase A3) */}
      <div className="hidden md:flex flex-1 justify-center px-6">
        <GlobalSearch />
      </div>

      {/* Right: Actions */}
      <div className="flex items-center gap-2">
        {/* Command Palette Trigger */}
        <button
          onClick={() => setCommandPaletteOpen(true)}
          className="inline-flex items-center gap-1.5 h-8 px-2.5 rounded-md border border-border text-xs
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
        >
          <Command className="h-3.5 w-3.5" />
          <kbd className="font-mono text-[10px]">K</kbd>
        </button>

        {/* Theme Toggle */}
        <button
          onClick={cycleTheme}
          className="relative inline-flex items-center justify-center h-8 w-8 rounded-md
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          title={`Theme: ${theme || 'system'}`}
        >
          <ThemeIcon className="h-4 w-4" />
        </button>

        {/* Notifications */}
        <div ref={notificationRef} className="relative">
          <button
            onClick={() => setNotificationOpen(!notificationOpen)}
            className="relative inline-flex items-center justify-center h-8 w-8 rounded-md
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            <Bell className="h-4 w-4" />
            {firingAlerts.length > 0 && (
              <span className="absolute top-0.5 right-0.5 flex items-center justify-center h-4 min-w-[16px] px-1 rounded-full bg-status-error text-[10px] font-bold text-white">
                {firingAlerts.length > 99 ? '99+' : firingAlerts.length}
              </span>
            )}
          </button>

          {notificationOpen && (
            <div className="absolute right-0 top-full mt-1 w-80 rounded-lg border border-border bg-popover shadow-xl z-50 overflow-hidden">
              <div className="flex items-center justify-between px-4 py-3 border-b border-border">
                <h4 className="text-sm font-medium text-foreground">Recent Alerts</h4>
                {firingAlerts.length > 0 && (
                  <span className="text-xs px-2 py-0.5 rounded-full bg-status-error/10 text-status-error font-medium">
                    {firingAlerts.length} firing
                  </span>
                )}
              </div>

              <div className="max-h-80 overflow-y-auto">
                {recentAlerts.length === 0 ? (
                  <div className="px-4 py-8 text-center text-sm text-muted-foreground">
                    No recent alerts
                  </div>
                ) : (
                  recentAlerts.map((alert) => {
                    const SevIcon = severityIcon[alert.severity] || Info;
                    return (
                      <div
                        key={alert.id}
                        className="flex items-start gap-3 px-4 py-3 border-b border-border last:border-0 hover:bg-accent/50 transition-colors"
                      >
                        <SevIcon className={cn('h-4 w-4 flex-shrink-0 mt-0.5', severityColor[alert.severity] || 'text-muted-foreground')} />
                        <div className="flex-1 min-w-0">
                          <p className="text-sm text-foreground font-medium truncate">{alert.ruleName}</p>
                          <p className="text-xs text-muted-foreground truncate mt-0.5">{alert.message}</p>
                          <div className="flex items-center gap-2 mt-1">
                            <StatusBadge status={alert.status} size="sm" />
                            <span className="text-2xs text-muted-foreground">{formatRelativeTime(alert.firedAt)}</span>
                          </div>
                        </div>
                      </div>
                    );
                  })
                )}
              </div>

              <div className="px-4 py-2 border-t border-border">
                <button
                  onClick={() => {
                    router.push('/dashboard/alerting');
                    setNotificationOpen(false);
                  }}
                  className="w-full text-center text-xs text-muted-foreground hover:text-foreground transition-colors py-1"
                >
                  View all alerts
                </button>
              </div>
            </div>
          )}
        </div>

        {/* User Menu */}
        <div ref={userRef} className="relative">
          <button
            onClick={() => setUserMenuOpen(!userMenuOpen)}
            className="flex items-center gap-2 h-8 pl-1 pr-2 rounded-md hover:bg-accent transition-colors"
          >
            <div className="w-6 h-6 rounded-full bg-gradient-to-br from-zinc-600 to-zinc-800 flex items-center justify-center">
              <User className="h-3 w-3 text-zinc-300" />
            </div>
            <ChevronDown className="h-3 w-3 text-muted-foreground" />
          </button>

          {userMenuOpen && (
            <div className="absolute right-0 top-full mt-1 w-56 rounded-lg border border-border bg-popover shadow-xl z-50 overflow-hidden">
              <div className="px-3 py-2.5 border-b border-border">
                <p className="text-sm font-medium text-foreground">
                  {user?.displayName || user?.username}
                </p>
                <p className="text-xs text-muted-foreground">{user?.email}</p>
              </div>
              <div className="p-1">
                <button
                  onClick={() => {
                    router.push('/dashboard/settings');
                    setUserMenuOpen(false);
                  }}
                  className="w-full flex items-center gap-2.5 px-3 py-2 rounded-md text-sm
                    text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
                >
                  <Settings className="h-4 w-4" />
                  Settings
                </button>
                <button
                  onClick={() => {
                    logout();
                    router.push('/auth/login');
                  }}
                  className="w-full flex items-center gap-2.5 px-3 py-2 rounded-md text-sm
                    text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
                >
                  <LogOut className="h-4 w-4" />
                  Sign out
                </button>
              </div>
            </div>
          )}
        </div>
      </div>
    </header>
  );
}
