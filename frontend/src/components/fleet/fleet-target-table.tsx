'use client';

import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { getClusters } from '@/lib/api';
import { getFleetTargets, isTerminalFleetStatus } from '@/lib/api/fleet-operations';
import { queryKeys } from '@/lib/hooks';
import { formatRelativeTime } from '@/lib/utils';
import { FleetStatusBadge } from './fleet-status-badge';

interface FleetTargetTableProps {
  operationId: string;
  /** Parent operation status — polling stops once it is terminal. */
  status: string;
}

export function FleetTargetTable({ operationId, status }: FleetTargetTableProps) {
  const terminal = isTerminalFleetStatus(status);

  const targetsQuery = useQuery({
    queryKey: queryKeys.fleetOperations.targets(operationId),
    queryFn: () => getFleetTargets(operationId, { limit: 200 }),
    refetchInterval: terminal ? false : 5000,
  });

  const clustersQuery = useQuery({
    queryKey: queryKeys.clusters.list(),
    queryFn: () => getClusters({ pageSize: 500 }),
  });

  const clusterName = useMemo(() => {
    const map = new Map<string, string>();
    for (const c of clustersQuery.data?.data ?? []) {
      map.set(c.id, c.displayName || c.name);
    }
    return map;
  }, [clustersQuery.data]);

  const targets = targetsQuery.data?.data ?? [];

  if (targetsQuery.isLoading) {
    return <p className="text-sm text-muted-foreground">Loading targets…</p>;
  }

  if (targets.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No targets yet. The orchestrator evaluates the selector on the pending → running
        transition; targets appear once the operation starts.
      </p>
    );
  }

  return (
    <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Cluster</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Sub-operation</TableHead>
            <TableHead>Started</TableHead>
            <TableHead>Completed</TableHead>
            <TableHead>Last error</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {targets.map((t) => (
            <TableRow key={t.id}>
              <TableCell>
                <span className="font-medium text-foreground">
                  {clusterName.get(t.cluster_id) ?? t.cluster_id}
                </span>
              </TableCell>
              <TableCell>
                <FleetStatusBadge status={t.status} />
              </TableCell>
              <TableCell className="text-muted-foreground">{t.sub_operation_type || '—'}</TableCell>
              <TableCell className="text-muted-foreground">
                {t.started_at ? formatRelativeTime(t.started_at) : '—'}
              </TableCell>
              <TableCell className="text-muted-foreground">
                {t.completed_at ? formatRelativeTime(t.completed_at) : '—'}
              </TableCell>
              <TableCell className="max-w-xs truncate text-status-error" title={t.last_error}>
                {t.last_error || '—'}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
