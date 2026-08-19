import { createFileRoute } from '@tanstack/react-router';
import { useEffect, useMemo } from 'react';
import { ArrowRight, Server } from 'lucide-react';
import { useClusters } from '@/lib/hooks';
import { Link } from '@/lib/link';
import { useRouter, useSearchParams } from '@/lib/navigation';
import { PageHeader, PageShell } from '@/components/ui/page';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { formatPercentage } from '@/lib/utils';
import type { Cluster } from '@/types';
import { LoadingState } from '@/components/ui/empty-state';

function clusterMetricsPath(clusterId: string, range?: string | null): string {
  const base = `/dashboard/clusters/${clusterId}/metrics`;
  return range ? `${base}?range=${encodeURIComponent(range)}` : base;
}

function MonitoringFleetPage() {
  const router = useRouter();
  const search = useSearchParams();
  const clusterId = search.get('cluster');
  const range = search.get('range');
  const { data: clustersData, isLoading, isError, refetch } = useClusters({ pageSize: 100 });
  const clusters = useMemo(() => clustersData?.data ?? [], [clustersData]);

  useEffect(() => {
    if (!clusterId) return;
    router.replace(clusterMetricsPath(clusterId, range));
  }, [clusterId, range, router]);

  const columns: Column<Cluster>[] = [
    {
      key: 'name',
      header: 'Cluster',
      accessor: (row) => (
        <div className="min-w-0">
          <p className="font-medium text-foreground truncate">{row.displayName || row.name}</p>
          <p className="text-xs text-muted-foreground truncate">
            {row.environment || '—'} {row.region ? `· ${row.region}` : ''}
          </p>
        </div>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => <StatusBadge status={row.status} />,
    },
    {
      key: 'cpu',
      header: 'CPU',
      accessor: (row) => (
        <span className="text-xs tabular-nums text-muted-foreground">
          {formatPercentage(row.cpuPercentage, 0)}
        </span>
      ),
      sortAccessor: (row) => row.cpuPercentage,
    },
    {
      key: 'memory',
      header: 'Memory',
      accessor: (row) => (
        <span className="text-xs tabular-nums text-muted-foreground">
          {formatPercentage(row.memoryPercentage, 0)}
        </span>
      ),
      sortAccessor: (row) => row.memoryPercentage,
    },
    {
      key: 'pods',
      header: 'Pods',
      accessor: (row) => (
        <span className="text-xs tabular-nums text-muted-foreground">{row.podCount}</span>
      ),
      sortAccessor: (row) => row.podCount,
      align: 'center',
    },
    {
      key: 'open',
      header: '',
      accessor: (row) => (
        <Link
          href={clusterMetricsPath(row.id)}
          className="inline-flex items-center gap-1 text-xs font-medium text-muted-foreground hover:text-foreground"
        >
          Metrics
          <ArrowRight className="h-3.5 w-3.5" />
        </Link>
      ),
      sortable: false,
      align: 'right',
    },
  ];

  if (clusterId) {
    return <LoadingState title="Opening cluster metrics" />;
  }

  return (
    <PageShell>
      <PageHeader
        title="Fleet metrics"
        description="Open a cluster to see dashboards, node utilization, and the Prometheus stack for that environment."
      />
      {clusters.length === 0 && !isLoading ? (
        <div className="flex items-center gap-3 p-4 rounded-lg border border-border bg-card">
          <Server className="h-5 w-5 text-muted-foreground flex-shrink-0" />
          <p className="text-sm text-muted-foreground">
            No clusters registered yet. Register a cluster to view metrics.
          </p>
        </div>
      ) : (
        <DataTable
          data={clusters}
          columns={columns}
          keyExtractor={(row) => row.id}
          searchPlaceholder="Search clusters..."
          loading={isLoading}
          isError={isError}
          onRetry={() => refetch()}
          emptyMessage="No clusters registered yet"
          onRowClick={(row) => router.push(clusterMetricsPath(row.id))}
        />
      )}
    </PageShell>
  );
}

export const Route = createFileRoute('/dashboard/monitoring/')({
  component: MonitoringFleetPage,
});
