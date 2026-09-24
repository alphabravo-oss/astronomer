import type { Dispatch, SetStateAction } from "react";
import type { Workload } from "@/types";
import { ScaleDialog } from "@/components/workloads/scale-dialog";
import { YamlViewDialog } from "@/components/ui/yaml-view-dialog";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { CreateResourceDialog } from "./create-resource-dialog";
import { useScaleWorkload } from "@/lib/hooks/workloads";
import { useK8sDelete } from "@/lib/hooks/kubernetes-proxy";
import { useClusterResourcePermissions } from "./resource-action-policy";
import { toastPermissionDenied } from "@/lib/permission-hooks";
import { kindToResourceType, k8sResourcePath } from "@/lib/k8s-paths";
import { resourceDeletionImpact } from "./resource-deletion-impact";
import { WORKLOAD_TEMPLATE_BY_KIND } from "./resource-route-config";
type Setter<T> = Dispatch<SetStateAction<T>>;
export function WorkloadTableDialogs({
  clusterId,
  kind,
  scaleTarget,
  setScaleTarget,
  yamlTarget,
  setYamlTarget,
  deleteTarget,
  setDeleteTarget,
  showCreate,
  setShowCreate,
}: {
  clusterId: string;
  kind: string;
  scaleTarget: Workload | null;
  setScaleTarget: Setter<Workload | null>;
  yamlTarget: { path: string; title: string } | null;
  setYamlTarget: Setter<{ path: string; title: string } | null>;
  deleteTarget: Workload | null;
  setDeleteTarget: Setter<Workload | null>;
  showCreate: boolean;
  setShowCreate: Setter<boolean>;
}) {
  const permissions = useClusterResourcePermissions(clusterId, "workloads");
  const scaleWorkload = useScaleWorkload();
  const k8sDeleteMut = useK8sDelete();
  return (
    <>
      <ScaleDialog
        open={!!scaleTarget}
        onClose={() => setScaleTarget(null)}
        onScale={(replicas) => {
          if (!permissions.scale.allowed) {
            toastPermissionDenied(permissions.scale);
            return;
          }
          if (scaleTarget) {
            scaleWorkload.mutate(
              {
                clusterId,
                kind: scaleTarget.kind,
                namespace: scaleTarget.namespace,
                name: scaleTarget.name,
                replicas,
              },
              { onSuccess: () => setScaleTarget(null) },
            );
          }
        }}
        workloadName={scaleTarget?.name || ""}
        currentReplicas={scaleTarget?.replicas || 0}
        loading={scaleWorkload.isPending}
      />

      {yamlTarget && (
        <YamlViewDialog
          open={!!yamlTarget}
          onClose={() => setYamlTarget(null)}
          clusterId={clusterId}
          k8sPath={yamlTarget.path}
          title={yamlTarget.title}
          allowEdit={permissions.update.allowed}
          forceConflictPermission={permissions.manage}
        />
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget) {
            const resType = kindToResourceType(deleteTarget.kind);
            k8sDeleteMut.mutate(
              {
                clusterId,
                path: k8sResourcePath(
                  resType,
                  deleteTarget.name,
                  deleteTarget.namespace,
                ),
              },
              { onSuccess: () => setDeleteTarget(null) },
            );
          }
        }}
        title={`Delete ${deleteTarget?.kind || "Workload"}`}
        description={`This will permanently delete ${deleteTarget?.name}. Managed pods will also be terminated.`}
        impact={resourceDeletionImpact(
          deleteTarget ? kindToResourceType(deleteTarget.kind) : kind,
          deleteTarget,
        )}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={k8sDeleteMut.isPending}
      />

      {WORKLOAD_TEMPLATE_BY_KIND[kind] && (
        <CreateResourceDialog
          open={showCreate}
          onClose={() => setShowCreate(false)}
          clusterId={clusterId}
          templateKey={WORKLOAD_TEMPLATE_BY_KIND[kind]}
          title={`Create ${kind}`}
        />
      )}
    </>
  );
}
