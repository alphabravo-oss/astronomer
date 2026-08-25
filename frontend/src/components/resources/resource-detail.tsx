"use client";

import { useMemo, useState, type KeyboardEvent } from "react";
import { ArrowLeft } from "lucide-react";

import {
  ResourceDetailTabPanel,
  type ResourceDetailTabId,
} from "@/components/resources/resource-detail-tabs";
import type { K8sObject } from "@/components/resources/resource-detail-model";
import { supportsRolloutHistory } from "@/components/resources/rollout-history";
import { PermissionState } from "@/components/ui/empty-state";
import { ResourceActions } from "@/components/workloads/resource-actions";
import { useK8sResource } from "@/lib/hooks";
import { useRouter } from "@/lib/navigation";
import { useClusterResourcePermission } from "@/lib/permission-hooks";
import { cn, formatRelativeTime } from "@/lib/utils";

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

export function ResourceDetail({
  clusterId,
  resourceType,
  namespace,
  name,
  k8sPath,
  permissionResource,
}: ResourceDetailProps) {
  const router = useRouter();
  const [tab, setTab] = useState<ResourceDetailTabId>("overview");

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
    if (isPod) {
      if (logsPermission.allowed) available.push({ id: "logs", label: "Logs" });
      if (execPermission.allowed) available.push({ id: "exec", label: "Exec" });
    }
    return available;
  }, [
    conditions.length,
    execPermission.allowed,
    isPod,
    logsPermission.allowed,
    namespace,
    obj?.kind,
  ]);

  const handleTabKeyDown = (
    event: KeyboardEvent<HTMLButtonElement>,
    index: number,
  ) => {
    let nextIndex: number | undefined;
    switch (event.key) {
      case "ArrowRight":
      case "ArrowDown":
        nextIndex = (index + 1) % tabs.length;
        break;
      case "ArrowLeft":
      case "ArrowUp":
        nextIndex = (index - 1 + tabs.length) % tabs.length;
        break;
      case "Home":
        nextIndex = 0;
        break;
      case "End":
        nextIndex = tabs.length - 1;
        break;
      default:
        return;
    }
    event.preventDefault();
    setTab(tabs[nextIndex].id);
    const buttons =
      event.currentTarget.parentElement?.querySelectorAll<HTMLElement>(
        '[role="tab"]',
      );
    buttons?.[nextIndex]?.focus();
  };

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

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start gap-4">
        <button
          onClick={() => router.back()}
          className="mt-1 rounded-md p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          aria-label="Back"
        >
          <ArrowLeft className="h-5 w-5" />
        </button>
        <div className="min-w-[10rem] flex-1">
          <h1 className="truncate font-mono text-xl font-semibold tracking-tight text-foreground">
            {name}
          </h1>
          <div className="mt-1 flex items-center gap-4 text-xs text-muted-foreground">
            <span>Kind: {kind}</span>
            {namespace && <span>Namespace: {namespace}</span>}
            {created && <span>Age: {formatRelativeTime(created)}</span>}
          </div>
        </div>
        {obj?.kind && (
          <ResourceActions
            clusterId={clusterId}
            kind={obj.kind}
            namespace={namespace}
            name={name}
            replicas={obj.spec?.replicas}
            paused={
              obj.kind === "Deployment" ? (obj.spec?.paused ?? false) : undefined
            }
            suspended={
              obj.kind === "CronJob" ? (obj.spec?.suspend ?? false) : undefined
            }
            jobTemplate={
              obj.kind === "CronJob"
                ? (asObject(obj.spec).jobTemplate as Record<string, unknown>)
                : undefined
            }
            k8sPath={k8sPath}
            permissionResource={permissionResource}
            onDeleted={() => router.back()}
          />
        )}
      </div>

      <div className="border-b border-border">
        <div
          className="-mb-px flex gap-0 overflow-x-auto"
          aria-label="Resource detail"
          role="tablist"
        >
          {tabs.map((item, index) => (
            <button
              key={item.id}
              id={`resource-tab-${item.id}`}
              type="button"
              role="tab"
              aria-selected={tab === item.id}
              aria-controls={`resource-tabpanel-${item.id}`}
              tabIndex={tab === item.id ? 0 : -1}
              onClick={() => setTab(item.id)}
              onKeyDown={(event) => handleTabKeyDown(event, index)}
              className={cn(
                "shrink-0 border-b-2 px-4 py-2 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset",
                tab === item.id
                  ? "border-foreground text-foreground"
                  : "border-transparent text-muted-foreground hover:border-muted-foreground/30 hover:text-foreground",
              )}
            >
              {item.label}
            </button>
          ))}
        </div>
      </div>

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

function asObject(value: unknown): Record<string, unknown> {
  return value && typeof value === "object"
    ? (value as Record<string, unknown>)
    : {};
}
