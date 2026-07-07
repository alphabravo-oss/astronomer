'use client';

import { cn } from '@/lib/utils';

/**
 * Shared status pill for fleet operations and their per-cluster targets.
 * A dedicated map (rather than the generic statusBgColor) so paused /
 * aborted / skipped / completed read the way an operator expects.
 */
const FLEET_STATUS_COLOR: Record<string, string> = {
  pending: 'bg-status-info/10 text-status-info',
  running: 'bg-status-info/10 text-status-info',
  paused: 'bg-status-warning/10 text-status-warning',
  completed: 'bg-status-success/10 text-status-success',
  succeeded: 'bg-status-success/10 text-status-success',
  failed: 'bg-status-error/10 text-status-error',
  aborted: 'bg-status-error/10 text-status-error',
  skipped: 'bg-status-neutral/10 text-status-neutral',
};

function labelFor(status: string): string {
  return status.charAt(0).toUpperCase() + status.slice(1);
}

export function FleetStatusBadge({ status, className }: { status: string; className?: string }) {
  const key = status.toLowerCase();
  const color = FLEET_STATUS_COLOR[key] ?? 'bg-status-neutral/10 text-status-neutral';
  const active = key === 'running';
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium',
        color,
        className,
      )}
    >
      <span className="relative flex h-1.5 w-1.5">
        {active && (
          <span className="absolute inline-flex h-full w-full rounded-full bg-current opacity-75 animate-pulse-dot" />
        )}
        <span className="relative inline-flex h-1.5 w-1.5 rounded-full bg-current" />
      </span>
      {labelFor(status)}
    </span>
  );
}
