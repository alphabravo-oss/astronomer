import { createFileRoute } from '@tanstack/react-router';
/**
 * /dashboard/settings/quotas/usage — fleet-wide quota usage view.
 *
 * Two parts:
 *   - "Top offenders" — entities at >80% of any cap, surfaced by the
 *     backend's pre-computed list (we don't re-derive client-side, as the
 *     backend already applies the threshold consistently with the same
 *     formula used for alerting).
 *   - Fleet totals — aggregate sums across the entire deployment, useful
 *     for capacity planning.
 */
import { Link } from '@/lib/link';
import {
  ArrowLeft,
  Gauge,
} from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { ErrorState, LoadingState } from '@/components/ui/empty-state';
import { cn } from '@/lib/utils';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { useQuotaUsage } from '@/components/settings/hooks';
import type { QuotaUsageRow } from '@/lib/api/settings';

function worstField(row: QuotaUsageRow): { field: string; pct: number } | null {
  let worst: { field: string; pct: number } | null = null;
  for (const [field, pct] of Object.entries(row.utilization ?? {})) {
    if (!worst || pct > worst.pct) worst = { field, pct };
  }
  return worst;
}

function UtilizationBar({ pct }: { pct: number }) {
  const clamped = Math.max(0, Math.min(100, pct));
  const color =
    clamped >= 95 ? 'bg-rose-500' : clamped >= 80 ? 'bg-amber-500' : 'bg-emerald-500';
  return (
    <div className="flex items-center gap-2">
      <div className="flex-1 h-1.5 rounded-full bg-muted overflow-hidden max-w-[160px]">
        <div className={cn('h-full transition-all', color)} style={{ width: `${clamped}%` }} />
      </div>
      <span className="text-xs tabular-nums text-muted-foreground">{Math.round(clamped)}%</span>
    </div>
  );
}

const FIELD_LABELS: Record<string, string> = {
  max_projects: 'Projects',
  max_clusters: 'Clusters',
  max_namespaces: 'Namespaces',
  max_users: 'Users',
  max_storage_gb: 'Storage (GiB)',
  max_cpu_cores: 'CPU cores',
  max_memory_gb: 'Memory (GiB)',
  max_backups_per_day: 'Backups / day',
  max_api_tokens: 'API tokens',
};

function fieldLabel(key: string) {
  return FIELD_LABELS[key] ?? key;
}

function UsageInner() {
  const { data, isLoading, error } = useQuotaUsage();

  if (isLoading) {
    return <LoadingState title="Loading quota usage" description="Fetching current utilization across projects and clusters." className="h-48 py-0" />;
  }
  if (error || !data) {
    return (
      <ErrorState
        title="Failed to load quota usage"
        description="Refresh the page or retry after the settings API is reachable."
        className="rounded-xl border border-border bg-card p-6"
      />
    );
  }

  const offenderColumns: Column<QuotaUsageRow>[] = [
    {
      key: 'scope',
      header: 'Scope',
      accessor: (row) => (
        <div>
          <p className="text-sm font-medium text-foreground capitalize">{row.scope}</p>
          <p className="text-2xs text-muted-foreground font-mono">{row.scopeName ?? row.scopeId ?? '--'}</p>
        </div>
      ),
    },
    {
      key: 'planName',
      header: 'Plan',
      accessor: (row) => (
        <Link
          href={`/dashboard/settings/quotas/${encodeURIComponent(row.planName)}`}
          className="text-sm text-foreground hover:underline font-mono"
        >
          {row.planName}
        </Link>
      ),
    },
    {
      key: 'worst',
      header: 'Worst cap',
      sortable: false,
      accessor: (row) => {
        const worst = worstField(row);
        if (!worst) return <span className="text-xs text-muted-foreground">--</span>;
        return (
          <div className="space-y-1 max-w-[260px]">
            <p className="text-xs text-muted-foreground">{fieldLabel(worst.field)}</p>
            <UtilizationBar pct={worst.pct} />
          </div>
        );
      },
    },
  ];

  return (
    <div className="space-y-6">
      <div className="rounded-xl border border-border bg-card p-6 space-y-4">
        <div>
          <h2 className="text-base font-semibold text-foreground">Top offenders</h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Tenants currently above 80% of any cap. Click a plan to adjust limits.
          </p>
        </div>
        {data.topOffenders.length === 0 ? (
          <p className="text-sm text-muted-foreground italic">No tenants currently above the 80% threshold.</p>
        ) : (
          <DataTable
            data={data.topOffenders}
            columns={offenderColumns}
            keyExtractor={(row) => `${row.planName}-${row.scopeId ?? row.scopeName ?? 'global'}`}
            emptyMessage="No offenders"
            searchable={false}
          />
        )}
      </div>

      <div className="rounded-xl border border-border bg-card p-6 space-y-4">
        <div>
          <h2 className="text-base font-semibold text-foreground">Fleet totals</h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Aggregate consumption across every tenant. Useful for capacity planning.
          </p>
        </div>
        <div className="grid grid-cols-2 sm:grid-cols-3 gap-3">
          {Object.entries(data.fleetTotals).map(([field, total]) => (
            <div key={field} className="rounded-lg border border-border bg-background p-3">
              <p className="text-xs text-muted-foreground">{fieldLabel(field)}</p>
              <p className="text-xl font-semibold text-foreground tabular-nums mt-1">
                {total.toLocaleString()}
              </p>
            </div>
          ))}
          {Object.keys(data.fleetTotals).length === 0 && (
            <p className="text-sm text-muted-foreground italic">No fleet data yet.</p>
          )}
        </div>
      </div>
    </div>
  );
}

function QuotaUsagePage() {
  return (
    <SettingsAuthGate>
      <div className="max-w-4xl mx-auto space-y-6">
        <Link
          href="/dashboard/settings/quotas"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to quotas
        </Link>
        <div>
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Settings · Quota usage</p>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1 flex items-center gap-2">
            <Gauge className="h-5 w-5 text-muted-foreground" />
            Fleet quota usage
          </h1>
        </div>
        <UsageInner />
      </div>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute('/dashboard/settings/quotas/usage/')({
  component: QuotaUsagePage,
});
