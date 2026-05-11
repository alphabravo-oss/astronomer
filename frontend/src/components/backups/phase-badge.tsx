'use client';

/**
 * Phase badge for Velero Backup / Restore CRs. The Velero phase set
 * (`New`, `InProgress`, `Completed`, `PartiallyFailed`, `Failed`,
 * `FailedValidation`, `Deleting`, `Finalizing`) doesn't map cleanly onto
 * the dashboard's generic `StatusBadge` colour table, so this component
 * does an explicit mapping. Falls back to the row's plain `status` column
 * when the reconciler hasn't projected a phase yet.
 */

import { StatusBadge } from '@/components/ui/status-badge';
import type { BackupStatus, VeleroPhase } from '@/types';

function phaseToStatus(phase: VeleroPhase | undefined, fallback: BackupStatus): string {
  if (!phase) return fallback;
  switch (phase) {
    case 'Completed':
      return 'completed';
    case 'InProgress':
    case 'New':
    case 'Finalizing':
      return 'pending';
    case 'PartiallyFailed':
      return 'warning';
    case 'Failed':
    case 'FailedValidation':
      return 'failed';
    case 'Deleting':
      return 'disconnected';
    default:
      return String(phase).toLowerCase();
  }
}

interface PhaseBadgeProps {
  phase?: VeleroPhase;
  status: BackupStatus;
  size?: 'sm' | 'md' | 'lg';
}

export function PhaseBadge({ phase, status, size = 'md' }: PhaseBadgeProps) {
  const display = phase || status;
  return <StatusBadge status={phaseToStatus(phase, status)} label={display} size={size} />;
}
