import type { ComponentType, ReactNode } from "react";
import {
  AlertCircle,
  Boxes,
  Briefcase,
  CheckCircle2,
  CircleHelp,
  Clock3,
  Database,
  Disc,
} from "lucide-react";

import { DataTable, type Column } from "@/components/ui/data-table";
import { detailHref } from "@/lib/k8s-paths";
import { Link as RouterLink } from "@tanstack/react-router";
import { formatRelativeTime } from "@/lib/utils";

export interface WorkloadMetadata {
  name: string;
  namespace: string;
  creationTimestamp?: string;
  ownerReferences?: Array<{ controller?: boolean }>;
}

export interface DeploymentLike {
  apiVersion: string;
  kind: string;
  metadata: WorkloadMetadata;
  spec?: { replicas?: number };
  status?: {
    replicas?: number;
    readyReplicas?: number;
    availableReplicas?: number;
    updatedReplicas?: number;
  };
}

export interface DaemonSetLike {
  apiVersion: string;
  kind: string;
  metadata: WorkloadMetadata;
  status?: {
    desiredNumberScheduled?: number;
    numberReady?: number;
    numberAvailable?: number;
  };
}

export interface JobLike {
  apiVersion: string;
  kind: string;
  metadata: WorkloadMetadata;
  spec?: { suspend?: boolean; completions?: number; parallelism?: number };
  status?: {
    active?: number;
    succeeded?: number;
    failed?: number;
    conditions?: Array<{ type: string; status: string }>;
  };
}

export interface CronJobLike {
  apiVersion: string;
  kind: string;
  metadata: WorkloadMetadata;
  spec?: { suspend?: boolean; schedule?: string };
  status?: {
    lastScheduleTime?: string;
    active?: Array<unknown>;
  };
}

export type WorkloadRow =
  | { kind: "Deployment"; item: DeploymentLike }
  | { kind: "StatefulSet"; item: DeploymentLike }
  | { kind: "DaemonSet"; item: DaemonSetLike }
  | { kind: "Job"; item: JobLike }
  | { kind: "CronJob"; item: CronJobLike };

const KIND_META: Record<
  WorkloadRow["kind"],
  { icon: ComponentType<{ className?: string }>; urlSegment: string }
> = {
  Deployment: { icon: Boxes, urlSegment: "deployments" },
  StatefulSet: { icon: Database, urlSegment: "statefulsets" },
  DaemonSet: { icon: Disc, urlSegment: "daemonsets" },
  Job: { icon: Briefcase, urlSegment: "jobs" },
  CronJob: { icon: Clock3, urlSegment: "cronjobs" },
};

export interface WorkloadStatus {
  label: string;
  tone: string;
  icon: ReactNode;
}

export function workloadStatus(workload: WorkloadRow): WorkloadStatus {
  const ok = (label: string): WorkloadStatus => ({
    label,
    tone: "text-status-success",
    icon: <CheckCircle2 className="h-3.5 w-3.5" />,
  });
  const bad = (label: string): WorkloadStatus => ({
    label,
    tone: "text-status-error",
    icon: <AlertCircle className="h-3.5 w-3.5" />,
  });
  const muted = (label: string): WorkloadStatus => ({
    label,
    tone: "text-muted-foreground",
    icon: <CircleHelp className="h-3.5 w-3.5" />,
  });

  if (workload.kind === "Deployment" || workload.kind === "StatefulSet") {
    const desired = workload.item.spec?.replicas ?? 0;
    const ready = workload.item.status?.readyReplicas ?? 0;
    if (desired === 0) return muted("Scaled to 0");
    const label = `${ready}/${desired} ready`;
    return ready >= desired ? ok(label) : bad(label);
  }
  if (workload.kind === "DaemonSet") {
    const desired = workload.item.status?.desiredNumberScheduled ?? 0;
    const ready = workload.item.status?.numberReady ?? 0;
    if (desired === 0) return muted("No nodes match");
    const label = `${ready}/${desired} ready`;
    return ready >= desired ? ok(label) : bad(label);
  }
  if (workload.kind === "Job") {
    if (workload.item.spec?.suspend) return muted("Suspended");
    const conditions = workload.item.status?.conditions ?? [];
    if (
      conditions.some(
        (condition) =>
          condition.type === "Complete" && condition.status === "True",
      )
    ) {
      return ok("Complete");
    }
    if (
      conditions.some(
        (condition) =>
          condition.type === "Failed" && condition.status === "True",
      )
    ) {
      return bad("Failed");
    }
    return muted(`Active ${workload.item.status?.active ?? 0}`);
  }
  if (workload.item.spec?.suspend) return muted("Suspended");
  if ((workload.item.status?.active ?? []).length > 0) {
    return ok(`Running (${workload.item.status?.active?.length ?? 0})`);
  }
  return ok(workload.item.spec?.schedule ?? "Idle");
}

export function workloadKey(workload: WorkloadRow): string {
  return `${workload.kind}/${workload.item.metadata.namespace}/${workload.item.metadata.name}`;
}

export function workloadHref(clusterId: string, workload: WorkloadRow): string {
  return detailHref(
    clusterId,
    KIND_META[workload.kind].urlSegment,
    workload.item.metadata.namespace,
    workload.item.metadata.name,
  );
}

export function workloadColumns(clusterId: string): Column<WorkloadRow>[] {
  return [
    {
      key: "kind",
      header: "Kind",
      accessor: (workload) => {
        const Icon = KIND_META[workload.kind].icon;
        return (
          <span className="inline-flex items-center gap-1.5 text-foreground">
            <Icon className="h-3.5 w-3.5 text-muted-foreground" />
            {workload.kind}
          </span>
        );
      },
      searchAccessor: (workload) => workload.kind,
      sortAccessor: (workload) => workload.kind,
      filter: { label: "Kind" },
      width: "9rem",
    },
    {
      key: "name",
      header: "Name",
      accessor: (workload) => (
        <RouterLink
          to={workloadHref(clusterId, workload)}
          className="font-medium text-foreground hover:underline"
        >
          {workload.item.metadata.name}
        </RouterLink>
      ),
      searchAccessor: (workload) => workload.item.metadata.name,
      sortAccessor: (workload) => workload.item.metadata.name,
    },
    {
      key: "namespace",
      header: "Namespace",
      accessor: (workload) => (
        <span className="text-muted-foreground">
          {workload.item.metadata.namespace}
        </span>
      ),
      searchAccessor: (workload) => workload.item.metadata.namespace,
      sortAccessor: (workload) => workload.item.metadata.namespace,
      filter: { label: "Namespace" },
      width: "12rem",
    },
    {
      key: "status",
      header: "Status",
      accessor: (workload) => {
        const status = workloadStatus(workload);
        return (
          <span className={`inline-flex items-center gap-1 ${status.tone}`}>
            {status.icon}
            {status.label}
          </span>
        );
      },
      searchAccessor: (workload) => workloadStatus(workload).label,
      sortAccessor: (workload) => workloadStatus(workload).label,
      width: "10rem",
    },
    {
      key: "age",
      header: "Age",
      accessor: (workload) => (
        <span className="text-muted-foreground">
          {workload.item.metadata.creationTimestamp
            ? formatRelativeTime(workload.item.metadata.creationTimestamp)
            : "—"}
        </span>
      ),
      sortAccessor: (workload) =>
        workload.item.metadata.creationTimestamp
          ? Date.parse(workload.item.metadata.creationTimestamp)
          : 0,
      width: "8rem",
    },
  ];
}

interface WorkloadsTableProps {
  clusterId: string;
  data: WorkloadRow[];
  onRowClick: (workload: WorkloadRow) => void;
}

export function WorkloadsTable({
  clusterId,
  data,
  onRowClick,
}: WorkloadsTableProps) {
  return (
    <DataTable
      data={data}
      columns={workloadColumns(clusterId)}
      keyExtractor={workloadKey}
      density="compact"
      pageSize={25}
      persistKey="cluster-workloads"
      resizable
      searchPlaceholder="Search workloads..."
      emptyState={{
        title: "No workloads available",
        description:
          "Resources will appear here when they are available in this scope.",
      }}
      onRowClick={onRowClick}
    />
  );
}
