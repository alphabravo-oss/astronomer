'use client';

import { cn } from '@/lib/utils';
import { Check, RefreshCw, HelpCircle } from 'lucide-react';
import type { ArgoSyncStatus, ArgoHealthStatus } from '@/types';

interface SyncStatusBadgeProps {
  syncStatus: ArgoSyncStatus;
  className?: string;
}

export function SyncStatusBadge({ syncStatus, className }: SyncStatusBadgeProps) {
  const config: Record<ArgoSyncStatus, { icon: typeof Check; color: string; label: string }> = {
    Synced: {
      icon: Check,
      color: 'bg-status-success/10 text-status-success',
      label: 'Synced',
    },
    OutOfSync: {
      icon: RefreshCw,
      color: 'bg-status-warning/10 text-status-warning',
      label: 'Out of Sync',
    },
    Unknown: {
      icon: HelpCircle,
      color: 'bg-status-neutral/10 text-status-neutral',
      label: 'Unknown',
    },
  };

  const { icon: Icon, color, label } = config[syncStatus] || config.Unknown;

  return (
    <span className={cn('inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium', color, className)}>
      <Icon className="h-3 w-3" />
      {label}
    </span>
  );
}

interface HealthStatusBadgeProps {
  healthStatus: ArgoHealthStatus;
  className?: string;
}

export function HealthStatusBadge({ healthStatus, className }: HealthStatusBadgeProps) {
  const colorMap: Record<ArgoHealthStatus, string> = {
    Healthy: 'bg-status-success/10 text-status-success',
    Degraded: 'bg-status-warning/10 text-status-warning',
    Progressing: 'bg-status-info/10 text-status-info',
    Suspended: 'bg-status-neutral/10 text-status-neutral',
    Missing: 'bg-status-error/10 text-status-error',
    Unknown: 'bg-status-neutral/10 text-status-neutral',
  };

  const dotMap: Record<ArgoHealthStatus, string> = {
    Healthy: 'bg-status-success',
    Degraded: 'bg-status-warning',
    Progressing: 'bg-status-info',
    Suspended: 'bg-status-neutral',
    Missing: 'bg-status-error',
    Unknown: 'bg-status-neutral',
  };

  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium',
        colorMap[healthStatus] || colorMap.Unknown,
        className
      )}
    >
      <span className={cn('h-1.5 w-1.5 rounded-full', dotMap[healthStatus] || dotMap.Unknown)} />
      {healthStatus}
    </span>
  );
}
