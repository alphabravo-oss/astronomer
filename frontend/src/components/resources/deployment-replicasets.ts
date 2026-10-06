import type { GenericK8sResource } from "@/types/generic-kubernetes";

export const REVISION_ANNOTATION = "deployment.kubernetes.io/revision";
const TEMPLATE_HASH_LABEL = "pod-template-hash";

export interface DeploymentReplicaSet {
  name: string;
  revision: number | null;
  desired: number;
  ready: number;
  available: number;
  createdAt: string;
  /** Highest revision that still wants replicas: the rollout's live set. */
  current: boolean;
}

/**
 * ReplicaSets owned by a Deployment, newest revision first.
 *
 * The generic ReplicaSet row carries no owner references, so ownership comes
 * from Kubernetes' own naming: a Deployment names each ReplicaSet exactly
 * `<deployment>-<pod-template-hash>` and labels it with that hash, so an exact
 * match also keeps `web` from claiming the sets of a `web-admin` deployment.
 */
export function ownedReplicaSets(
  replicaSets: readonly GenericK8sResource[],
  deploymentName: string,
): DeploymentReplicaSet[] {
  const owned = replicaSets
    .filter((rs) => {
      const hash = rs.labels?.[TEMPLATE_HASH_LABEL];
      return !!hash && rs.name === `${deploymentName}-${hash}`;
    })
    .map((rs) => {
      const parsed = Number(rs.annotations?.[REVISION_ANNOTATION]);
      return {
        name: rs.name,
        revision: Number.isFinite(parsed) && parsed > 0 ? parsed : null,
        desired: rs.desired ?? 0,
        ready: rs.ready ?? 0,
        available: rs.available ?? 0,
        createdAt: rs.createdAt,
        current: false,
      };
    })
    .sort(
      (a, b) =>
        (b.revision ?? -1) - (a.revision ?? -1) ||
        b.createdAt.localeCompare(a.createdAt),
    );
  const live = owned.find((rs) => rs.desired > 0);
  if (live) live.current = true;
  return owned;
}
