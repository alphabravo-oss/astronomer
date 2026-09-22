import { useState } from "react";
import { useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BookOpen, Cloud, Loader2, Lock, Plus, Server } from "lucide-react";

import {
  createControlPlaneSnapshot,
  getControlPlaneSnapshotRestoreGuidance,
  listControlPlaneSnapshots,
  type ControlPlaneSnapshot,
  type ControlPlaneSnapshotStatus,
} from "@/lib/api/cluster-snapshots";
import { useCluster } from "@/lib/hooks/clusters";
import { liveFallback } from "@/lib/live/status-store";
import { pageRowCount } from "@/lib/api/pagination";
import { queryKeys } from "@/lib/query-keys";
import { useClustersUpdate } from "@/lib/permission-hooks";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { cn } from "@/lib/utils";
import { ActionButton } from "@/components/ui/action-button";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { ModalShell } from "@/components/ui/modal-shell";
import { PageHeader } from "@/components/ui/page";

import {
  formatSnapshotBytes,
  formatSnapshotDate,
  isManagedControlPlane,
} from "./control-plane-snapshot-utils";

function ControlPlaneSnapshotStatusPill({
  status,
}: {
  status: ControlPlaneSnapshotStatus;
}) {
  const palette: Record<string, string> = {
    pending: "bg-muted text-muted-foreground border-border",
    running: "bg-status-info/10 text-status-info border-status-info/20",
    in_progress: "bg-status-info/10 text-status-info border-status-info/20",
    completed:
      "bg-status-success/10 text-status-success border-status-success/20",
    failed: "bg-status-error/10 text-status-error border-status-error/20",
  };
  return (
    <span
      className={cn(
        "inline-flex items-center px-2 py-0.5 rounded-sm border text-xs font-medium capitalize",
        palette[status] ?? "bg-muted text-muted-foreground border-border",
      )}
    >
      {String(status).replace(/_/g, " ")}
    </span>
  );
}

function controlPlaneSnapshotColumns(
  onRestoreGuidance: (snapshot: ControlPlaneSnapshot) => void,
): Column<ControlPlaneSnapshot>[] {
  return [
    {
      key: "name",
      header: "Snapshot",
      accessor: (snapshot) => (
        <div className="min-w-0">
          <div className="font-mono text-xs text-foreground break-all">
            {snapshot.name || snapshot.id}
          </div>
          {snapshot.error ? (
            <div className="text-xs text-status-error mt-1">
              {snapshot.error}
            </div>
          ) : null}
        </div>
      ),
      sortAccessor: (snapshot) => snapshot.name || snapshot.id,
    },
    {
      key: "status",
      header: "Status",
      accessor: (snapshot) => (
        <ControlPlaneSnapshotStatusPill status={snapshot.status} />
      ),
      sortAccessor: (snapshot) => snapshot.status,
      filter: { label: "Status" },
    },
    {
      key: "etcdRevision",
      header: "etcd revision",
      accessor: (snapshot) => (
        <span className="font-mono text-xs text-muted-foreground">
          {snapshot.etcdRevision?.toLocaleString() ?? "—"}
        </span>
      ),
      sortAccessor: (snapshot) => snapshot.etcdRevision ?? 0,
      align: "right",
    },
    {
      key: "size",
      header: "Size",
      accessor: (snapshot) => (
        <span className="text-xs text-muted-foreground">
          {formatSnapshotBytes(snapshot.sizeBytes)}
        </span>
      ),
      sortAccessor: (snapshot) => snapshot.sizeBytes ?? 0,
      align: "right",
    },
    {
      key: "createdBy",
      header: "Taken by",
      accessor: (snapshot) => (
        <span className="text-xs text-muted-foreground">
          {snapshot.createdBy || "—"}
        </span>
      ),
      sortAccessor: (snapshot) => snapshot.createdBy ?? "",
    },
    {
      key: "createdAt",
      header: "Created",
      accessor: (snapshot) => (
        <span className="text-xs text-muted-foreground">
          {formatSnapshotDate(snapshot.createdAt)}
        </span>
      ),
      sortAccessor: (snapshot) => snapshot.createdAt ?? "",
    },
    {
      key: "completedAt",
      header: "Completed",
      accessor: (snapshot) => (
        <span className="text-xs text-muted-foreground">
          {formatSnapshotDate(snapshot.completedAt)}
        </span>
      ),
      sortAccessor: (snapshot) => snapshot.completedAt ?? "",
    },
    {
      key: "actions",
      header: "",
      sortable: false,
      align: "right",
      accessor: (snapshot) => (
        <button
          onClick={() => onRestoreGuidance(snapshot)}
          className="inline-flex items-center gap-1 h-7 px-2 rounded-sm text-xs text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          title="View restore runbook"
        >
          <BookOpen className="h-3.5 w-3.5" />
          Restore guidance
        </button>
      ),
    },
  ];
}

function RestoreGuidanceModal({
  clusterId,
  snapshot,
  onClose,
}: {
  clusterId: string;
  snapshot: ControlPlaneSnapshot;
  onClose: () => void;
}) {
  const guidanceQuery = useQuery({
    queryKey: queryKeys.clusterPages.controlPlaneSnapshotGuidance(
      clusterId,
      snapshot.id,
    ),
    queryFn: ({ signal }) =>
      getControlPlaneSnapshotRestoreGuidance(clusterId, snapshot.id, signal),
    refetchOnWindowFocus: false,
  });

  return (
    <ModalShell
      title="Restore runbook"
      subtitle={
        <span className="font-mono">{snapshot.name || snapshot.id}</span>
      }
      onClose={onClose}
      size="lg"
      titleIcon={
        <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center text-muted-foreground shrink-0">
          <BookOpen className="h-4 w-4" />
        </div>
      }
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <ActionButton intent="ghost" onClick={onClose}>
          Close
        </ActionButton>
      }
    >
      <div className="rounded-lg border border-status-warning/30 bg-status-warning/10 px-3 py-2 text-xs text-status-warning flex items-start gap-2">
        <Lock className="h-3.5 w-3.5 shrink-0 mt-0.5" />
        <span>
          This is guidance only. Restoring a control plane is a manual,
          out-of-band procedure — nothing on this page performs an automated
          restore.
        </span>
      </div>
      {guidanceQuery.isLoading ? (
        <div className="flex items-center gap-2 text-sm text-muted-foreground py-8 justify-center">
          <Loader2 className="h-4 w-4 animate-spin" /> Loading runbook…
        </div>
      ) : guidanceQuery.isError ? (
        <div className="text-sm text-status-error py-8 text-center">
          Failed to load restore guidance.{" "}
          <button
            onClick={() => void guidanceQuery.refetch()}
            className="underline hover:text-foreground"
          >
            Retry
          </button>
        </div>
      ) : (
        <div className="space-y-4">
          {guidanceQuery.data?.steps?.length ? (
            <ol className="list-decimal pl-5 space-y-1 text-sm text-foreground">
              {guidanceQuery.data.steps.map((step, index) => (
                <li key={index}>{step}</li>
              ))}
            </ol>
          ) : null}
          <pre className="whitespace-pre-wrap break-words rounded-lg border border-border bg-muted/30 p-3 text-xs font-mono text-foreground">
            {guidanceQuery.data?.guidance ||
              "No runbook text was provided for this snapshot."}
          </pre>
          {guidanceQuery.data?.generatedAt ? (
            <p className="text-xs text-muted-foreground">
              Generated {formatSnapshotDate(guidanceQuery.data.generatedAt)}
            </p>
          ) : null}
        </div>
      )}
    </ModalShell>
  );
}

export function ClusterControlPlaneSnapshotsPage() {
  const { id: clusterId } = useParams({ from: "/dashboard/clusters/$id" });
  const queryClient = useQueryClient();
  const { canWrite, reason } = useClustersUpdate(clusterId);
  const { data: cluster, isLoading: clusterLoading } = useCluster(clusterId);
  const managed = isManagedControlPlane(cluster?.distribution);
  const [guidanceTarget, setGuidanceTarget] =
    useState<ControlPlaneSnapshot | null>(null);
  const [pageIndex, setPageIndex] = useState(0);
  const pageSize = 25;
  const snapshotsQuery = useQuery({
    queryKey: queryKeys.clusterPages.controlPlaneSnapshotsPage(clusterId, {
      pageIndex,
      pageSize,
    }),
    queryFn: ({ signal }) =>
      listControlPlaneSnapshots(
        clusterId,
        { limit: pageSize, offset: pageIndex * pageSize },
        signal,
      ),
    enabled: !!cluster && !managed,
    refetchInterval: liveFallback(15_000),
    refetchIntervalInBackground: false,
  });
  const takeSnapshot = useMutation({
    mutationFn: () => createControlPlaneSnapshot(clusterId, {}),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.clusterPages.controlPlaneSnapshots(clusterId),
      });
      toastSuccess("Snapshot requested");
    },
    onError: (error: Error) => toastApiError("Snapshot failed", error),
  });

  if (clusterLoading)
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  if (!cluster)
    return (
      <div className="flex flex-col items-center justify-center h-64 text-muted-foreground">
        <Server className="h-8 w-8 mb-3" />
        <p>Cluster not found</p>
      </div>
    );

  const header = (
    <PageHeader
      title="Control-plane snapshots"
      description={`Point-in-time etcd/control-plane snapshots for ${cluster.displayName}. Restore is a guided, out-of-band procedure — this page surfaces the runbook, it does not perform an automated restore.`}
      actions={
        !managed ? (
          <ActionButton
            intent="primary"
            onClick={() => canWrite && takeSnapshot.mutate()}
            disabled={!canWrite || takeSnapshot.isPending}
            disabledReason={canWrite ? undefined : reason}
            loading={takeSnapshot.isPending}
            icon={<Plus className="h-3.5 w-3.5" />}
          >
            Take snapshot
          </ActionButton>
        ) : undefined
      }
    />
  );
  if (managed)
    return (
      <div className="space-y-6">
        {header}
        <EmptyState
          icon={Cloud}
          title="Not available for managed control planes"
          description={
            <>
              {cluster.displayName} runs a managed control plane
              {cluster.distribution ? ` (${cluster.distribution})` : ""}, so its
              etcd is operated by the cloud provider and can&apos;t be
              snapshotted from here. Use the provider&apos;s managed
              backup/restore for control-plane recovery; workload state can
              still be protected with Velero snapshots.
            </>
          }
          // terminal: the cloud provider owns control-plane backup/restore here.
          terminal
        />
      </div>
    );

  return (
    <div className="space-y-6">
      {header}
      <DataTable
        data={snapshotsQuery.data?.data ?? []}
        columns={controlPlaneSnapshotColumns(setGuidanceTarget)}
        keyExtractor={(snapshot) => snapshot.id}
        loading={snapshotsQuery.isLoading}
        isError={snapshotsQuery.isError}
        error={snapshotsQuery.error}
        errorMessage="Failed to load control-plane snapshots"
        onRetry={() => void snapshotsQuery.refetch()}
        emptyState={{
          title: "No control-plane snapshots yet",
          description: "Take one to capture the current etcd state.",
        }}
        searchPlaceholder="Search snapshots…"
        serverSide={{
          rowCount: pageRowCount(snapshotsQuery.data),
          pagination: { pageIndex, pageSize },
          onPaginationChange: (next) => setPageIndex(next.pageIndex),
        }}
      />
      {guidanceTarget ? (
        <RestoreGuidanceModal
          clusterId={clusterId}
          snapshot={guidanceTarget}
          onClose={() => setGuidanceTarget(null)}
        />
      ) : null}
    </div>
  );
}
