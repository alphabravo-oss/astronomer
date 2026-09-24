import { useMemo } from "react";
import { formatRelativeTime } from "@/lib/utils";
import type { GenericK8sResource, ClusterEvent, Pod } from "@/types";
import type { useNamespaceQueries } from "./namespace-queries";

export interface NamespaceResourceRow {
  id: string;
  kind: string;
  name: string;
  status: string;
  detail: string;
  age: string;
  createdAt: string;
  href?: string;
}

export const buildGenericNamespaceRows = (
  clusterId: string,
  namespace: string,
  resourceType: string,
  kind: string,
  items: GenericK8sResource[] | undefined,
): NamespaceResourceRow[] =>
  (items ?? [])
    .filter((item) => item.namespace === namespace)
    .map((item) => ({
      id: `${kind}/${item.name}`,
      kind,
      name: item.name,
      status: item.status || "Active",
      detail: "",
      createdAt: item.createdAt || "",
      age: item.createdAt ? formatRelativeTime(item.createdAt) : "—",
      href: `/dashboard/clusters/${clusterId}/${resourceType}/${namespace}/${item.name}`,
    }));

export function useNamespaceResourceRows(
  clusterId: string,
  namespace: string,
  queries: ReturnType<typeof useNamespaceQueries>,
  namespacePods: Pod[],
  namespaceEvents: ClusterEvent[],
) {
  const {
    workloads,
    services,
    ingresses,
    policies,
    pvcs,
    configMaps,
    secrets,
    quotas,
    limits,
    serviceAccounts,
    roles,
    roleBindings,
  } = queries;
  return useMemo<
    Record<
      | "workloads"
      | "pods"
      | "networking"
      | "storage"
      | "configuration"
      | "events"
      | "security",
      NamespaceResourceRow[]
    >
  >(
    () => ({
      workloads: (workloads.data?.data ?? []).map((item) => ({
        id: `${item.kind}/${item.name}`,
        kind: item.kind,
        name: item.name,
        status: item.status,
        detail: item.ready,
        createdAt: item.createdAt,
        age: item.age,
        href: `/dashboard/clusters/${clusterId}/workloads/${item.kind.toLowerCase()}s/${namespace}/${item.name}`,
      })),
      pods: namespacePods.map((pod) => ({
        id: `Pod/${pod.name}`,
        kind: "Pod",
        name: pod.name,
        status: pod.status,
        detail: `${pod.ready} ready · ${pod.restarts} restarts · ${pod.node || "unscheduled"}`,
        createdAt: pod.createdAt,
        age: pod.age,
        href: `/dashboard/clusters/${clusterId}/pods/${namespace}/${pod.name}`,
      })),
      networking: [
        ...(services.data?.data ?? [])
          .filter((item) => item.namespace === namespace)
          .map((item) => ({
            id: `Service/${item.name}`,
            kind: "Service",
            name: item.name,
            status: "Active",
            detail: `${item.type} · ${item.clusterIP || "no cluster IP"}`,
            createdAt: item.createdAt,
            age: formatRelativeTime(item.createdAt),
            href: `/dashboard/clusters/${clusterId}/services/${namespace}/${item.name}`,
          })),
        ...(ingresses.data?.data ?? [])
          .filter((item) => item.namespace === namespace)
          .map((item) => ({
            id: `Ingress/${item.name}`,
            kind: "Ingress",
            name: item.name,
            status: "Active",
            detail: item.hosts.join(", ") || "No hosts",
            createdAt: item.createdAt,
            age: formatRelativeTime(item.createdAt),
            href: `/dashboard/clusters/${clusterId}/ingresses/${namespace}/${item.name}`,
          })),
        ...(policies.data?.data ?? [])
          .filter((item) => item.namespace === namespace)
          .map((item) => ({
            id: `NetworkPolicy/${item.name}`,
            kind: "NetworkPolicy",
            name: item.name,
            status: "Active",
            detail: item.policyTypes.join(" + "),
            createdAt: item.createdAt,
            age: formatRelativeTime(item.createdAt),
            href: `/dashboard/clusters/${clusterId}/networkpolicies/${namespace}/${item.name}`,
          })),
      ],
      storage: (pvcs.data?.data ?? [])
        .filter((item) => item.namespace === namespace)
        .map((item) => ({
          id: `PVC/${item.name}`,
          kind: "PersistentVolumeClaim",
          name: item.name,
          status: item.status,
          detail: `${item.capacity || "unallocated"} · ${item.storageClass || "default class"}`,
          createdAt: item.createdAt,
          age: formatRelativeTime(item.createdAt),
          href: `/dashboard/clusters/${clusterId}/persistentvolumeclaims/${namespace}/${item.name}`,
        })),
      configuration: [
        ...buildGenericNamespaceRows(
          clusterId,
          namespace,
          "configmaps",
          "ConfigMap",
          configMaps.data?.data,
        ),
        ...buildGenericNamespaceRows(
          clusterId,
          namespace,
          "secrets",
          "Secret",
          secrets.data?.data,
        ),
        ...buildGenericNamespaceRows(
          clusterId,
          namespace,
          "resourcequotas",
          "ResourceQuota",
          quotas.data?.data,
        ),
        ...buildGenericNamespaceRows(
          clusterId,
          namespace,
          "limitranges",
          "LimitRange",
          limits.data?.data,
        ),
      ],
      events: namespaceEvents.map((event) => ({
        id: event.id,
        kind: event.involvedObject.kind,
        name: event.involvedObject.name,
        status: event.type,
        detail: `${event.reason}: ${event.message}`,
        createdAt: event.lastTimestamp,
        age: formatRelativeTime(event.lastTimestamp),
      })),
      security: [
        ...buildGenericNamespaceRows(
          clusterId,
          namespace,
          "serviceaccounts",
          "ServiceAccount",
          serviceAccounts.data?.data,
        ),
        ...buildGenericNamespaceRows(
          clusterId,
          namespace,
          "k8s-roles",
          "Role",
          roles.data?.data,
        ),
        ...buildGenericNamespaceRows(
          clusterId,
          namespace,
          "k8s-rolebindings",
          "RoleBinding",
          roleBindings.data?.data,
        ),
        ...(policies.data?.data ?? [])
          .filter((item) => item.namespace === namespace)
          .map((item) => ({
            id: `NetworkPolicy/${item.name}`,
            kind: "NetworkPolicy",
            name: item.name,
            status: "Active",
            detail: `${item.ingressRules} ingress · ${item.egressRules} egress rules`,
            createdAt: item.createdAt,
            age: formatRelativeTime(item.createdAt),
            href: `/dashboard/clusters/${clusterId}/networkpolicies/${namespace}/${item.name}`,
          })),
      ],
    }),
    [
      clusterId,
      configMaps.data,
      ingresses.data,
      limits.data,
      namespace,
      namespaceEvents,
      namespacePods,
      policies.data,
      pvcs.data,
      quotas.data,
      roleBindings.data,
      roles.data,
      secrets.data,
      serviceAccounts.data,
      services.data,
      workloads.data?.data,
    ],
  );
}
