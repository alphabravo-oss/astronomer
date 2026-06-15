'use client';

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { useClusters, useActivityFeed, useAlertEvents, useTools, queryKeys } from '@/lib/hooks';
import { useLatestBackupDrill } from '@/components/settings/hooks';
import { useLiveQueryInvalidation } from '@/lib/live-events';
import { StatusBadge } from '@/components/ui/status-badge';
import { formatRelativeTime, cn } from '@/lib/utils';
import { WidgetGrid } from '@/components/dashboards/widget-grid';
import { renderGlobal } from '@/lib/api/dashboards';
import {
  Server,
  Activity,
  AlertTriangle,
  WifiOff,
  Loader2,
  ArrowRight,
  PackagePlus,
  TerminalSquare,
  Bell,
  Boxes,
  ShieldCheck,
  Package,
  Layers,
} from 'lucide-react';
import { Link } from '@/lib/link';

export default function DashboardPage() {
  const { data: clustersData, isLoading: clustersLoading } = useClusters({ pageSize: 100 });
  const { data: activityData, isLoading: activityLoading } = useActivityFeed(10);
  const { data: alertEventsData } = useAlertEvents({ status: 'firing' });
  const { data: toolsData } = useTools();
  // T7.3 — backup-drill health row. The CronJob writes one row
  // per drill run; useLatestBackupDrill returns the most recent.
  const { data: latestDrill } = useLatestBackupDrill();

  useLiveQueryInvalidation(
    [
      'cluster.connected',
      'cluster.disconnected',
      'cluster.created',
      'cluster.updated',
      'cluster.deleted',
      'cluster.status_changed',
      'agent.reconnecting',
      'agent.failed',
    ],
    [queryKeys.clusters.all, queryKeys.activity()],
  );

  const clusters = clustersData?.data || [];
  const activity = activityData || [];
  const alertEvents = (alertEventsData as { data?: Array<{ severity?: string }> } | undefined)?.data || [];
  const tools = toolsData || [];

  const activeClusters = clusters.filter((c) => c.status === 'active').length;
  const warningClusters = clusters.filter((c) => c.status === 'warning').length;
  const errorClusters = clusters.filter((c) => c.status === 'error' || c.status === 'disconnected').length;
  const totalNodes = clusters.reduce((acc, c) => acc + c.nodeCount, 0);
  const totalPods = clusters.reduce((acc, c) => acc + c.podCount, 0);
  const criticalAlerts = alertEvents.filter((e) => e.severity === 'critical' || e.severity === 'error').length;
  const warningAlerts = alertEvents.filter((e) => e.severity === 'warning').length;
  const totalTools = Array.isArray(tools) ? tools.length : 0;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight">
          Platform Overview
        </h1>
        <p className="text-sm text-muted-foreground mt-1">
          Real-time status of your Kubernetes infrastructure
        </p>
      </div>

      {/* At-a-glance metric strip */}
      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3">
        <MetricTile
          href="/dashboard/clusters"
          label="Clusters"
          value={clusters.length}
          sublabel={`${activeClusters} active`}
          icon={<Server className="h-4 w-4" />}
          tone="default"
        />
        <MetricTile
          href="/dashboard/clusters?status=warning"
          label="Warnings"
          value={warningClusters}
          sublabel="needs attention"
          icon={<AlertTriangle className="h-4 w-4" />}
          tone={warningClusters > 0 ? 'warning' : 'default'}
        />
        <MetricTile
          href="/dashboard/clusters?status=disconnected"
          label="Disconnected"
          value={errorClusters}
          sublabel="agent offline"
          icon={<WifiOff className="h-4 w-4" />}
          tone={errorClusters > 0 ? 'error' : 'default'}
        />
        <MetricTile
          href="/dashboard/alerting"
          label="Open Alerts"
          value={alertEvents.length}
          sublabel={
            criticalAlerts > 0 ? `${criticalAlerts} critical`
            : warningAlerts > 0 ? `${warningAlerts} warning`
            : 'all clear'
          }
          icon={<Bell className="h-4 w-4" />}
          tone={criticalAlerts > 0 ? 'error' : warningAlerts > 0 ? 'warning' : 'default'}
        />
        <MetricTile
          href="/dashboard/clusters"
          label="Pods"
          value={totalPods.toLocaleString()}
          sublabel={`across ${totalNodes} nodes`}
          icon={<Boxes className="h-4 w-4" />}
          tone="default"
        />
      </div>

      {/* Quick actions */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <ActionCard
          href="/dashboard/clusters/register"
          icon={<Server className="h-4 w-4" />}
          title="Register cluster"
          description="Generate an install command for a new cluster"
        />
        <ActionCard
          href="/dashboard/catalog"
          icon={<PackagePlus className="h-4 w-4" />}
          title="Browse catalog"
          description="Install Helm charts across your fleet"
        />
        <ActionCard
          href="/dashboard/alerting"
          icon={<Bell className="h-4 w-4" />}
          title="Review alerts"
          description="Acknowledge firing alerts and tune rules"
        />
        <ActionCard
          href="/dashboard/projects"
          icon={<Layers className="h-4 w-4" />}
          title="Manage projects"
          description="Quotas, members, and project-scoped resources"
        />
      </div>

      {/* Clusters table */}
      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-medium text-foreground">Clusters</h2>
          <Link
            href="/dashboard/clusters"
            className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground transition-colors"
          >
            View all
            <ArrowRight className="h-3.5 w-3.5" />
          </Link>
        </div>

        {clustersLoading ? (
          <div className="flex items-center justify-center h-40 rounded-lg border border-border">
            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
          </div>
        ) : clusters.length === 0 ? (
          <div className="flex flex-col items-center justify-center h-48 rounded-lg border border-dashed border-border">
            <Server className="h-8 w-8 text-muted-foreground mb-3" />
            <p className="text-sm text-muted-foreground mb-3">No clusters registered yet</p>
            <Link
              href="/dashboard/clusters/register"
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity"
            >
              Register Cluster
            </Link>
          </div>
        ) : (
          <div className="rounded-lg border border-border overflow-hidden">
            <Table className="w-full text-sm">
              <TableHeader className="bg-muted/50 text-xs uppercase tracking-wide text-muted-foreground">
                <TableRow>
                  <TableHead className="text-left px-4 py-2 font-medium">Name</TableHead>
                  <TableHead className="text-left px-4 py-2 font-medium">Status</TableHead>
                  <TableHead className="text-left px-4 py-2 font-medium">Version</TableHead>
                  <TableHead className="text-right px-4 py-2 font-medium">Nodes</TableHead>
                  <TableHead className="text-right px-4 py-2 font-medium">Pods</TableHead>
                  <TableHead className="text-right px-4 py-2 font-medium">CPU</TableHead>
                  <TableHead className="text-right px-4 py-2 font-medium">Memory</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody className="divide-y divide-border">
                {clusters.map((cluster) => (
                  <TableRow key={cluster.id} className="hover:bg-muted/30 transition-colors">
                    <TableCell className="px-4 py-2">
                      <Link
                        href={`/dashboard/clusters/${cluster.id}`}
                        className="font-medium text-foreground hover:underline"
                      >
                        {cluster.name}
                      </Link>
                    </TableCell>
                    <TableCell className="px-4 py-2">
                      <StatusBadge status={cluster.status} />
                    </TableCell>
                    <TableCell className="px-4 py-2 text-muted-foreground font-mono text-xs">
                      {cluster.kubernetesVersion || '—'}
                    </TableCell>
                    <TableCell className="px-4 py-2 text-right font-mono text-xs">{cluster.nodeCount}</TableCell>
                    <TableCell className="px-4 py-2 text-right font-mono text-xs">{cluster.podCount}</TableCell>
                    <TableCell className={cn('px-4 py-2 text-right font-mono text-xs',
                      cluster.cpuPercentage >= 90 ? 'text-red-500' :
                      cluster.cpuPercentage >= 75 ? 'text-yellow-500' : 'text-muted-foreground')}>
                      {cluster.cpuPercentage != null ? `${cluster.cpuPercentage.toFixed(0)}%` : '—'}
                    </TableCell>
                    <TableCell className={cn('px-4 py-2 text-right font-mono text-xs',
                      cluster.memoryPercentage >= 90 ? 'text-red-500' :
                      cluster.memoryPercentage >= 75 ? 'text-yellow-500' : 'text-muted-foreground')}>
                      {cluster.memoryPercentage != null ? `${cluster.memoryPercentage.toFixed(0)}%` : '—'}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </section>

      {/* Two-column: Recent Activity (wider) + Platform health (signals) */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <section className="lg:col-span-2 space-y-3">
          <h2 className="text-lg font-medium text-foreground">Recent Activity</h2>
          <div className="rounded-lg border border-border overflow-hidden">
            {activityLoading ? (
              <div className="flex items-center justify-center h-48">
                <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
              </div>
            ) : activity.length === 0 ? (
              <div className="flex flex-col items-center justify-center h-48 text-muted-foreground">
                <Activity className="h-6 w-6 mb-2" />
                <p className="text-sm">No recent activity</p>
              </div>
            ) : (
              <div className="divide-y divide-border max-h-[420px] overflow-y-auto">
                {activity.map((event) => (
                  <div key={event.id} className="px-4 py-3 hover:bg-muted/30 transition-colors">
                    <div className="flex items-start gap-3">
                      <div
                        className={`mt-0.5 h-2 w-2 rounded-full flex-shrink-0 ${
                          event.type === 'cluster'
                            ? 'bg-blue-400'
                            : event.type === 'workload'
                              ? 'bg-green-400'
                              : event.type === 'deployment'
                                ? 'bg-violet-400'
                                : event.type === 'rbac'
                                  ? 'bg-yellow-400'
                                  : 'bg-zinc-400'
                        }`}
                      />
                      <div className="flex-1 min-w-0">
                        <p className="text-sm text-foreground leading-snug">{event.message}</p>
                        <div className="flex items-center gap-2 mt-1">
                          {event.user && (
                            <span className="text-xs text-muted-foreground">{event.user}</span>
                          )}
                          <span className="text-xs text-muted-foreground/60">
                            {formatRelativeTime(event.timestamp)}
                          </span>
                        </div>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </section>

        {/* Platform health — at-a-glance signals + drill-down links */}
        <section className="space-y-3">
          <h2 className="text-lg font-medium text-foreground">Platform Health</h2>
          <div className="rounded-lg border border-border bg-card divide-y divide-border">
            <HealthRow
              href="/dashboard/alerting"
              icon={<Bell className="h-4 w-4" />}
              label="Firing alerts"
              value={alertEvents.length}
              tone={criticalAlerts > 0 ? 'error' : warningAlerts > 0 ? 'warning' : 'success'}
              hint={criticalAlerts > 0 ? `${criticalAlerts} critical` : warningAlerts > 0 ? `${warningAlerts} warning` : 'All clear'}
            />
            <HealthRow
              href="/dashboard/clusters?status=disconnected"
              icon={<WifiOff className="h-4 w-4" />}
              label="Agent offline"
              value={errorClusters}
              tone={errorClusters > 0 ? 'error' : 'success'}
              hint={errorClusters > 0 ? 'reconnect needed' : 'all reachable'}
            />
            <HealthRow
              href="/dashboard/tools"
              icon={<Package className="h-4 w-4" />}
              label="Tools installed"
              value={totalTools}
              tone="default"
              hint="across all clusters"
            />
            <HealthRow
              href="/dashboard/security"
              icon={<ShieldCheck className="h-4 w-4" />}
              label="Security posture"
              value="—"
              tone="default"
              hint="run a scan"
            />
            <HealthRow
              href="/dashboard/settings/compliance/baselines"
              icon={<ShieldCheck className="h-4 w-4" />}
              label="Compliance baseline"
              value="—"
              tone="default"
              hint="not applied"
            />
            <HealthRow
              href="/dashboard/settings/backup-drill"
              icon={<ShieldCheck className="h-4 w-4" />}
              label="Backup restore drill"
              value={latestDrill?.status ?? '—'}
              tone={
                latestDrill?.status === 'success'
                  ? 'success'
                  : latestDrill?.status === 'failure'
                    ? 'error'
                    : latestDrill?.status === 'partial'
                      ? 'warning'
                      : 'default'
              }
              hint={
                latestDrill?.completedAt
                  ? `last run ${formatRelativeTime(latestDrill.completedAt)}`
                  : 'never run'
              }
            />
          </div>
          <Link
            href="/dashboard/clusters"
            className="block text-center text-xs text-muted-foreground hover:text-foreground py-1"
          >
            <TerminalSquare className="inline h-3 w-3 mr-1" />
            Open kubectl shell on any cluster
          </Link>
        </section>
      </div>

      {/* Custom widgets — only renders when operators have configured them.
          Hidden via hideWhenEmpty so the dashboard stays clean on fresh
          installs. */}
      <section className="space-y-3">
        <WidgetGrid fetcher={renderGlobal} hideWhenEmpty />
      </section>
    </div>
  );
}

function MetricTile({
  href,
  label,
  value,
  sublabel,
  icon,
  tone,
}: {
  href: string;
  label: string;
  value: string | number;
  sublabel?: string;
  icon: React.ReactNode;
  tone: 'default' | 'warning' | 'error';
}) {
  const toneRing =
    tone === 'error' ? 'ring-red-500/20 hover:ring-red-500/40' :
    tone === 'warning' ? 'ring-yellow-500/20 hover:ring-yellow-500/40' :
    'ring-transparent';
  const toneValue =
    tone === 'error' ? 'text-red-500' :
    tone === 'warning' ? 'text-yellow-500' :
    'text-foreground';
  return (
    <Link
      href={href}
      className={cn(
        'block rounded-lg border border-border bg-card p-3 hover:bg-card/80 transition-all ring-2 ring-inset',
        toneRing,
      )}
    >
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        {icon}
        <span>{label}</span>
      </div>
      <div className={cn('mt-1 text-2xl font-semibold tabular-nums', toneValue)}>{value}</div>
      {sublabel && <div className="text-xs text-muted-foreground mt-0.5">{sublabel}</div>}
    </Link>
  );
}

function ActionCard({
  href,
  icon,
  title,
  description,
}: {
  href: string;
  icon: React.ReactNode;
  title: string;
  description: string;
}) {
  return (
    <Link
      href={href}
      className="group flex items-start gap-3 rounded-lg border border-border bg-card p-3 hover:bg-card/80 hover:border-foreground/20 transition-colors"
    >
      <div className="flex-shrink-0 w-8 h-8 rounded-md bg-muted flex items-center justify-center text-muted-foreground group-hover:text-foreground transition-colors">
        {icon}
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex items-center justify-between">
          <p className="text-sm font-medium text-foreground">{title}</p>
          <ArrowRight className="h-3.5 w-3.5 text-muted-foreground opacity-0 group-hover:opacity-100 transition-opacity" />
        </div>
        <p className="text-xs text-muted-foreground mt-0.5 line-clamp-2">{description}</p>
      </div>
    </Link>
  );
}

function HealthRow({
  href,
  icon,
  label,
  value,
  tone,
  hint,
}: {
  href: string;
  icon: React.ReactNode;
  label: string;
  value: string | number;
  tone: 'default' | 'warning' | 'error' | 'success';
  hint?: string;
}) {
  const dot =
    tone === 'error' ? 'bg-red-500' :
    tone === 'warning' ? 'bg-yellow-500' :
    tone === 'success' ? 'bg-green-500' :
    'bg-zinc-400';
  return (
    <Link
      href={href}
      className="flex items-center justify-between px-3 py-2.5 hover:bg-muted/30 transition-colors"
    >
      <div className="flex items-center gap-2 min-w-0">
        <span className={cn('h-2 w-2 rounded-full flex-shrink-0', dot)} />
        <span className="text-muted-foreground flex-shrink-0">{icon}</span>
        <span className="text-sm text-foreground truncate">{label}</span>
      </div>
      <div className="flex items-center gap-2 flex-shrink-0">
        {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
        <span className="text-sm font-medium tabular-nums text-foreground">{value}</span>
      </div>
    </Link>
  );
}
