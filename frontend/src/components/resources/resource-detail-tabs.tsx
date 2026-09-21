import { useMemo } from "react";
import { Loader2 } from "lucide-react";

import { Link as RouterLink } from "@tanstack/react-router";
import { useK8sResource } from "@/lib/hooks/kubernetes-proxy";
import { useWorkloadPods } from "@/lib/hooks/workloads";
import {
  detailHref,
  KIND_TO_RESOURCE_TYPE,
  k8sListPath,
} from "@/lib/k8s-paths";
import { useClusterResourcePermission } from "@/lib/permission-hooks";
import type { PermissionDecision } from "@/lib/permissions";
import { cn, formatRelativeTime } from "@/lib/utils";
import type { Pod } from "@/types";
import { ResourceOverview } from "@/components/resources/resource-overview";
import type { K8sObject } from "@/components/resources/resource-detail-model";
import { RolloutHistory } from "@/components/resources/rollout-history";
import { ErrorState, LoadingState } from "@/components/ui/empty-state";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/operator-table";
import { TabsContent } from "@/components/ui/tabs";
import { YamlPanel } from "@/components/ui/yaml-view-dialog";
import { PodLogsViewer } from "@/components/workloads/pod-logs-viewer";
import { PodTerminal } from "@/components/workloads/pod-terminal";
import {
  ResourceMetricsTab,
  WorkloadResourceTabPanel,
  type WorkloadResourceTabId,
} from "@/components/resources/workload-resource-tabs";

export type ResourceDetailTabId =
  | "overview"
  | "yaml"
  | "conditions"
  | "events"
  | "related"
  | "rollout"
  | "logs"
  | "exec"
  | "pod-metrics"
  | WorkloadResourceTabId;

interface ResourceDetailTabPanelProps {
  tab: ResourceDetailTabId;
  clusterId: string;
  resourceType: string;
  namespace?: string;
  name: string;
  k8sPath: string;
  obj?: K8sObject;
  isLoading: boolean;
  error: unknown;
  updateAllowed: boolean;
  forceConflictPermission: PermissionDecision;
  onRetry: () => void;
}

export function ResourceDetailTabPanel({
  tab,
  clusterId,
  resourceType,
  namespace,
  name,
  k8sPath,
  obj,
  isLoading,
  error,
  updateAllowed,
  forceConflictPermission,
  onRetry,
}: ResourceDetailTabPanelProps) {
  const isPod = resourceType === "pods";
  const isWorkloadTab = tab.startsWith("workload-");
  const conditions = obj?.status?.conditions ?? [];
  const kind = obj?.kind || resourceType;

  return (
    <TabsContent value={tab}>
      {tab === "overview" &&
        (isLoading ? (
          <LoadingState
            title="Loading resource"
            description={`Fetching ${name} from the cluster.`}
            className="py-24"
          />
        ) : error ? (
          <ErrorState
            title="Failed to load resource"
            description={(error as Error).message}
            onRetry={onRetry}
            className="py-24"
          />
        ) : (
          <ResourceOverview
            obj={obj}
            resourceType={resourceType}
            clusterId={clusterId}
            namespace={namespace}
            name={name}
          />
        ))}

      {tab === "yaml" && (
        <div className="h-[70vh] overflow-hidden rounded-lg border border-border">
          <YamlPanel
            clusterId={clusterId}
            k8sPath={k8sPath}
            allowEdit={updateAllowed}
            forceConflictPermission={forceConflictPermission}
          />
        </div>
      )}

      {tab === "conditions" && <ConditionsTab conditions={conditions} />}
      {tab === "events" && (
        <ResourceEvents
          clusterId={clusterId}
          namespace={namespace}
          name={name}
          kind={kind}
        />
      )}
      {tab === "related" && (
        <RelatedResources
          clusterId={clusterId}
          namespace={namespace}
          name={name}
          kind={kind}
          obj={obj}
        />
      )}
      {tab === "rollout" && namespace && obj?.kind && (
        <RolloutHistory
          clusterId={clusterId}
          namespace={namespace}
          owner={{ kind: obj.kind, name, uid: obj.metadata?.uid }}
        />
      )}
      {tab === "logs" && isPod && namespace && (
        <PodLogsViewer
          clusterId={clusterId}
          namespace={namespace}
          pods={[podForViewer(obj, namespace)]}
          selectedPod={name}
          onPodChange={() => {
            // The detail route has exactly one selected pod.
          }}
        />
      )}
      {tab === "exec" && isPod && namespace && (
        <div className="h-[70vh]">
          <PodTerminal
            clusterId={clusterId}
            namespace={namespace}
            pod={name}
            container={podContainerNames(obj)[0] ?? ""}
            containers={podContainerNames(obj)}
          />
        </div>
      )}
      {tab === "pod-metrics" && isPod && namespace && (
        <ResourceMetricsTab
          clusterId={clusterId}
          resourceType="pods"
          namespace={namespace}
          name={name}
        />
      )}
      {isWorkloadTab && namespace && (
        <WorkloadResourceTabPanel
          tab={tab as WorkloadResourceTabId}
          clusterId={clusterId}
          resourceType={resourceType}
          namespace={namespace}
          name={name}
        />
      )}
    </TabsContent>
  );
}

export function ConditionsTab({
  conditions,
}: {
  conditions: Array<Record<string, unknown>>;
}) {
  if (conditions.length === 0) {
    return (
      <p className="py-12 text-center text-sm text-muted-foreground">
        No conditions reported.
      </p>
    );
  }
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-card">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Type</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Reason</TableHead>
            <TableHead>Message</TableHead>
            <TableHead>Last Transition</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {conditions.map((condition, index) => {
            const status = String(condition.status ?? "-");
            const transition = condition.lastTransitionTime;
            return (
              <TableRow key={String(condition.type ?? index)}>
                <TableCell className="text-xs font-medium">
                  {String(condition.type ?? "-")}
                </TableCell>
                <TableCell
                  className={cn(
                    "text-xs font-medium",
                    status === "True"
                      ? "text-status-success"
                      : status === "False"
                        ? "text-status-warning"
                        : "text-muted-foreground",
                  )}
                >
                  {status}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {String(condition.reason ?? "-")}
                </TableCell>
                <TableCell className="break-words text-xs text-muted-foreground">
                  {String(condition.message ?? "-")}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {typeof transition === "string" && transition
                    ? formatRelativeTime(transition)
                    : "-"}
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}

interface K8sEvent {
  metadata?: { uid?: string };
  type?: string;
  reason?: string;
  message?: string;
  count?: number;
  lastTimestamp?: string;
  eventTime?: string;
  firstTimestamp?: string;
}

function eventsPath(
  namespace: string | undefined,
  name: string,
  kind: string,
): string {
  const selector = `involvedObject.name=${name},involvedObject.kind=${kind}`;
  const base = namespace
    ? `api/v1/namespaces/${namespace}/events`
    : "api/v1/events";
  return `${base}?fieldSelector=${encodeURIComponent(selector)}`;
}

function ResourceEvents({
  clusterId,
  namespace,
  name,
  kind,
}: {
  clusterId: string;
  namespace?: string;
  name: string;
  kind: string;
}) {
  const path = useMemo(
    () => eventsPath(namespace, name, kind),
    [namespace, name, kind],
  );
  const { data, isLoading, error } = useK8sResource(clusterId, path);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-24">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (error) {
    return (
      <div className="py-24 text-center text-sm text-status-error">
        Failed to load events: {(error as Error).message}
      </div>
    );
  }

  const items = (data as { items?: K8sEvent[] } | undefined)?.items ?? [];
  if (items.length === 0) {
    return (
      <p className="py-12 text-center text-sm text-muted-foreground">
        No events for this resource.
      </p>
    );
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Type</TableHead>
          <TableHead>Reason</TableHead>
          <TableHead>Message</TableHead>
          <TableHead className="text-center">Count</TableHead>
          <TableHead>Last Seen</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((event, index) => {
          const last =
            event.lastTimestamp || event.eventTime || event.firstTimestamp;
          return (
            <TableRow key={event.metadata?.uid || index}>
              <TableCell
                className={cn(
                  "text-xs font-medium",
                  event.type === "Warning"
                    ? "text-status-warning"
                    : "text-status-info",
                )}
              >
                {event.type || "-"}
              </TableCell>
              <TableCell className="text-xs font-medium text-foreground">
                {event.reason || "-"}
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">
                {event.message || "-"}
              </TableCell>
              <TableCell className="text-center text-xs tabular-nums">
                {event.count ?? 1}
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">
                {last ? formatRelativeTime(last) : "-"}
              </TableCell>
            </TableRow>
          );
        })}
      </TableBody>
    </Table>
  );
}

const WORKLOAD_POD_KINDS: Record<string, string> = {
  Deployment: "deployment",
  StatefulSet: "statefulset",
  DaemonSet: "daemonset",
};

function RelatedResources({
  clusterId,
  namespace,
  name,
  kind,
  obj,
}: {
  clusterId: string;
  namespace?: string;
  name: string;
  kind: string;
  obj?: K8sObject;
}) {
  const owners = obj?.metadata?.ownerReferences ?? [];
  const workloadKind = WORKLOAD_POD_KINDS[kind] ?? "";
  const { data: pods } = useWorkloadPods(
    clusterId,
    workloadKind,
    namespace ?? "",
    name,
  );
  const showPods = !!workloadKind && !!namespace;
  const servicesPermission = useClusterResourcePermission(
    clusterId,
    "services",
    "read",
  );
  const servicesQuery = useK8sResource(
    clusterId,
    namespace ? k8sListPath("services", namespace) : "",
    kind === "Pod" && !!namespace && servicesPermission.allowed,
  );
  const selectedByServices = useMemo(() => {
    if (kind !== "Pod") return [];
    const labels = obj?.metadata?.labels ?? {};
    const items = (servicesQuery.data as { items?: K8sObject[] } | undefined)
      ?.items;
    return (items ?? []).filter((service) => {
      const selector = service.spec?.selector ?? {};
      const entries = Object.entries(selector);
      return (
        entries.length > 0 &&
        entries.every(([key, value]) => labels[key] === value)
      );
    });
  }, [kind, obj?.metadata?.labels, servicesQuery.data]);

  return (
    <div className="space-y-6">
      <DetailSection title="Owned By">
        {owners.length === 0 ? (
          <p className="text-xs text-muted-foreground">No owner references.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Kind</TableHead>
                <TableHead>Name</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {owners.map((reference) => {
                const ownerType = KIND_TO_RESOURCE_TYPE[reference.kind];
                return (
                  <TableRow
                    key={reference.uid || `${reference.kind}/${reference.name}`}
                  >
                    <TableCell className="text-xs">{reference.kind}</TableCell>
                    <TableCell className="font-mono text-xs">
                      {ownerType ? (
                        <RouterLink
                          to={detailHref(
                            clusterId,
                            ownerType,
                            namespace,
                            reference.name,
                          )}
                          className="text-foreground hover:underline"
                        >
                          {reference.name}
                        </RouterLink>
                      ) : (
                        reference.name
                      )}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
      </DetailSection>

      {showPods && (
        <DetailSection title="Pods">
          {!pods || pods.length === 0 ? (
            <p className="text-xs text-muted-foreground">No pods.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="text-center">Restarts</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {pods.map((pod) => (
                  <TableRow key={`${pod.namespace}/${pod.name}`}>
                    <TableCell className="font-mono text-xs">
                      <RouterLink
                        to={detailHref(
                          clusterId,
                          "pods",
                          pod.namespace,
                          pod.name,
                        )}
                        className="text-foreground hover:underline"
                      >
                        {pod.name}
                      </RouterLink>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {pod.status}
                    </TableCell>
                    <TableCell className="text-center text-xs tabular-nums">
                      {pod.restarts}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </DetailSection>
      )}

      {kind === "Pod" && namespace && (
        <DetailSection title="Selected by Services">
          {!servicesPermission.allowed ? (
            <p className="text-xs text-muted-foreground">
              Service relationships require services:read for this cluster.
            </p>
          ) : servicesQuery.isLoading ? (
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Loader2 className="h-3.5 w-3.5 animate-spin" /> Resolving service
              selectors…
            </div>
          ) : selectedByServices.length === 0 ? (
            <p className="text-xs text-muted-foreground">
              No Service selector currently targets this pod.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Cluster IP</TableHead>
                  <TableHead>Selector</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {selectedByServices.map((service) => (
                  <TableRow
                    key={service.metadata?.uid ?? service.metadata?.name}
                  >
                    <TableCell className="font-mono text-xs">
                      <RouterLink
                        to={detailHref(
                          clusterId,
                          "services",
                          namespace,
                          service.metadata?.name ?? "",
                        )}
                        className="text-foreground hover:underline"
                      >
                        {service.metadata?.name}
                      </RouterLink>
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {service.spec?.clusterIP ?? "—"}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {Object.entries(service.spec?.selector ?? {})
                        .map(([key, value]) => `${key}=${value}`)
                        .join(", ")}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </DetailSection>
      )}
    </div>
  );
}

function DetailSection({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-card">
      <h2 className="border-b border-border px-4 py-2.5 text-sm font-semibold text-foreground">
        {title}
      </h2>
      <div className="px-4 py-3">{children}</div>
    </div>
  );
}

function podContainerNames(obj?: K8sObject): string[] {
  return (obj?.spec?.containers ?? [])
    .map((container) => container.name ?? "")
    .filter(Boolean);
}

function podForViewer(obj: K8sObject | undefined, namespace: string): Pod {
  const viewerContainers = (
    specs: NonNullable<K8sObject["spec"]>["containers"],
    statuses: NonNullable<K8sObject["status"]>["containerStatuses"],
    init: boolean,
  ): Pod["containers"] => {
    const byName = new Map(
      (statuses ?? []).map((status) => [status.name ?? "", status]),
    );
    return (specs ?? []).map((container) => {
      const status = byName.get(container.name ?? "");
      const state = status?.state ?? {};
      const currentState: Pod["containers"][number]["status"] = state.running
        ? "running"
        : state.waiting
          ? "waiting"
          : "terminated";
      const detail = state[currentState];
      return {
        name: container.name ?? "",
        image: container.image ?? status?.image ?? "",
        status: currentState,
        ready: status?.ready ?? false,
        restartCount: status?.restartCount ?? 0,
        init,
        reason: detail?.reason,
        message: detail?.message,
        imageId: status?.imageID,
        state: status?.state,
        lastState: status?.lastState,
      };
    });
  };
  const containers = [
    ...viewerContainers(
      obj?.spec?.containers,
      obj?.status?.containerStatuses,
      false,
    ),
    ...viewerContainers(
      obj?.spec?.initContainers,
      obj?.status?.initContainerStatuses,
      true,
    ),
  ];
  return {
    name: obj?.metadata?.name ?? "",
    namespace,
    clusterId: "",
    phase: (obj?.status?.phase ?? "Unknown") as Pod["phase"],
    status: obj?.status?.phase ?? "Unknown",
    ready: "",
    restarts: 0,
    node: obj?.spec?.nodeName ?? "",
    ip: obj?.status?.podIP ?? "",
    images: containers.map((container) => container.image).filter(Boolean),
    containers,
    conditions: [],
    createdAt: obj?.metadata?.creationTimestamp ?? "",
    age: "",
  };
}
