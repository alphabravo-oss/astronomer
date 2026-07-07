'use client';

/**
 * Cluster detail → Gatekeeper constraints (P-04).
 *
 * Lists bundle + operator-authored constraints with their violation counts,
 * and offers a YAML authoring panel: Validate (kind/apiVersion + embedded Rego
 * for ConstraintTemplates, no apply) and Apply (validate + server-side apply
 * through the agent tunnel + persist). Delete removes an authored constraint
 * from both the cluster and the management store.
 *
 * Apply + Delete are RBAC-gated in the UI (clusters:update) to match the
 * server-side enforcement; the server independently fails closed + audits.
 */
import { useState } from 'react';
import { useParams } from '@/lib/navigation';
import { Link } from '@/lib/link';
import {
  ArrowLeft,
  ShieldCheck,
  Trash2,
  Loader2,
  CheckCircle2,
  XCircle,
  Play,
  Upload,
  Server,
} from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { useCluster } from '@/lib/hooks';
import { useClustersUpdate } from '@/lib/permission-hooks';
import { cn } from '@/lib/utils';
import type { GatekeeperConstraint, ConstraintValidateResult } from '@/types';
import {
  useGatekeeperConstraints,
  useValidateConstraint,
  useApplyConstraint,
  useDeleteConstraint,
} from './hooks';

const STARTER_YAML = `apiVersion: constraints.gatekeeper.sh/v1beta1
kind: K8sRequiredLabels
metadata:
  name: require-team-label
spec:
  enforcementAction: deny
  match:
    kinds:
      - apiGroups: [""]
        kinds: ["Namespace"]
  parameters:
    labels: ["team"]
`;

export default function ClusterGatekeeperPage() {
  const params = useParams();
  const clusterId = (params?.id as string) ?? '';
  const { canWrite, reason } = useClustersUpdate(clusterId);

  const { data: cluster, isLoading: clusterLoading } = useCluster(clusterId);
  const { data: constraints, isLoading, isError, refetch } = useGatekeeperConstraints(clusterId);

  const validate = useValidateConstraint(clusterId);
  const apply = useApplyConstraint(clusterId);
  const del = useDeleteConstraint(clusterId);

  const [yaml, setYaml] = useState(STARTER_YAML);
  const [result, setResult] = useState<ConstraintValidateResult | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<GatekeeperConstraint | null>(null);

  const handleValidate = async () => {
    setResult(null);
    try {
      setResult(await validate.mutateAsync(yaml));
    } catch {
      /* mutation toasts on error */
    }
  };

  const handleApply = async () => {
    if (!canWrite) return;
    setResult(null);
    try {
      const r = await apply.mutateAsync(yaml);
      setResult(r);
    } catch {
      /* mutation toasts on error */
    }
  };

  const columns: Column<GatekeeperConstraint>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.name}</p>
          <p className="text-2xs font-mono text-muted-foreground">{row.kind}</p>
        </div>
      ),
    },
    {
      key: 'source',
      header: 'Source',
      accessor: (row) => (
        <span
          className={cn(
            'text-xs px-2 py-0.5 rounded capitalize font-medium',
            row.source === 'custom'
              ? 'bg-status-info/10 text-status-info'
              : 'bg-muted text-muted-foreground',
          )}
        >
          {row.source}
        </span>
      ),
      sortAccessor: (row) => row.source,
    },
    {
      key: 'enforcement',
      header: 'Enforcement',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono">
          {row.enforcementAction || '—'}
        </span>
      ),
      sortAccessor: (row) => row.enforcementAction,
    },
    {
      key: 'violations',
      header: 'Violations',
      align: 'center',
      accessor: (row) => (
        <span
          className={cn(
            'tabular-nums text-sm font-medium',
            row.violationCount > 0 ? 'text-status-error' : 'text-muted-foreground',
          )}
        >
          {row.violationCount}
        </span>
      ),
      sortAccessor: (row) => row.violationCount,
    },
    {
      key: 'actions',
      header: '',
      sortable: false,
      accessor: (row) =>
        row.source === 'custom' ? (
          <button
            onClick={(e) => {
              e.stopPropagation();
              if (canWrite) setDeleteTarget(row);
            }}
            disabled={!canWrite}
            title={canWrite ? 'Delete constraint' : reason}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-muted-foreground"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        ) : null,
    },
  ];

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

  const busy = validate.isPending || apply.isPending;

  return (
    <div className="space-y-6">
      <Link
        href={`/dashboard/clusters/${clusterId}`}
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to cluster
      </Link>

      <div className="flex items-start gap-3">
        <div className="flex-shrink-0 w-10 h-10 rounded-lg bg-muted flex items-center justify-center">
          <ShieldCheck className="h-5 w-5 text-muted-foreground" />
        </div>
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Gatekeeper Constraints</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Bundle + custom OPA/Gatekeeper policy for {cluster.displayName}. Author a ConstraintTemplate
            or Constraint, validate it, then apply it through the agent.
          </p>
        </div>
      </div>

      {/* Authoring panel */}
      <div className="rounded-xl border border-border bg-card p-4 space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold text-foreground">Author constraint</h2>
          <div className="flex items-center gap-2">
            <button
              onClick={handleValidate}
              disabled={busy || !yaml.trim()}
              className="inline-flex items-center gap-2 h-9 px-3 rounded-lg border border-border text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
            >
              {validate.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
              Validate
            </button>
            <button
              onClick={handleApply}
              disabled={busy || !yaml.trim() || !canWrite}
              title={canWrite ? undefined : reason}
              className="inline-flex items-center gap-2 h-9 px-3 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
            >
              {apply.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Upload className="h-3.5 w-3.5" />}
              Apply
            </button>
          </div>
        </div>

        <textarea
          value={yaml}
          onChange={(e) => setYaml(e.target.value)}
          spellCheck={false}
          rows={16}
          aria-label="Constraint YAML"
          className="w-full px-3 py-2 rounded-md border border-border bg-background text-xs font-mono text-foreground focus:outline-none focus:ring-1 focus:ring-ring resize-y"
        />

        {result && (
          <div
            className={cn(
              'rounded-lg border p-3 text-sm',
              result.valid
                ? 'border-status-success/30 bg-status-success/10'
                : 'border-status-error/30 bg-status-error/10',
            )}
          >
            <div className="flex items-center gap-2 font-medium">
              {result.valid ? (
                <CheckCircle2 className="h-4 w-4 text-status-success" />
              ) : (
                <XCircle className="h-4 w-4 text-status-error" />
              )}
              <span className="text-foreground">
                {result.applied
                  ? `Applied ${result.kind} "${result.name}"`
                  : result.valid
                    ? `Valid ${result.kind || 'constraint'}${result.name ? ` "${result.name}"` : ''}`
                    : 'Validation failed'}
              </span>
            </div>
            {result.errors && result.errors.length > 0 && (
              <ul className="mt-2 space-y-1 text-xs text-status-error list-disc list-inside">
                {result.errors.map((err, i) => (
                  <li key={i} className="font-mono">
                    {err}
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}

        {!canWrite && (
          <p className="text-2xs text-muted-foreground">
            You can validate constraints, but applying requires cluster write access. {reason}
          </p>
        )}
      </div>

      {/* Constraint inventory */}
      <div className="space-y-3">
        <h2 className="text-sm font-semibold text-foreground">Active constraints</h2>
        <DataTable
          data={constraints ?? []}
          columns={columns}
          keyExtractor={(row) => `${row.kind}/${row.name}`}
          loading={isLoading}
          isError={isError}
          onRetry={() => refetch()}
          searchPlaceholder="Search constraints..."
          emptyMessage="No Gatekeeper constraints found on this cluster"
        />
      </div>

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return;
          await del.mutateAsync(deleteTarget.name);
          setDeleteTarget(null);
        }}
        title="Delete constraint?"
        description={`This removes "${deleteTarget?.name}" from the cluster and the management store. This cannot be undone.`}
        confirmText="Delete"
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={del.isPending}
      />
    </div>
  );
}
