'use client';

import { useState } from 'react';
import { Scaling, RotateCw, Trash2, Pause, Play } from 'lucide-react';
import { ScaleDialog } from '@/components/workloads/scale-dialog';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { useScaleWorkload, useRestartWorkload, useK8sDelete, useK8sPatch } from '@/lib/hooks';
import { useClusterResourcePermission } from '@/lib/permission-hooks';
import {
  k8sResourcePath,
  kindToResourceType,
  WORKLOAD_SCALABLE_KINDS,
  WORKLOAD_RESTARTABLE_KINDS,
} from '@/lib/k8s-paths';
import { cn } from '@/lib/utils';

interface ResourceActionsProps {
  clusterId: string;
  kind: string; // PascalCase K8s Kind, e.g. "Deployment"
  namespace?: string; // omitted for cluster-scoped kinds (Node)
  name: string;
  replicas?: number; // current replicas (scale-dialog default)
  /** Deployment rollout paused (spec.paused). Pass to show Pause/Resume. */
  paused?: boolean;
  /** CronJob suspended (spec.suspend). Pass to show Suspend/Resume. */
  suspended?: boolean;
  /** Exact object path; preferred for delete/patch (correct for CRs too). */
  k8sPath?: string;
  /** RBAC resource override when it differs from the kind (custom resources). */
  permissionResource?: string;
  onDeleted?: () => void; // e.g. navigate back to the list after delete
}

const BTN =
  'inline-flex items-center gap-1.5 h-8 px-3 rounded-md border border-border bg-background text-sm font-medium text-foreground transition-colors hover:bg-accent disabled:opacity-50 disabled:cursor-not-allowed';

const denied = (p: { allowed: boolean; disabledReason?: string; reason?: string }) =>
  p.allowed ? undefined : p.disabledReason || p.reason;

/**
 * Resource-management toolbar for a single object: Scale/Restart (workloads),
 * Pause/Resume (Deployment), Suspend/Resume (CronJob), and Delete (everything).
 * Reuses the same hooks + dialogs the list page wires; each control is gated on
 * the canonical RBAC verb and disabled (with reason) when not permitted — the
 * server stays the real gate.
 */
export function ResourceActions({
  clusterId, kind, namespace, name, replicas, paused, suspended, k8sPath, permissionResource, onDeleted,
}: ResourceActionsProps) {
  const resourceType = kindToResourceType(kind);
  const path = k8sPath ?? k8sResourcePath(resourceType, name, namespace);

  const scalePerm = useClusterResourcePermission(clusterId, resourceType, 'scale', permissionResource);
  const restartPerm = useClusterResourcePermission(clusterId, resourceType, 'restart', permissionResource);
  const updatePerm = useClusterResourcePermission(clusterId, resourceType, 'update', permissionResource);
  const deletePerm = useClusterResourcePermission(clusterId, resourceType, 'delete', permissionResource);

  const scaleWorkload = useScaleWorkload();
  const restartWorkload = useRestartWorkload();
  const patch = useK8sPatch();
  const k8sDelete = useK8sDelete();

  const [showScale, setShowScale] = useState(false);
  const [showDelete, setShowDelete] = useState(false);

  return (
    <div className="flex items-center gap-2">
      {WORKLOAD_SCALABLE_KINDS.includes(kind) && (
        <button type="button" className={BTN} disabled={!scalePerm.allowed} title={denied(scalePerm)}
          onClick={() => setShowScale(true)}>
          <Scaling className="h-3.5 w-3.5" /> Scale
        </button>
      )}
      {WORKLOAD_RESTARTABLE_KINDS.includes(kind) && (
        <button type="button" className={BTN} disabled={!restartPerm.allowed || restartWorkload.isPending}
          title={denied(restartPerm)}
          onClick={() => restartWorkload.mutate({ clusterId, kind, namespace: namespace ?? '', name })}>
          <RotateCw className={cn('h-3.5 w-3.5', restartWorkload.isPending && 'animate-spin')} /> Restart
        </button>
      )}
      {kind === 'Deployment' && paused !== undefined && (
        <button type="button" className={BTN} disabled={!updatePerm.allowed || patch.isPending}
          title={denied(updatePerm)}
          onClick={() => patch.mutate({ clusterId, path, body: { spec: { paused: !paused } } })}>
          {paused ? <Play className="h-3.5 w-3.5" /> : <Pause className="h-3.5 w-3.5" />}
          {paused ? 'Resume' : 'Pause'}
        </button>
      )}
      {kind === 'CronJob' && suspended !== undefined && (
        <button type="button" className={BTN} disabled={!updatePerm.allowed || patch.isPending}
          title={denied(updatePerm)}
          onClick={() => patch.mutate({ clusterId, path, body: { spec: { suspend: !suspended } } })}>
          {suspended ? <Play className="h-3.5 w-3.5" /> : <Pause className="h-3.5 w-3.5" />}
          {suspended ? 'Resume' : 'Suspend'}
        </button>
      )}
      <button type="button"
        className={cn(BTN, 'border-status-error/30 text-status-error hover:bg-status-error/10')}
        disabled={!deletePerm.allowed} title={denied(deletePerm)}
        onClick={() => setShowDelete(true)}>
        <Trash2 className="h-3.5 w-3.5" /> Delete
      </button>

      <ScaleDialog
        open={showScale}
        onClose={() => setShowScale(false)}
        onScale={(r) =>
          scaleWorkload.mutate(
            { clusterId, kind, namespace: namespace ?? '', name, replicas: r },
            { onSuccess: () => setShowScale(false) },
          )
        }
        workloadName={name}
        currentReplicas={replicas ?? 0}
        loading={scaleWorkload.isPending}
      />

      <ConfirmDialog
        open={showDelete}
        onClose={() => setShowDelete(false)}
        onConfirm={() =>
          k8sDelete.mutate(
            { clusterId, path },
            { onSuccess: () => { setShowDelete(false); onDeleted?.(); } },
          )
        }
        title={`Delete ${kind}`}
        description={`This will permanently delete ${name}.`}
        confirmValue={name}
        variant="destructive"
      />
    </div>
  );
}
