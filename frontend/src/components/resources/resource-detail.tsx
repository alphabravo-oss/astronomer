import { useMemo, useState } from "react";
import { useNavigate } from "@tanstack/react-router";

import {
  ResourceDetailTabPanel,
  type ResourceDetailTabId,
} from "@/components/resources/resource-detail-tabs";
import type { K8sObject } from "@/components/resources/resource-detail-model";
import { supportsRolloutHistory } from "@/components/resources/rollout-history";
import { PermissionState } from "@/components/ui/empty-state";
import { ResourceMasthead } from "@/components/ui/page";
import { StatusBadge } from "@/components/ui/status-badge";
import { TabStrip } from "@/components/ui/tabs";
import { ResourceActions } from "@/components/workloads/resource-actions";
import { useK8sResource } from "@/lib/hooks/kubernetes-proxy";
import { useClusterResourcePermission } from "@/lib/permission-hooks";
import { formatRelativeTime } from "@/lib/utils";

export { ResourceOverview } from "@/components/resources/resource-overview";

interface ResourceDetailProps {
  clusterId: string;
  resourceType: string;
  namespace?: string;
  name: string;
  /** K8s API path to the single object. */
  k8sPath: string;
  /** Override for resources whose canonical RBAC name differs from the route. */
  permissionResource?: string;
}

const BASE_TABS = [
  { id: "overview", label: "Overview" },
  { id: "yaml", label: "YAML" },
  { id: "events", label: "Events" },
  { id: "related", label: "Related" },
] as const;

const WORKLOAD_RESOURCE_TYPES = new Set([
  "deployments",
  "statefulsets",
  "daemonsets",
  "replicasets",
  "jobs",
  "cronjobs",
]);

export function ResourceDetail({
  clusterId,
  resourceType,
  namespace,
  name,
  k8sPath,
  permissionResource,
}: ResourceDetailProps) {
  const [tab, setTab] = useState<ResourceDetailTabId>("overview");
  const navigate = useNavigate();

  // These decisions intentionally use the same canonical/override resource as
  // list rows. The backend remains the final authorization boundary.
  const read = useClusterResourcePermission(
    clusterId,
    resourceType,
    "read",
    permissionResource,
  );
  const update = useClusterResourcePermission(
    clusterId,
    resourceType,
    "update",
    permissionResource,
  );
  const manage = useClusterResourcePermission(
    clusterId,
    resourceType,
    "manage",
    permissionResource,
  );
  // Always invoke pod-only permission hooks to preserve hook ordering.
  const logsPermission = useClusterResourcePermission(
    clusterId,
    resourceType,
    "logs",
    permissionResource,
  );
  const execPermission = useClusterResourcePermission(
    clusterId,
    resourceType,
    "exec",
    permissionResource,
  );
  const metricsPermission = useClusterResourcePermission(
    clusterId,
    "monitoring",
    "read",
    "monitoring",
  );

  const isPod = resourceType === "pods";
  const resourceQuery = useK8sResource(clusterId, k8sPath, read.allowed);
  const { data, isLoading, error } = resourceQuery;
  const obj = data as K8sObject | undefined;
  const conditions = obj?.status?.conditions ?? [];
  const tabs = useMemo(() => {
    const available: Array<{ id: ResourceDetailTabId; label: string }> = [
      ...BASE_TABS,
    ];
    if (conditions.length > 0) {
      available.splice(2, 0, { id: "conditions", label: "Conditions" });
    }
    if (namespace && supportsRolloutHistory(obj?.kind ?? "")) {
      available.push({ id: "rollout", label: "Rollout history" });
    }
    if (namespace && WORKLOAD_RESOURCE_TYPES.has(resourceType)) {
      available.push(
        { id: "workload-pods", label: "Pods" },
        { id: "workload-logs", label: "Logs" },
      );
      if (metricsPermission.allowed) {
        available.push({ id: "workload-metrics", label: "Metrics" });
      }
    }
    if (isPod) {
      if (metricsPermission.allowed) {
        available.push({ id: "pod-metrics", label: "Metrics" });
      }
      if (logsPermission.allowed) available.push({ id: "logs", label: "Logs" });
      if (execPermission.allowed) available.push({ id: "exec", label: "Exec" });
    }
    return available;
  }, [
    conditions.length,
    execPermission.allowed,
    isPod,
    logsPermission.allowed,
    metricsPermission.allowed,
    namespace,
    obj?.kind,
    resourceType,
  ]);

  if (!read.allowed) {
    return (
      <PermissionState
        title="Resource access denied"
        description={read.disabledReason || read.reason}
        className="py-24"
      />
    );
  }

  const kind = obj?.kind || resourceType;
  const created = obj?.metadata?.creationTimestamp;
  const detailStatus = isPod ? podStatus(obj) : obj?.status?.phase;

  const backTo = `/dashboard/clusters/${clusterId}/${resourceType}`;

  return (
    <div className="space-y-6">
      <ResourceMasthead
        backTo={backTo}
        title={name}
        mono
        status={detailStatus && <StatusBadge status={detailStatus} />}
        meta={[
          { label: "Kind", value: kind },
          ...(namespace ? [{ label: "Namespace", value: namespace }] : []),
          ...(created
            ? [{ label: "Age", value: formatRelativeTime(created) }]
            : []),
        ]}
        actions={
          obj?.kind && (
            <ResourceActions
              clusterId={clusterId}
              kind={obj.kind}
              namespace={namespace}
              name={name}
              replicas={obj.spec?.replicas}
              paused={
                obj.kind === "Deployment"
                  ? (obj.spec?.paused ?? false)
                  : undefined
              }
              suspended={
                obj.kind === "CronJob"
                  ? (obj.spec?.suspend ?? false)
                  : undefined
              }
              jobTemplate={
                obj.kind === "CronJob"
                  ? (asObject(obj.spec).jobTemplate as Record<string, unknown>)
                  : undefined
              }
              k8sPath={k8sPath}
              permissionResource={permissionResource}
              onDeleted={() => void navigate({ to: backTo })}
            />
          )
        }
      />

      <TabStrip
        tabs={tabs.map((item) => ({ key: item.id, label: item.label }))}
        value={tab}
        onChange={setTab}
        aria-label="Resource detail"
      />

      <ResourceDetailTabPanel
        tab={tab}
        clusterId={clusterId}
        resourceType={resourceType}
        namespace={namespace}
        name={name}
        k8sPath={k8sPath}
        obj={obj}
        isLoading={isLoading}
        error={error}
        updateAllowed={update.allowed}
        forceConflictPermission={manage}
        onRetry={() => void resourceQuery.refetch()}
      />
    </div>
  );
}

function podStatus(obj?: K8sObject): string | undefined {
  for (const status of [
    ...(obj?.status?.initContainerStatuses ?? []),
    ...(obj?.status?.containerStatuses ?? []),
  ]) {
    const waiting = status.state?.waiting;
    if (waiting?.reason) return waiting.reason;
    const terminated = status.state?.terminated;
    if (terminated?.reason && (terminated.exitCode ?? 0) !== 0) {
      return terminated.reason;
    }
  }
  return obj?.status?.reason ?? obj?.status?.phase;
}

function asObject(value: unknown): Record<string, unknown> {
  return value && typeof value === "object"
    ? (value as Record<string, unknown>)
    : {};
}
