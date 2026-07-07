'use client';

/**
 * Fleet Operations — list + create launcher (DIR-01).
 *
 * Surfaces the bulk fleet-operations backend (upgrade / install / uninstall /
 * apply-template / rotate-agent-token fanned out across the fleet) as a
 * first-class dashboard feature. Read-gated on `fleet_operations:list`;
 * create is gated on `fleet_operations:create`.
 */
import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Plus, Rocket } from 'lucide-react';
import { PageHeader, PageShell } from '@/components/ui/page';
import { PermissionState } from '@/components/ui/empty-state';
import { FleetOperationList } from '@/components/fleet/fleet-operation-list';
import { CreateFleetOperationDialog } from '@/components/fleet/create-fleet-operation-dialog';
import { useCurrentUser } from '@/lib/hooks';
import { can } from '@/lib/permissions';
import { getFleetOperations, type FleetOperationStatus } from '@/lib/api/fleet-operations';
import { queryKeys } from '@/lib/hooks';

const STATUS_FILTERS: { value: '' | FleetOperationStatus; label: string }[] = [
  { value: '', label: 'All statuses' },
  { value: 'pending', label: 'Pending' },
  { value: 'running', label: 'Running' },
  { value: 'paused', label: 'Paused' },
  { value: 'completed', label: 'Completed' },
  { value: 'failed', label: 'Failed' },
  { value: 'aborted', label: 'Aborted' },
];

export default function FleetOperationsPage() {
  const { data: user } = useCurrentUser();
  const canList = can(user, 'fleet_operations', 'list');
  const canCreate = can(user, 'fleet_operations', 'create');

  const [status, setStatus] = useState<'' | FleetOperationStatus>('');
  const [showCreate, setShowCreate] = useState(false);

  const params = status ? { status } : undefined;
  const query = useQuery({
    queryKey: queryKeys.fleetOperations.list(params),
    queryFn: () => getFleetOperations({ ...(params ?? {}), limit: 100 }),
    enabled: canList,
    // Poll the list so in-flight operations' counters/status stay fresh.
    refetchInterval: 15000,
  });

  if (!canList) {
    return (
      <PageShell>
        <PageHeader
          eyebrow="Platform"
          title="Fleet Operations"
          description="Drive an upgrade, install, or template apply across the whole fleet from one place."
        />
        <PermissionState permission="fleet_operations:list" />
      </PageShell>
    );
  }

  return (
    <PageShell>
      <PageHeader
        eyebrow="Platform"
        title="Fleet Operations"
        description="Drive an upgrade, install, or template apply across the whole fleet from one place."
        actions={
          canCreate ? (
            <button
              type="button"
              onClick={() => setShowCreate(true)}
              className="inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground hover:bg-primary/90"
            >
              <Plus className="h-4 w-4" /> New operation
            </button>
          ) : null
        }
      />

      <div className="flex items-center gap-2">
        <Rocket className="h-4 w-4 text-muted-foreground" />
        <select
          aria-label="status filter"
          value={status}
          onChange={(e) => setStatus(e.target.value as '' | FleetOperationStatus)}
          className="h-8 rounded-md border border-border bg-background px-2 text-sm focus:outline-none focus:ring-1 focus:ring-ring"
        >
          {STATUS_FILTERS.map((f) => (
            <option key={f.value} value={f.value}>
              {f.label}
            </option>
          ))}
        </select>
      </div>

      <FleetOperationList
        operations={query.data?.data ?? []}
        loading={query.isLoading}
        isError={query.isError}
        onRetry={() => query.refetch()}
      />

      {showCreate && <CreateFleetOperationDialog onClose={() => setShowCreate(false)} />}
    </PageShell>
  );
}
