'use client';

/**
 * Project detail Overview tab — the bare /projects/[id] route.
 *
 * Surfaces the project's clusters, namespaces, and members in read-only
 * cards. The new Policy / Cloud Credentials / Quota tabs (siblings under
 * the same `[id]/layout.tsx`) handle the editable surfaces.
 */
import { use } from 'react';
import Link from 'next/link';
import { Loader2, Users, Server, Layers } from 'lucide-react';
import { useProject } from '@/lib/hooks';
import { formatRelativeTime } from '@/lib/utils';
import { WidgetGrid } from '@/components/dashboards/widget-grid';
import { renderForProject } from '@/lib/api/dashboards';

interface OverviewPageProps {
  params: Promise<{ id: string }>;
}

export default function ProjectOverviewPage({ params }: OverviewPageProps) {
  const { id } = use(params);
  const { data: project, isLoading } = useProject(id);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-32">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (!project) {
    return <p className="text-sm text-muted-foreground">Project not found.</p>;
  }

  return (
    <div className="space-y-6">
      {/* Custom dashboard widgets (migration 058). Per-project scope —
          empty by default so the project overview stays clean unless
          the operator explicitly pins something here. */}
      <section className="space-y-2">
        <h3 className="text-sm font-medium text-muted-foreground uppercase tracking-wide">Widgets</h3>
        <WidgetGrid fetcher={() => renderForProject(project.id)} emptyHint="" />
      </section>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <SummaryCard icon={Server} label="Clusters" value={(project.clusterIds?.length ?? (project.clusterId ? 1 : 0)) || 1} />
        <SummaryCard icon={Layers} label="Namespaces" value={project.namespaces.length} />
        <SummaryCard icon={Users} label="Members" value={project.members.length} />

        <div className="md:col-span-3 rounded-xl border border-border bg-card p-5 space-y-2">
          <h3 className="text-sm font-medium text-foreground">Identifiers</h3>
          <dl className="grid grid-cols-2 gap-x-6 gap-y-1.5 text-sm">
            <dt className="text-muted-foreground">Name</dt>
            <dd className="font-mono text-xs text-foreground">{project.name}</dd>
            <dt className="text-muted-foreground">Project ID</dt>
            <dd className="font-mono text-xs text-foreground">{project.id}</dd>
            <dt className="text-muted-foreground">Created</dt>
            <dd className="text-foreground">{formatRelativeTime(project.createdAt)}</dd>
            <dt className="text-muted-foreground">Updated</dt>
            <dd className="text-foreground">{formatRelativeTime(project.updatedAt)}</dd>
          </dl>
          <p className="text-xs text-muted-foreground pt-2">
            Configure pod security and resource limits on the{' '}
            <Link
              href={`/dashboard/projects/${project.id}/policy`}
              className="text-foreground underline-offset-2 hover:underline"
            >
              Policy tab
            </Link>
            .
          </p>
        </div>
      </div>
    </div>
  );
}

function SummaryCard({
  icon: Icon,
  label,
  value,
}: {
  icon: React.ElementType;
  label: string;
  value: number;
}) {
  return (
    <div className="rounded-xl border border-border bg-card p-5">
      <div className="flex items-center gap-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
        <Icon className="h-3.5 w-3.5" />
        {label}
      </div>
      <p className="mt-2 text-2xl font-semibold tabular-nums text-foreground">{value}</p>
    </div>
  );
}
