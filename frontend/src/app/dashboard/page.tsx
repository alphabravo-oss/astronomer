'use client';

import { useClusters, useActivityFeed, queryKeys } from '@/lib/hooks';
import { useLiveQueryInvalidation } from '@/lib/live-events';
import { MetricCard } from '@/components/ui/metric-card';
import { ClusterCard } from '@/components/clusters/cluster-card';
import { StatusBadge } from '@/components/ui/status-badge';
import { formatRelativeTime } from '@/lib/utils';
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

      {/* Main content grid */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        {/* Cluster Grid */}
        <div className="lg:col-span-2 space-y-4">
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
            <div className="flex items-center justify-center h-48">
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
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              {clusters.slice(0, 6).map((cluster) => (
                <ClusterCard key={cluster.id} cluster={cluster} />
              ))}
            </div>
          )}
        </div>

        {/* Activity Feed */}
        <div className="space-y-4">
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
        </div>
      </div>
    </div>
  );
}
