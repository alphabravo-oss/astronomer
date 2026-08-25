import { useMemo } from "react";
import {
  canonicalPermissionResource,
  usePermissionDecision,
} from "@/lib/permission-hooks";
import type { PermissionDecision } from "@/lib/permissions";

export interface ResourcePermissionDecisions {
  create: PermissionDecision;
  read: PermissionDecision;
  update: PermissionDecision;
  delete: PermissionDecision;
  scale: PermissionDecision;
  restart: PermissionDecision;
  exec: PermissionDecision;
  logs: PermissionDecision;
  manage: PermissionDecision;
}

/** One policy projection shared by every resource adapter and action menu. */
export function useClusterResourcePermissions(
  clusterId: string,
  resourceType: string,
): ResourcePermissionDecisions {
  const permissionResource = canonicalPermissionResource(resourceType);
  const scope = useMemo(
    () => ({ type: "cluster" as const, id: clusterId }),
    [clusterId],
  );

  return {
    create: usePermissionDecision(permissionResource, "create", scope),
    read: usePermissionDecision(permissionResource, "read", scope),
    update: usePermissionDecision(permissionResource, "update", scope),
    delete: usePermissionDecision(permissionResource, "delete", scope),
    scale: usePermissionDecision(permissionResource, "scale", scope),
    restart: usePermissionDecision(permissionResource, "restart", scope),
    exec: usePermissionDecision(permissionResource, "exec", scope),
    logs: usePermissionDecision(permissionResource, "logs", scope),
    manage: usePermissionDecision(permissionResource, "manage", scope),
  };
}

export function firstDeniedDecision(
  ...decisions: PermissionDecision[]
): PermissionDecision | undefined {
  return decisions.find((decision) => !decision.allowed);
}

const deletableGenericTypes = new Set([
  "jobs",
  "cronjobs",
  "configmaps",
  "secrets",
  "hpa",
  "resourcequotas",
  "limitranges",
  "poddisruptionbudgets",
  "serviceaccounts",
  "k8s-roles",
  "k8s-rolebindings",
  "endpoints",
  "replicasets",
]);

const editableGenericTypes = new Set([
  "configmaps",
  "secrets",
  "jobs",
  "cronjobs",
  "hpa",
  "resourcequotas",
  "limitranges",
  "poddisruptionbudgets",
  "serviceaccounts",
  "k8s-roles",
  "k8s-rolebindings",
  "replicasets",
]);

export function genericResourceActionPolicy(resourceType: string) {
  return {
    deletable: deletableGenericTypes.has(resourceType),
    editable: editableGenericTypes.has(resourceType),
  } as const;
}
