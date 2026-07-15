import { createFileRoute } from '@tanstack/react-router';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * Cluster Templates · Detail.
 *
 * Read-only summary of the template + a list of clusters bound to it (one
 * row per cluster with its apply status). Edit jumps to `./edit` where the
 * full form is re-rendered.
 */
import { Link } from '@/lib/link';
import { useParams } from '@/lib/navigation';
import { ArrowLeft, PencilLine, Layers } from 'lucide-react';
import { ErrorState, LoadingState, PermissionState } from '@/components/ui/empty-state';
import { useCurrentUser } from '@/lib/hooks';
import {
  useClusterTemplate,
  useClusterTemplateBoundClusters,
  canReadClusterTemplates,
  canWriteClusterTemplates,
} from '@/components/projects/hooks';
import { formatRelativeTime, cn } from '@/lib/utils';
import type { ClusterTemplateBoundCluster } from '@/lib/api/project-detail';

const statusStyles: Record<ClusterTemplateBoundCluster['status'], string> = {
  pending: 'bg-status-warning/10 text-status-warning',
  applied: 'bg-status-success/10 text-status-success',
  drift: 'bg-status-warning/20 text-status-warning',
  failed: 'bg-status-error/10 text-status-error',
};

function ClusterTemplateDetailPage() {
  const params = useParams();
  const id = params.id as string;
  const { data: user } = useCurrentUser();
  const canRead = canReadClusterTemplates(user);
  const canWrite = canWriteClusterTemplates(user);

  const { data: template, isLoading } = useClusterTemplate(id);
  const { data: bound = [] } = useClusterTemplateBoundClusters(canRead ? id : undefined);

  if (!canRead) {
    return (
      <div className="space-y-4">
        <Link
          href="/dashboard/cluster-templates"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to bundles
        </Link>
        <PermissionState
          permission="cluster_templates:read"
          description={<>You need <span className="font-mono">cluster_templates:read</span> to view this bundle.</>}
          className="rounded-lg border border-border bg-muted/30 p-6"
        />
      </div>
    );
  }

  if (isLoading) {
    return <LoadingState title="Loading cluster template" className="h-32 py-0" />;
  }
  if (!template) {
    return <ErrorState title="Template not found" description="The requested cluster template does not exist or is no longer available." />;
  }

  return (
    <div className="max-w-4xl mx-auto space-y-6">
      <Link
        href="/dashboard/cluster-templates"
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to bundles
      </Link>

      <div className="flex items-start justify-between gap-4">
        <div>
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            Onboarding Bundle
          </p>
          <div className="flex items-center gap-2 mt-1">
            <Layers className="h-5 w-5 text-muted-foreground" />
            <h1 className="text-2xl font-semibold text-foreground tracking-tight">
              {template.displayName}
            </h1>
            <span className="text-xs text-muted-foreground font-mono">{template.name}</span>
          </div>
          {template.description && (
            <p className="text-sm text-muted-foreground mt-2 max-w-2xl">{template.description}</p>
          )}
        </div>
        {canWrite && (
          <Link
            href={`/dashboard/cluster-templates/${template.id}/edit`}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg border border-border text-sm font-medium hover:bg-accent transition-colors"
          >
            <PencilLine className="h-3.5 w-3.5" />
            Edit
          </Link>
        )}
      </div>

      {/* Summary */}
      <section className="rounded-xl border border-border bg-card p-5 space-y-3">
        <h2 className="text-sm font-medium text-foreground">Spec</h2>
        <dl className="grid grid-cols-1 md:grid-cols-2 gap-x-6 gap-y-2 text-sm">
          <DetailRow label="Environment" value={template.spec.environment} />
          <DetailRow
            label="Tools"
            value={
              template.spec.tools.length === 0
                ? '—'
                : template.spec.tools.map((t) => `${t.slug}${t.preset ? `:${t.preset}` : ''}`).join(', ')
            }
          />
          <DetailRow
            label="Labels"
            value={
              template.spec.labels.length === 0
                ? '—'
                : template.spec.labels.map((l) => `${l.key}=${l.value}`).join(', ')
            }
          />
          <DetailRow label="Default PSA" value={template.spec.defaultProject.podSecurityProfile} />
          <DetailRow
            label="Default netpol"
            value={template.spec.defaultProject.networkPolicyMode}
          />
          <DetailRow
            label="Default CPU quota"
            value={template.spec.defaultProject.resourceQuotaCpu ?? 'unlimited'}
          />
          <DetailRow
            label="Default memory quota"
            value={template.spec.defaultProject.resourceQuotaMemory ?? 'unlimited'}
          />
          <DetailRow
            label="Default pod quota"
            value={
              template.spec.defaultProject.resourceQuotaPods != null
                ? String(template.spec.defaultProject.resourceQuotaPods)
                : 'unlimited'
            }
          />
          <DetailRow
            label="Token rotation"
            value={`${template.spec.registrationPolicy.tokenRotationDays} days`}
          />
          <DetailRow
            label="Approval required"
            value={template.spec.registrationPolicy.requireApproval ? 'yes' : 'no'}
          />
        </dl>
        <p className="text-xs text-muted-foreground pt-2 border-t border-border">
          Created {formatRelativeTime(template.createdAt)}
          {template.createdBy ? ` by ${template.createdBy}` : ''} · Updated{' '}
          {formatRelativeTime(template.updatedAt)}
        </p>
      </section>

      {/* Bound clusters */}
      <section className="rounded-xl border border-border bg-card overflow-hidden">
        <div className="px-5 py-3 border-b border-border">
          <h2 className="text-sm font-medium text-foreground">Bound clusters</h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Registered clusters this bundle has been applied to.
          </p>
        </div>
        <Table className="w-full text-sm">
          <TableHeader>
            <TableRow className="text-xs text-muted-foreground border-b border-border bg-muted/30">
              <TableHead className="text-left font-medium py-2 px-4">Cluster</TableHead>
              <TableHead className="text-left font-medium py-2 px-4">Status</TableHead>
              <TableHead className="text-left font-medium py-2 px-4">Last applied</TableHead>
              <TableHead className="text-left font-medium py-2 px-4">Detail</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {bound.length === 0 ? (
              <TableRow>
                <TableCell colSpan={4} className="py-6 text-center text-xs text-muted-foreground">
                  No clusters bound yet.
                </TableCell>
              </TableRow>
            ) : (
              bound.map((row) => (
                <TableRow key={row.clusterId} className="border-b border-border last:border-0">
                  <TableCell className="py-2 px-4">
                    <Link
                      href={`/dashboard/clusters/${row.clusterId}`}
                      className="text-foreground hover:underline underline-offset-2"
                    >
                      {row.clusterName}
                    </Link>
                  </TableCell>
                  <TableCell className="py-2 px-4">
                    <span
                      className={cn(
                        'inline-flex px-2 py-0.5 rounded text-xs font-medium capitalize',
                        statusStyles[row.status] ?? 'bg-muted text-muted-foreground',
                      )}
                    >
                      {row.status}
                    </span>
                  </TableCell>
                  <TableCell className="py-2 px-4 text-xs text-muted-foreground">
                    {row.lastAppliedAt ? formatRelativeTime(row.lastAppliedAt) : '—'}
                  </TableCell>
                  <TableCell className="py-2 px-4 text-xs text-muted-foreground truncate max-w-[260px]">
                    {row.message || '—'}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </section>
    </div>
  );
}

function DetailRow({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="text-foreground font-mono text-xs break-all">{value}</dd>
    </>
  );
}

export const Route = createFileRoute('/dashboard/cluster-templates/$id/')({
  component: ClusterTemplateDetailPage,
});
