'use client';

// Phase B1: ArgoCD instances index.
//
// Lists every registered ArgoCD instance and lets operators register a new
// one. Row click navigates to the per-instance detail page.

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { useQuery } from '@tanstack/react-query';
import { Plus, GitBranch, ExternalLink } from 'lucide-react';
import api from '@/lib/api';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { PageHeader, PageSection, PageShell } from '@/components/ui/page';
import { useLiveQueryInvalidation } from '@/lib/live-events';
import { RegisterInstanceModal } from '@/components/argocd/register-instance-modal';
import { formatRelativeTime } from '@/lib/utils';
import { queryKeys } from '@/lib/hooks';
import type { PaginatedResponse, ArgoInstanceB1 } from '@/types';

interface InstanceRow extends ArgoInstanceB1 {
  // Optional enrichment for the table; resolved via the per-instance app
  // count probe below. Kept inline so the column accessors don't need a
  // second data structure.
  appCount?: number;
}

export default function ArgoCDInstancesPage() {
  const router = useRouter();
  const [showRegister, setShowRegister] = useState(false);

  const { data: instancesPage, isLoading } = useQuery({
    queryKey: queryKeys.argocd.instances(),
    queryFn: async () => {
      const res = await api.get<PaginatedResponse<ArgoInstanceB1>>('/argocd/instances');
      return res.data;
    },
    refetchInterval: 30000,
  });

  // The k8s_changed event is the closest thing we have today to an
  // ArgoCD-mutation signal; reusing it keeps the page reactive when
  // someone registers a new instance from another tab/session.
  useLiveQueryInvalidation(
    ['cluster.connected', 'cluster.disconnected', 'cluster.k8s_changed'],
    [queryKeys.argocd.instances()],
  );

  const instances: InstanceRow[] = instancesPage?.data ?? [];

  const columns: Column<InstanceRow>[] = [
    {
      key: 'name',
      header: 'Display Name',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <GitBranch className="h-4 w-4 text-muted-foreground" />
          <span className="font-medium text-foreground">{row.name}</span>
        </div>
      ),
      sortAccessor: (row) => row.name,
    },
    {
      key: 'apiUrl',
      header: 'URL',
      accessor: (row) => (
        <a
          href={row.apiUrl}
          target="_blank"
          rel="noopener noreferrer"
          onClick={(e) => e.stopPropagation()}
          className="inline-flex items-center gap-1 text-xs font-mono text-muted-foreground hover:text-foreground"
        >
          {row.apiUrl}
          <ExternalLink className="h-3 w-3" />
        </a>
      ),
    },
    {
      key: 'health',
      header: 'Health',
      accessor: (row) => (
        <StatusBadge
          status={row.isHealthy ? 'healthy' : 'unhealthy'}
          label={row.isHealthy ? 'Healthy' : 'Unhealthy'}
        />
      ),
      sortAccessor: (row) => (row.isHealthy ? 1 : 0),
    },
    {
      key: 'verifySsl',
      header: 'TLS',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.verifySsl ? 'Verify' : 'Skip'}
        </span>
      ),
    },
    {
      key: 'createdAt',
      header: 'Registered',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {formatRelativeTime(row.createdAt)}
        </span>
      ),
      sortAccessor: (row) => row.createdAt,
    },
  ];

  return (
    <PageShell>
      <PageHeader
        title="ArgoCD"
        description="GitOps control planes registered with Astronomer."
        actions={(
          <button
            onClick={() => setShowRegister(true)}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity"
          >
            <Plus className="h-4 w-4" />
            Register Instance
          </button>
        )}
      />

      <PageSection>
        <DataTable
          data={instances}
          columns={columns}
          keyExtractor={(row) => row.id}
          onRowClick={(row) => router.push(`/dashboard/argocd/${row.id}`)}
          searchPlaceholder="Search instances..."
          loading={isLoading}
          emptyMessage="No ArgoCD instances yet. Register one to start managing applications."
        />
      </PageSection>

      {showRegister && <RegisterInstanceModal onClose={() => setShowRegister(false)} />}
    </PageShell>
  );
}
