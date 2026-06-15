'use client';

import { Check, RefreshCw, HelpCircle } from 'lucide-react';
import { StatusBadge } from '@/components/ui/status-badge';
import type { ArgoSyncStatus, ArgoHealthStatus } from '@/types';

interface SyncStatusBadgeProps {
  syncStatus: ArgoSyncStatus;
  className?: string;
}

export function SyncStatusBadge({ syncStatus, className }: SyncStatusBadgeProps) {
  const config: Record<ArgoSyncStatus, { icon: typeof Check; label: string }> = {
    Synced: {
      icon: Check,
      label: 'Synced',
    },
    OutOfSync: {
      icon: RefreshCw,
      label: 'Out of Sync',
    },
    Unknown: {
      icon: HelpCircle,
      label: 'Unknown',
    },
  };

  const { icon: Icon, label } = config[syncStatus] || config.Unknown;

  return (
    <StatusBadge
      status={syncStatus}
      label={label}
      icon={<Icon className="h-3 w-3" />}
      className={className}
    />
  );
}

interface HealthStatusBadgeProps {
  healthStatus: ArgoHealthStatus;
  className?: string;
}

export function HealthStatusBadge({ healthStatus, className }: HealthStatusBadgeProps) {
  return <StatusBadge status={healthStatus} label={healthStatus} className={className} />;
}
