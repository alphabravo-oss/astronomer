'use client';

import { useState } from 'react';
import { Scaling, RotateCw, Trash2 } from 'lucide-react';
import { ScaleDialog } from '@/components/workloads/scale-dialog';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { useScaleWorkload, useRestartWorkload, useK8sDelete } from '@/lib/hooks';
import { useClusterResourcePermission } from '@/lib/permission-hooks';
import {
  k8sResourcePath,
  kindToResourceType,
  WORKLOAD_SCALABLE_KINDS,
  WORKLOAD_RESTARTABLE_KINDS,
} from '@/lib/k8s-paths';
import { cn } from '@/lib/utils';

interface WorkloadActionsProps {
  clusterId: string;
  kind: string; // PascalCase K8s Kind, e.g. "Deployment"
  namespace: string;
  name: string;
  replicas: number; // current replicas (scale-dialog default)
  onDeleted?: () => void; // e.g. navigate back to the list after delete
}

const BTN =
  'inline-flex items-center gap-1.5 h-8 px-3 rounded-md border border-border bg-background text-sm font-medium text-foreground transition-colors hover:bg-accent disabled:opacity-50 disabled:cursor-not-allowed';

/**
 * Resource-management toolbar for a single workload (Scale / Restart / Delete),
 * reusing the same hooks + dialogs the list page wires. Each button is gated on
 * the canonical RBAC verb and disabled (with reason) when not permitted; the
 * server stays the real gate.
 */
export function WorkloadActions({ clusterId, kind, namespace, name, replicas, onDeleted }: WorkloadActionsProps) {
  const resourceType = kindToResourceType(kind);
  const scalePerm = useClusterResourcePermission(clusterId, resourceType, 'scale');
  const restartPerm = useClusterResourcePermission(clusterId, resourceType, 'restart');
  const deletePerm = useClusterResourcePermission(clusterId, resourceType, 'delete');

  const scaleWorkload = useScaleWorkload();
  const restartWorkload = useRestartWorkload();
  const k8sDelete = useK8sDelete();

  const [showScale, setShowScale] = useState(false);
  const [showDelete, setShowDelete] = useState(false);

  return (
    <div className="flex items-center gap-2">
      {WORKLOAD_SCALABLE_KINDS.includes(kind) && (
        <button type="button" className={BTN} disabled={!scalePerm.allowed}
          title={scalePerm.allowed ? undefined : scalePerm.disabledReason || scalePerm.reason}
          onClick={() => setShowScale(true)}>
          <Scaling className="h-3.5 w-3.5" /> Scale
        </button>
      )}
      {WORKLOAD_RESTARTABLE_KINDS.includes(kind) && (
        <button type="button" className={BTN} disabled={!restartPerm.allowed || restartWorkload.isPending}
          title={restartPerm.allowed ? undefined : restartPerm.disabledReason || restartPerm.reason}
          onClick={() => restartWorkload.mutate({ clusterId, kind, namespace, name })}>
          <RotateCw className={cn('h-3.5 w-3.5', restartWorkload.isPending && 'animate-spin')} /> Restart
        </button>
      )}
      <button type="button"
        className={cn(BTN, 'border-status-error/30 text-status-error hover:bg-status-error/10')}
        disabled={!deletePerm.allowed}
        title={deletePerm.allowed ? undefined : deletePerm.disabledReason || deletePerm.reason}
        onClick={() => setShowDelete(true)}>
        <Trash2 className="h-3.5 w-3.5" /> Delete
      </button>

      <ScaleDialog
        open={showScale}
        onClose={() => setShowScale(false)}
        onScale={(r) =>
          scaleWorkload.mutate(
            { clusterId, kind, namespace, name, replicas: r },
            { onSuccess: () => setShowScale(false) },
          )
        }
        workloadName={name}
        currentReplicas={replicas}
        loading={scaleWorkload.isPending}
      />

      <ConfirmDialog
        open={showDelete}
        onClose={() => setShowDelete(false)}
        onConfirm={() =>
          k8sDelete.mutate(
            { clusterId, path: k8sResourcePath(resourceType, name, namespace) },
            { onSuccess: () => { setShowDelete(false); onDeleted?.(); } },
          )
        }
        title={`Delete ${kind}`}
        description={`This will permanently delete ${name}. Managed pods will also be terminated.`}
        confirmValue={name}
        variant="destructive"
      />
    </div>
  );
}
