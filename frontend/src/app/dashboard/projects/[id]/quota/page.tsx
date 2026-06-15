'use client';

/**
 * Project · Quota tab — single-card view of the effective project quota.
 *
 * Distinct from the Policy tab's per-namespace ResourceQuota: this one is
 * the *project-level* plan that caps how many clusters / namespaces /
 * members the project can claim. Editing the plan is admin-only and lives
 * on /dashboard/settings/quotas/; we just deep-link there.
 */
import { use } from 'react';
import { Link } from '@/lib/link';
import { Loader2, Settings, Server, Layers, Users } from 'lucide-react';
import { useProjectEffectiveQuota } from '@/components/projects/hooks';
import { cn } from '@/lib/utils';

interface QuotaPageProps {
  params: Promise<{ id: string }>;
}

export default function ProjectQuotaPage({ params }: QuotaPageProps) {
  const { id } = use(params);
  const { data: quota, isLoading } = useProjectEffectiveQuota(id);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-32">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!quota) {
    return (
      <p className="text-sm text-muted-foreground">
        No quota plan applies to this project yet.
      </p>
    );
  }

  return (
    <div className="space-y-6 max-w-5xl">
      {/* Plan summary */}
      <section className="rounded-xl border border-border bg-card p-5">
        <div className="flex items-start justify-between gap-4">
          <div>
            <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
              Effective plan
            </p>
            <h2 className="text-lg font-semibold text-foreground mt-1">{quota.planName}</h2>
            <p className="text-xs text-muted-foreground mt-0.5">
              Enforcement:{' '}
              <span className={cn('font-mono', quota.enforcement === 'hard' ? 'text-status-error' : 'text-status-warning')}>
                {quota.enforcement}
              </span>
            </p>
          </div>
          <Link
            href="/dashboard/settings/quotas/"
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded-lg border border-border text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            <Settings className="h-3.5 w-3.5" />
            Manage plans
          </Link>
        </div>
      </section>

      {/* Stat tiles */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <UsageTile
          icon={Server}
          label="Clusters"
          used={quota.clustersUsed}
          limit={quota.clustersLimit}
        />
        <UsageTile
          icon={Layers}
          label="Namespaces"
          used={quota.namespacesUsed}
          limit={quota.namespacesLimit}
        />
        <UsageTile
          icon={Users}
          label="Members"
          used={quota.membersUsed}
          limit={quota.membersLimit}
        />
      </div>
    </div>
  );
}

function UsageTile({
  icon: Icon,
  label,
  used,
  limit,
}: {
  icon: React.ElementType;
  label: string;
  used: number;
  limit: number;
}) {
  // A zero or negative limit means unlimited — render a flat bar and skip
  // the warning colors entirely.
  const unlimited = limit <= 0;
  const ratio = unlimited ? 0 : Math.min(used / limit, 1);
  const pct = Math.round(ratio * 100);
  const barColor = unlimited
    ? 'bg-muted-foreground/40'
    : ratio > 0.9
      ? 'bg-status-error'
      : ratio > 0.75
        ? 'bg-status-warning'
        : 'bg-status-success';

  return (
    <div className="rounded-xl border border-border bg-card p-5 space-y-3">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
          <Icon className="h-3.5 w-3.5" />
          {label}
        </div>
        <span className="text-xs text-muted-foreground tabular-nums">
          {unlimited ? `${used} used` : `${used} / ${limit}`}
        </span>
      </div>
      <div className="h-2 rounded-full bg-muted overflow-hidden">
        <div
          className={cn('h-full rounded-full transition-all duration-300', barColor)}
          style={{ width: unlimited ? '100%' : `${pct}%` }}
        />
      </div>
      <p className="text-xs text-muted-foreground">
        {unlimited ? 'Unlimited' : `${pct}% used`}
      </p>
    </div>
  );
}
