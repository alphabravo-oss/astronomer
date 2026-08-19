import { createFileRoute } from '@tanstack/react-router';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * mTLS breakdown sub-page — per-namespace view backed by the
 * /api/v1/clusters/{id}/service-mesh/mtls/ endpoint (migration 071).
 *
 * The backend either returns full per-namespace rows (when the tunnel
 * fallback resolved PeerAuthentication CRs) or an aggregate-only
 * payload with a notice explaining why the per-namespace breakdown
 * isn't available. This page renders both shapes — rows + notice are
 * non-exclusive.
 */

import { Link } from '@/lib/link';
import { useParams } from '@/lib/navigation';
import { useQuery } from '@tanstack/react-query';
import { ChevronLeft, Info, Loader2, Server, Shield } from 'lucide-react';

import { queryKeys, useCluster } from '@/lib/hooks';
import { PageHeader, PageShell } from '@/components/ui/page';
import { getServiceMeshMTLS, type MTLSBreakdownRow } from '@/lib/api/cluster-detail';

// modeStyle picks a tailwind colour for a mode badge. STRICT is the
// strongest signal so we paint it green; UNSET / DISABLE stay muted.
function modeStyle(mode: string): string {
  switch (mode) {
    case 'STRICT':
      return 'bg-status-success/15 text-status-success border-status-success/30';
    case 'PERMISSIVE':
      return 'bg-status-warning/15 text-status-warning border-status-warning/30';
    case 'DISABLE':
      return 'bg-status-error/15 text-status-error border-status-error/30';
    default:
      return 'bg-muted text-muted-foreground border-border';
  }
}

function MTLSRow({ row }: { row: MTLSBreakdownRow }) {
  return (
    <TableRow className="border-t border-border">
      <TableCell className="px-4 py-2.5 font-mono text-sm text-foreground">{row.namespace}</TableCell>
      <TableCell className="px-4 py-2.5">
        <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium border ${modeStyle(row.mode)}`}>
          {row.mode}
        </span>
      </TableCell>
      <TableCell className="px-4 py-2.5 text-sm text-muted-foreground">{row.rules}</TableCell>
    </TableRow>
  );
}

function ClusterServiceMeshMTLSPage() {
  const params = useParams();
  const clusterId = params.id as string;
  const { data: cluster, isLoading: clusterLoading } = useCluster(clusterId);
  const { data: mtls, isLoading } = useQuery({
    queryKey: queryKeys.clusterPages.serviceMeshMtls(clusterId),
    queryFn: () => getServiceMeshMTLS(clusterId),
    enabled: !!clusterId,
  });

  if (clusterLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (!cluster) {
    return (
      <div className="flex flex-col items-center justify-center h-64 text-muted-foreground">
        <Server className="h-8 w-8 mb-3" />
        <p>Cluster not found</p>
      </div>
    );
  }

  return (
    <PageShell>
      <Link
        href={`/dashboard/clusters/${clusterId}/service-mesh/`}
        className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ChevronLeft className="h-3.5 w-3.5" />
        Back to service mesh
      </Link>
      <PageHeader
        title="mTLS posture"
        description={`Per-namespace PeerAuthentication / ServerAuthorization breakdown for ${cluster.displayName}.`}
      />

      {/* Aggregate summary */}
      {mtls && (
        <div className="rounded-lg border border-border bg-card p-4 flex items-center gap-4">
          <Shield className="h-6 w-6 text-status-success flex-shrink-0" />
          <div className="flex-1">
            <p className="text-sm font-medium text-foreground">
              {mtls.mtlsCoveragePct}% of user namespaces covered
            </p>
            <p className="text-xs text-muted-foreground mt-0.5">
              {mtls.totalCount} total rules across the cluster.
            </p>
          </div>
        </div>
      )}

      {/* Notice (scaffolding-only fallback) */}
      {mtls?.notice && (
        <div className="rounded-lg border border-status-warning/30 bg-status-warning/10 p-4 flex items-start gap-3">
          <Info className="h-5 w-5 text-status-warning flex-shrink-0 mt-0.5" />
          <p className="text-xs text-muted-foreground flex-1">{mtls.notice}</p>
        </div>
      )}

      {/* Table */}
      <div className="rounded-lg border border-border bg-card overflow-hidden">
        <Table className="w-full">
          <TableHeader>
            <TableRow className="border-b border-border">
              <TableHead className="text-left px-4 py-2.5 text-xs font-medium text-muted-foreground uppercase tracking-wide">
                Namespace
              </TableHead>
              <TableHead className="text-left px-4 py-2.5 text-xs font-medium text-muted-foreground uppercase tracking-wide">
                Mode
              </TableHead>
              <TableHead className="text-left px-4 py-2.5 text-xs font-medium text-muted-foreground uppercase tracking-wide">
                Rules
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {isLoading ? (
              <TableRow>
                <TableCell colSpan={3} className="px-4 py-12 text-center">
                  <Loader2 className="h-5 w-5 animate-spin text-muted-foreground inline" />
                </TableCell>
              </TableRow>
            ) : !mtls || mtls.rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={3} className="px-4 py-12 text-center text-sm text-muted-foreground">
                  {mtls?.notice ? 'No per-namespace breakdown available.' : 'No mTLS rules found.'}
                </TableCell>
              </TableRow>
            ) : (
              mtls.rows.map((row) => <MTLSRow key={`${row.namespace}-${row.mode}`} row={row} />)
            )}
          </TableBody>
        </Table>
      </div>
    </PageShell>
  );
}

export const Route = createFileRoute('/dashboard/clusters/$id/service-mesh/mtls/')({
  component: ClusterServiceMeshMTLSPage,
});
