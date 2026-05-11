'use client';

import { useRouter } from 'next/navigation';
import { StatusBadge } from '@/components/ui/status-badge';
import {
  formatPercentage,
  distributionDisplayName,
  formatRelativeTime,
  cn,
  gaugeColor,
} from '@/lib/utils';
import type { Cluster } from '@/types';
import { Server, Box, Clock } from 'lucide-react';

interface ClusterCardProps {
  cluster: Cluster;
  className?: string;
}

export function ClusterCard({ cluster, className }: ClusterCardProps) {
  const router = useRouter();

  return (
    <div
      onClick={() => router.push(`/dashboard/clusters/${cluster.id}`)}
      className={cn(
        'group rounded-lg border border-border bg-card p-4 space-y-3 cursor-pointer transition-all',
        'hover:bg-card/80 hover:border-border/80 hover:shadow-lg hover:shadow-black/5',
        className
      )}
    >
      {/* Header */}
      <div className="flex items-start justify-between">
        <div className="min-w-0 flex-1">
          <h3 className="font-medium text-foreground truncate group-hover:text-foreground/90">
            {cluster.displayName}
          </h3>
          <div className="flex items-center gap-2 mt-0.5">
            <span className="text-xs text-muted-foreground">
              {distributionDisplayName(cluster.distribution)}
            </span>
          </div>
        </div>
        <StatusBadge status={cluster.status} size="sm" />
      </div>

      {/* Resource Bars */}
      <div className="space-y-2">
        <div className="space-y-1">
          <div className="flex items-center justify-between text-xs">
            <span className="text-muted-foreground">CPU</span>
            <span className="text-muted-foreground tabular-nums">
              {formatPercentage(cluster.cpuPercentage, cluster.cpuPercentage < 10 ? 1 : 0)}
            </span>
          </div>
          <div className="gauge-bar">
            <div
              className={cn('gauge-bar-fill', gaugeColor(cluster.cpuPercentage))}
              style={{ width: `${Math.min(cluster.cpuPercentage, 100)}%` }}
            />
          </div>
        </div>

        <div className="space-y-1">
          <div className="flex items-center justify-between text-xs">
            <span className="text-muted-foreground">Memory</span>
            <span className="text-muted-foreground tabular-nums">
              {formatPercentage(cluster.memoryPercentage, cluster.memoryPercentage < 10 ? 1 : 0)}
            </span>
          </div>
          <div className="gauge-bar">
            <div
              className={cn('gauge-bar-fill', gaugeColor(cluster.memoryPercentage))}
              style={{ width: `${Math.min(cluster.memoryPercentage, 100)}%` }}
            />
          </div>
        </div>
      </div>

      {/* Footer stats */}
      <div className="flex items-center justify-between pt-1 border-t border-border">
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-1 text-xs text-muted-foreground">
            <Server className="h-3 w-3" />
            <span className="tabular-nums">{cluster.nodeCount}</span>
            <span>nodes</span>
          </div>
          <div className="flex items-center gap-1 text-xs text-muted-foreground">
            <Box className="h-3 w-3" />
            <span className="tabular-nums">{cluster.podCount}</span>
            <span>pods</span>
          </div>
        </div>
        <span
          className={cn(
            'inline-flex items-center gap-1 text-2xs px-1.5 py-0.5 rounded capitalize',
            cluster.environment === 'production'
              ? 'bg-status-error/10 text-status-error'
              : cluster.environment === 'staging'
                ? 'bg-status-warning/10 text-status-warning'
                : 'bg-status-info/10 text-status-info'
          )}
        >
          {cluster.environment}
        </span>
      </div>
    </div>
  );
}
