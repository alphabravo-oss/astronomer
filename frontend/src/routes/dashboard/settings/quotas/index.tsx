import { createFileRoute } from '@tanstack/react-router';
/**
 * /dashboard/settings/quotas — quota plan index + at-a-glance usage joined
 * from `GET /admin/quota-usage/`. Each row links to the detail page; usage
 * is shown as a small "N in use" + worst-cap bar so operators can spot the
 * hot plans without clicking through.
 */
import { Link } from '@/lib/link';
import { useRouter } from '@/lib/navigation';
import {
  ArrowLeft,
  ExternalLink,
  Gauge,
  Plus,
} from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { ActionButton } from '@/components/ui/action-button';
import { PageHeader, PageShell } from '@/components/ui/page';
import { cn, formatRelativeTime } from '@/lib/utils';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import {
  useQuotaPlans,
  useQuotaUsage,
} from '@/components/settings/hooks';
import type { QuotaPlan, QuotaUsageRow } from '@/lib/api/settings';

function enforcementBadge(e: QuotaPlan['enforcement']) {
  const palette: Record<QuotaPlan['enforcement'], string> = {
    hard: 'bg-status-error/10 text-status-error border-status-error/30',
    soft: 'bg-status-warning/10 text-status-warning border-status-warning/30',
    disabled: 'bg-muted text-muted-foreground border-border',
  };
  return (
    <span className={cn('text-xs px-2 py-0.5 rounded border font-medium capitalize', palette[e])}>
      {e}
    </span>
  );
}

function maxUtilization(rows: QuotaUsageRow[]): number {
  let worst = 0;
  for (const r of rows) {
    for (const v of Object.values(r.utilization ?? {})) {
      if (v > worst) worst = v;
    }
  }
  return worst;
}

function UtilizationBar({ pct }: { pct: number }) {
  const clamped = Math.max(0, Math.min(100, pct));
  const color =
    clamped >= 95
      ? 'bg-status-error'
      : clamped >= 80
        ? 'bg-status-warning'
        : 'bg-status-success';
  return (
    <div className="flex items-center gap-2">
      <div className="flex-1 h-1.5 rounded-full bg-muted overflow-hidden max-w-[120px]">
        <div className={cn('h-full transition-all', color)} style={{ width: `${clamped}%` }} />
      </div>
      <span className="text-xs tabular-nums text-muted-foreground">{Math.round(clamped)}%</span>
    </div>
  );
}

function QuotaPlansTable() {
  const router = useRouter();
  const { data: plans, isLoading } = useQuotaPlans();
  const { data: usage } = useQuotaUsage();

  // Group usage rows by plan so the list can render a per-plan count and
  // worst-utilization summary without an additional fetch per row.
  const usageByPlan = new Map<string, QuotaUsageRow[]>();
  for (const row of usage?.rows ?? []) {
    const list = usageByPlan.get(row.planName) ?? [];
    list.push(row);
    usageByPlan.set(row.planName, list);
  }

  const columns: Column<QuotaPlan>[] = [
    {
      key: 'name',
      header: 'Plan',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Gauge className="h-4 w-4 text-muted-foreground" />
          <div>
            <p className="font-medium text-foreground">{row.displayName}</p>
            <p className="text-2xs text-muted-foreground font-mono">{row.name}</p>
          </div>
        </div>
      ),
    },
    {
      key: 'enforcement',
      header: 'Enforcement',
      accessor: (row) => enforcementBadge(row.enforcement),
    },
    {
      key: 'inUse',
      header: 'In use',
      accessor: (row) => {
        const rows = usageByPlan.get(row.name) ?? [];
        return <span className="tabular-nums text-sm">{rows.length}</span>;
      },
      sortAccessor: (row) => (usageByPlan.get(row.name) ?? []).length,
      align: 'right',
    },
    {
      key: 'worst',
      header: 'Worst utilization',
      sortable: false,
      accessor: (row) => {
        const rows = usageByPlan.get(row.name) ?? [];
        if (rows.length === 0) {
          return <span className="text-xs text-muted-foreground">--</span>;
        }
        return <UtilizationBar pct={maxUtilization(rows)} />;
      },
    },
    {
      key: 'updatedAt',
      header: 'Updated',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">{formatRelativeTime(row.updatedAt)}</span>
      ),
    },
  ];

  return (
    <DataTable
      data={plans ?? []}
      columns={columns}
      keyExtractor={(row) => row.name}
      loading={isLoading}
      onRowClick={(row) => router.push(`/dashboard/settings/quotas/${encodeURIComponent(row.name)}`)}
      emptyMessage="No quota plans defined"
      searchPlaceholder="Search plans..."
    />
  );
}

function QuotasPage() {
  const router = useRouter();
  return (
    <SettingsAuthGate>
      <PageShell>
        <Link
          href="/dashboard/settings"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Settings
        </Link>
        <PageHeader
          eyebrow="Settings · Quotas"
          title="Quota plans"
          description="Per-tenant caps on projects, clusters, namespaces, storage, tokens, and more."
          actions={
            <>
              <ActionButton icon={<ExternalLink className="h-3.5 w-3.5" />} onClick={() => router.push('/dashboard/settings/quotas/usage')}>
                Deployment-wide usage
              </ActionButton>
              <ActionButton intent="primary" icon={<Plus className="h-4 w-4" />} onClick={() => router.push('/dashboard/settings/quotas/new')}>
                New plan
              </ActionButton>
            </>
          }
        />
        <QuotaPlansTable />
      </PageShell>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute('/dashboard/settings/quotas/')({
  component: QuotasPage,
});
