import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  getSnapshotRestore,
  getSnapshotRestores,
} from "@/lib/api/cluster-velero";
import { queryKeys } from "@/lib/query-keys";
import { useInvestigationParam } from "@/components/resources/resource-navigation-context";
import { QueryStates } from "@/components/ui/query-states";
import { ModalShell } from "@/components/ui/modal-shell";
import { StatusBadge } from "@/components/ui/status-badge";
import { ActionButton } from "@/components/ui/action-button";
import { DataTable } from "@/components/ui/data-table";
import { PageSection } from "@/components/ui/page";
import { pageTableCount } from "@/lib/api/pagination";

const terminal = new Set([
  "completed",
  "partiallyfailed",
  "failed",
  "failedvalidation",
]);
export function SnapshotRestoreHistory({ clusterId }: { clusterId: string }) {
  const [selected, setSelected] = useInvestigationParam("restore");
  const [page, setPage] = useState(0);
  const query = useQuery({
    queryKey: queryKeys.clusterPages.snapshotRestores(clusterId, page * 50),
    queryFn: ({ signal }) => getSnapshotRestores(clusterId, page * 50, signal),
    refetchInterval: 10000,
    throwOnError: false,
  });
  return (
    <PageSection title="Restore history">
      <p className="text-sm text-muted-foreground">
        Restores targeting this cluster. A queued request is not a completed
        restore.
      </p>
      <QueryStates
        query={query}
        permission="clusters:read"
        errorTitle="Restore history unavailable"
      >
        {(result) => (
          <DataTable
            data={result.data ?? []}
            keyExtractor={(restore) => restore.id ?? ""}
            columns={[
              {
                key: "id",
                header: "Restore",
                accessor: (restore) => (
                  <ActionButton
                    intent="ghost"
                    size="sm"
                    onClick={() => setSelected(restore.id ?? "")}
                  >
                    {restore.velero_name || restore.id || ""}
                  </ActionButton>
                ),
              },
              {
                key: "source",
                header: "Source cluster",
                accessor: (restore) => restore.source_cluster_id,
              },
              {
                key: "snapshot",
                header: "Source snapshot",
                accessor: (restore) => restore.snapshot_id,
              },
              {
                key: "status",
                header: "Status",
                accessor: (restore) => (
                  <StatusBadge status={restore.phase || "Unknown"} />
                ),
              },
            ]}
            searchable={false}
            pageSize={50}
            serverSide={{
              ...pageTableCount(result),
              pagination: { pageIndex: page, pageSize: 50 },
              onPaginationChange: (next) => setPage(next.pageIndex),
            }}
            emptyState={{
              title: "No restores",
              description:
                "Accepted restore requests targeting this cluster appear here.",
            }}
          />
        )}
      </QueryStates>
      {selected && (
        <SnapshotRestoreReceipt
          clusterId={clusterId}
          id={selected}
          onClose={() => setSelected("")}
        />
      )}
    </PageSection>
  );
}
export function SnapshotRestoreReceipt({
  clusterId,
  id,
  onClose,
}: {
  clusterId: string;
  id: string;
  onClose: () => void;
}) {
  const query = useQuery({
    queryKey: queryKeys.clusterPages.snapshotRestore(clusterId, id),
    queryFn: ({ signal }) => getSnapshotRestore(clusterId, id, signal),
    refetchInterval: (current) =>
      terminal.has((current.state.data?.phase ?? "").toLowerCase())
        ? false
        : 2500,
    throwOnError: false,
  });
  return (
    <ModalShell title="Snapshot restore" onClose={onClose}>
      <QueryStates
        query={query}
        permission="clusters:read"
        errorTitle="Restore unavailable"
      >
        {(restore) => (
          <div className="space-y-3">
            <StatusBadge status={restore.phase || "Unknown"} />
            {restore.last_poll_error && (
              <p role="alert" className="text-sm text-status-warning">
                Status observation failed: {restore.last_poll_error}. The
                displayed phase may be stale.
              </p>
            )}
            <dl className="grid grid-cols-2 gap-2 text-sm">
              <dt>Restore ID</dt>
              <dd className="break-all">{restore.id ?? ""}</dd>
              <dt>Source snapshot</dt>
              <dd className="break-all">{restore.snapshot_id}</dd>
              <dt>Source cluster</dt>
              <dd className="break-all">{restore.source_cluster_id}</dd>
              <dt>Target cluster</dt>
              <dd className="break-all">{restore.target_cluster_id}</dd>
              <dt>Last observed</dt>
              <dd className="break-all">
                {restore.last_poll_at || "Not yet observed"}
              </dd>
              <dt>Started</dt>
              <dd className="break-all">
                {restore.start_time || "Not started"}
              </dd>
              <dt>Completed</dt>
              <dd className="break-all">
                {restore.completion_time || "Not completed"}
              </dd>
              <dt>Errors</dt>
              <dd className="break-all">
                {restore.errors_count ?? "Unavailable"}
              </dd>
              <dt>Warnings</dt>
              <dd className="break-all">
                {restore.warnings_count ?? "Unavailable"}
              </dd>
            </dl>
            <p className="text-sm text-muted-foreground">
              This status belongs to the restore request, independently of the
              source snapshot's completion.
            </p>
          </div>
        )}
      </QueryStates>
    </ModalShell>
  );
}
