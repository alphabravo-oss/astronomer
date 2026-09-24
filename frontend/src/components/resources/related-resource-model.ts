import type { ResourceDiscoveryView } from "@/lib/api/resources";
import { clusterDiscoveryFromDefinitions } from "@/components/layout/cluster-discovery-model";
import { crDetailHref, detailHref, k8sListPath } from "@/lib/k8s-paths";
import type { K8sObject } from "./resource-detail-model";

export interface OwnerReference {
  apiVersion?: string;
  kind: string;
  name: string;
  uid?: string;
}
export function ownerHref(
  clusterId: string,
  namespace: string | undefined,
  owner: OwnerReference,
  discovery: ResourceDiscoveryView,
): string | undefined {
  if (!owner.apiVersion || !owner.name || !owner.kind) return undefined;
  const parts = owner.apiVersion.split("/");
  const [group, version] = parts.length === 1 ? ["", parts[0]] : parts;
  const resource = discovery.resources.find(
    (entry) =>
      entry.kind === owner.kind &&
      (entry.apiGroup || "") === group &&
      entry.apiVersion === version,
  );
  if (resource && (!resource.namespaced || namespace))
    return detailHref(
      clusterId,
      resource.resourceType,
      resource.namespaced ? namespace : undefined,
      owner.name,
    );
  const custom = clusterDiscoveryFromDefinitions(discovery.crds)
    .crdsByGroup.get(group)
    ?.find(
      (entry) =>
        entry.kind === owner.kind && entry.servedVersions.includes(version),
    );
  if (custom && (!custom.namespaced || namespace))
    return crDetailHref(
      clusterId,
      group,
      version,
      custom.plural,
      owner.name,
      custom.namespaced ? namespace : undefined,
    );
  return undefined;
}

export function ownedBy(
  child: K8sObject,
  parent: { name: string; kind: string; uid?: string; apiVersion?: string },
): boolean {
  return !!child.metadata?.ownerReferences?.some(
    (owner) =>
      owner.kind === parent.kind &&
      owner.name === parent.name &&
      (!parent.uid || owner.uid === parent.uid) &&
      (!parent.apiVersion || owner.apiVersion === parent.apiVersion),
  );
}
export function serviceSelectsPod(
  service: K8sObject,
  pod: K8sObject | undefined,
): boolean {
  const selector = Object.entries(service.spec?.selector ?? {});
  return (
    selector.length > 0 &&
    selector.every(([key, value]) => pod?.metadata?.labels?.[key] === value)
  );
}
export function relatedPagePath(
  resourceType: string,
  namespace: string,
  token: string,
): string {
  const query = new URLSearchParams({ limit: "50" });
  if (token) query.set("continue", token);
  return `${k8sListPath(resourceType, namespace)}?${query}`;
}
export const CHILD_RESOURCE_TYPES: Record<string, string> = {
  Deployment: "replicasets",
  StatefulSet: "pods",
  DaemonSet: "pods",
  ReplicaSet: "pods",
  Job: "pods",
  CronJob: "jobs",
};
