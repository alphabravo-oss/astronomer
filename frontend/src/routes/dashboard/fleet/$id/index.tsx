import { createFileRoute } from '@tanstack/react-router';
/**
 * Fleet Operations · Detail (DIR-01).
 *
 * Header (status + rollup counters), lifecycle controls, and a polling
 * per-cluster target-status table. Polling stops when the operation reaches
 * a terminal status (completed / failed / aborted).
 */
import { useQuery } from '@tanstack/react-query';
import { Link } from '@/lib/link';
import { useParams } from '@/lib/navigation';
import { ArrowLeft } from 'lucide-react';
import { PageShell } from '@/components/ui/page';
import { ErrorState, LoadingState, PermissionState } from '@/components/ui/empty-state';
import { FleetStatusBadge } from '@/components/fleet/fleet-status-badge';
import { FleetOperationControls } from '@/components/fleet/fleet-operation-controls';
import { FleetTargetTable } from '@/components/fleet/fleet-target-table';
import { useCurrentUser } from '@/lib/hooks';
import { can } from '@/lib/permissions';
import { queryKeys } from '@/lib/hooks';
import {
  fleetOpTypeLabel,
  getFleetOperation,
  isTerminalFleetStatus,
} from '@/lib/api/fleet-operations';
import { cn, formatRelativeTime } from '@/lib/utils';

function FleetOperationDetailPage() {
  const params = useParams();
  const id = params.id as string;
  const { data: user } = useCurrentUser();
  const canRead = can(user, 'fleet_operations', 'read');
  const canUpdate = can(user, 'fleet_operations', 'update');

  const query = useQuery({
    queryKey: queryKeys.fleetOperations.detail(id),
    queryFn: () => getFleetOperation(id),
    enabled: canRead,
    refetchInterval: (q) => {
      const s = q.state.data?.status;
      return s && isTerminalFleetStatus(s) ? false : 5000;
    },
  });

  const op = query.data;

  const back = (
    <Link
      href="/dashboard/fleet"
      className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
    >
      <ArrowLeft className="h-3.5 w-3.5" /> Fleet Operations
    </Link>
  );

  if (!canRead) {
    return (
      <PageShell>
        {back}
        <PermissionState permission="fleet_operations:read" />
      </PageShell>
    );
  }

  if (query.isLoading) {
    return (
      <PageShell>
        {back}
        <LoadingState />
      </PageShell>
    );
  }

  if (query.isError || !op) {
    return (
      <PageShell>
        {back}
        <ErrorState description="Failed to load fleet operation." onRetry={() => query.refetch()} />
      </PageShell>
    );
  }

  const counters: { label: string; value: number; tone: string }[] = [
    { label: 'Total', value: op.total_clusters, tone: 'text-foreground' },
    { label: 'Completed', value: op.completed_clusters, tone: 'text-status-success' },
    { label: 'Failed', value: op.failed_clusters, tone: 'text-status-error' },
    { label: 'Skipped', value: op.skipped_clusters, tone: 'text-status-neutral' },
  ];

  return (
    <PageShell>
      {back}

      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="flex items-center gap-3">
            <h1 className="truncate text-2xl font-semibold tracking-tight text-foreground">{op.name}</h1>
            <FleetStatusBadge status={op.status} />
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            {fleetOpTypeLabel(op.operation_type)}
            {' · '}
            {op.strategy}
            {op.strategy === 'parallel' ? ` (max ${op.max_concurrent})` : ''}
            {' · on error: '}
            {op.on_error}
          </p>
          {op.description ? (
            <p className="mt-1 max-w-3xl text-sm text-muted-foreground">{op.description}</p>
          ) : null}
        </div>
        <FleetOperationControls operation={op} canUpdate={canUpdate} />
      </div>

      {op.last_error ? (
        <div className="rounded-md border border-status-error/40 bg-status-error/10 px-4 py-3 text-sm text-status-error">
          {op.last_error}
        </div>
      ) : null}

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        {counters.map((c) => (
          <div key={c.label} className="rounded-lg border border-border bg-card p-4">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">{c.label}</p>
            <p className={cn('mt-1 text-2xl font-semibold', c.tone)}>{c.value}</p>
          </div>
        ))}
      </div>

      <div className="flex flex-wrap gap-x-6 gap-y-1 text-xs text-muted-foreground">
        <span>Created {formatRelativeTime(op.created_at)}</span>
        {op.started_at ? <span>Started {formatRelativeTime(op.started_at)}</span> : null}
        {op.completed_at ? <span>Completed {formatRelativeTime(op.completed_at)}</span> : null}
      </div>

      <div className="space-y-3">
        <h2 className="text-sm font-semibold text-foreground">Targets</h2>
        <FleetTargetTable operationId={op.id} status={op.status} />
      </div>
    </PageShell>
  );
}

export const Route = createFileRoute('/dashboard/fleet/$id/')({
  component: FleetOperationDetailPage,
});
