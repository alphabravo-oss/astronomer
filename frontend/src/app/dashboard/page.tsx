'use client';

import { useClusters, useActivityFeed, queryKeys } from '@/lib/hooks';
import { useLiveQueryInvalidation } from '@/lib/live-events';
import { MetricCard } from '@/components/ui/metric-card';
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
} from 'lucide-react';
import Link from 'next/link';

export default function DashboardPage() {
  const { data: clustersData, isLoading: clustersLoading } = useClusters({ pageSize: 100 });
  const { data: activityData, isLoading: activityLoading } = useActivityFeed(15);

  // Refresh activity + cluster summaries on any cluster lifecycle event.
  // The metrics merger in the layout already patches CPU/mem/pod-count in
  // place, so we don't invalidate on `cluster.metrics` here — just on the
  // coarser shape changes.
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

  const activeClusters = clusters.filter((c) => c.status === 'active').length;
  const warningClusters = clusters.filter((c) => c.status === 'warning').length;
  const errorClusters = clusters.filter((c) => c.status === 'error' || c.status === 'disconnected').length;
  const totalNodes = clusters.reduce((acc, c) => acc + c.nodeCount, 0);
  const totalPods = clusters.reduce((acc, c) => acc + c.podCount, 0);

  return (
    <div className="space-y-8">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight">
          Platform Overview
        </h1>
        <p className="text-sm text-muted-foreground mt-1">
          Real-time status of your Kubernetes infrastructure
        </p>
      </div>

      {/* Custom dashboard widgets (migration 058). Hidden when no
          widgets are configured to keep the platform overview clean
          on a fresh install. */}
      <section className="space-y-2">
        <h2 className="text-sm font-medium text-muted-foreground uppercase tracking-wide">Widgets</h2>
        <WidgetGrid fetcher={renderGlobal} emptyHint="" />
      </section>

      {/* Summary Cards */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-4">
        <MetricCard
          title="Total Clusters"
          value={clusters.length}
          icon={<Server className="h-4 w-4" />}
        />
        <MetricCard
          title="Active"
          value={activeClusters}
          subtitle={`${clusters.length > 0 ? ((activeClusters / clusters.length) * 100).toFixed(0) : 0}% healthy`}
          icon={<Activity className="h-4 w-4" />}
        />
        <MetricCard
          title="Warnings"
          value={warningClusters}
          icon={<AlertTriangle className="h-4 w-4" />}
        />
        <MetricCard
          title="Disconnected"
          value={errorClusters}
          icon={<WifiOff className="h-4 w-4" />}
        />
        <MetricCard
          title="Total Pods"
          value={totalPods.toLocaleString()}
          subtitle={`across ${totalNodes} nodes`}
        />
      </div>

      {/* Clusters table — full width */}
      <section className="space-y-4">
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
          <div className="flex items-center justify-center h-48 rounded-lg border border-border">
            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
          </div>
        ) : clusters.length === 0 ? (
          <div className="flex flex-col items-center justify-center h-48 rounded-lg border border-dashed border-border">
            <Server className="h-8 w-8 text-muted-foreground mb-3" />
            <p className="text-sm text-muted-foreground mb-3">No clusters registered yet</p>
            <Link
              href="/dashboard/clusters?register=true"
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity"
            >
              Register Cluster
            </Link>
          </div>
        ) : (
          <div className="rounded-lg border border-border overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-muted/50 text-xs uppercase tracking-wide text-muted-foreground">
                <tr>
                  <th className="text-left px-4 py-2 font-medium">Name</th>
                  <th className="text-left px-4 py-2 font-medium">Status</th>
                  <th className="text-left px-4 py-2 font-medium">Version</th>
                  <th className="text-right px-4 py-2 font-medium">Nodes</th>
                  <th className="text-right px-4 py-2 font-medium">Pods</th>
                  <th className="text-right px-4 py-2 font-medium">CPU</th>
                  <th className="text-right px-4 py-2 font-medium">Memory</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {clusters.map((cluster) => (
                  <tr key={cluster.id} className="hover:bg-muted/30 transition-colors">
                    <td className="px-4 py-2">
                      <Link
                        href={`/dashboard/clusters/${cluster.id}`}
                        className="font-medium text-foreground hover:underline"
                      >
                        {cluster.name}
                      </Link>
                    </td>
                    <td className="px-4 py-2">
                      <StatusBadge status={cluster.status} />
                    </td>
                    <td className="px-4 py-2 text-muted-foreground font-mono text-xs">
                      {cluster.kubernetesVersion || '—'}
                    </td>
                    <td className="px-4 py-2 text-right font-mono text-xs">{cluster.nodeCount}</td>
                    <td className="px-4 py-2 text-right font-mono text-xs">{cluster.podCount}</td>
                    <td className={cn('px-4 py-2 text-right font-mono text-xs',
                      cluster.cpuPercentage >= 90 ? 'text-red-500' :
                      cluster.cpuPercentage >= 75 ? 'text-yellow-500' : 'text-muted-foreground')}>
                      {cluster.cpuPercentage != null ? `${cluster.cpuPercentage.toFixed(0)}%` : '—'}
                    </td>
                    <td className={cn('px-4 py-2 text-right font-mono text-xs',
                      cluster.memoryPercentage >= 90 ? 'text-red-500' :
                      cluster.memoryPercentage >= 75 ? 'text-yellow-500' : 'text-muted-foreground')}>
                      {cluster.memoryPercentage != null ? `${cluster.memoryPercentage.toFixed(0)}%` : '—'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {/* Recent Activity — below clusters, full width */}
      <section className="space-y-4">
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
            <div className="divide-y divide-border max-h-[500px] overflow-y-auto">
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
                      <p className="text-sm text-foreground leading-snug">
                        {event.message}
                      </p>
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
    </div>
  );
}
