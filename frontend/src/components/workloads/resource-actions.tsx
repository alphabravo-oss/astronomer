'use client';

import { useState } from 'react';
import { Scaling, RotateCw, Trash2, Pause, Play, Zap, Download } from 'lucide-react';
import * as apiClient from '@/lib/api';
import { toastApiError } from '@/lib/toast';
import { ScaleDialog } from '@/components/workloads/scale-dialog';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { useScaleWorkload, useRestartWorkload, useK8sDelete, useK8sPatch, useK8sCreate } from '@/lib/hooks';
import { useClusterResourcePermission } from '@/lib/permission-hooks';
import {
  k8sResourcePath,
  k8sListPath,
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
  /** CronJob spec.jobTemplate. Pass to show "Run Now" (creates a Job from it). */
  jobTemplate?: Record<string, unknown>;
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
  clusterId, kind, namespace, name, replicas, paused, suspended, jobTemplate, k8sPath, permissionResource, onDeleted,
}: ResourceActionsProps) {
  const resourceType = kindToResourceType(kind);
  const path = k8sPath ?? k8sResourcePath(resourceType, name, namespace);

  const scalePerm = useClusterResourcePermission(clusterId, resourceType, 'scale', permissionResource);
  const restartPerm = useClusterResourcePermission(clusterId, resourceType, 'restart', permissionResource);
  const updatePerm = useClusterResourcePermission(clusterId, resourceType, 'update', permissionResource);
  const deletePerm = useClusterResourcePermission(clusterId, resourceType, 'delete', permissionResource);
  // "Run Now" creates a Job, so it needs jobs:create (canonically workloads:create).
  const triggerPerm = useClusterResourcePermission(clusterId, 'jobs', 'create');

  const scaleWorkload = useScaleWorkload();
  const restartWorkload = useRestartWorkload();
  const patch = useK8sPatch();
  const k8sDelete = useK8sDelete();
  const k8sCreate = useK8sCreate();

  const [showScale, setShowScale] = useState(false);
  const [showDelete, setShowDelete] = useState(false);

  // Download the object's live YAML (Rancher-universal action). Fetched on
  // demand so it isn't pulled for every detail view.
  const downloadYaml = async () => {
    try {
      const yaml = await apiClient.k8sGetYaml(clusterId, path);
      const url = URL.createObjectURL(new Blob([yaml], { type: 'text/yaml' }));
      const a = document.createElement('a');
      a.href = url;
      a.download = `${name}.yaml`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      toastApiError('Failed to download YAML', e);
    }
  };

  // Trigger a CronJob now: create a Job from its jobTemplate, mirroring
  // `kubectl create job --from=cronjob` (generateName + the manual-instantiate
  // annotation). Pure k8s-proxy POST, no backend endpoint needed.
  const runNow = () =>
    k8sCreate.mutate({
      clusterId,
      path: k8sListPath('jobs', namespace),
      body: {
        apiVersion: 'batch/v1',
        kind: 'Job',
        metadata: {
          generateName: `${name}-manual-`,
          namespace,
          annotations: { 'cronjob.kubernetes.io/instantiate': 'manual' },
        },
        spec: (jobTemplate as { spec?: unknown })?.spec,
      },
    });

  return (
    <div className="flex flex-wrap items-center gap-2">
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
      {kind === 'CronJob' && jobTemplate && (
        <button type="button" className={BTN} disabled={!triggerPerm.allowed || k8sCreate.isPending}
          title={denied(triggerPerm)} onClick={runNow}>
          <Zap className="h-3.5 w-3.5" /> Run Now
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
      <button type="button" className={BTN} onClick={downloadYaml} title="Download YAML">
        <Download className="h-3.5 w-3.5" /> YAML
      </button>
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
