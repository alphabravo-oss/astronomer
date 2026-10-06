import { useState } from "react";
import { ShieldCheck } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { cn, formatRelativeTime } from "@/lib/utils";
import { MetricCard } from "@/components/ui/metric-card";
import { EmptyState } from "@/components/ui/empty-state";
import { QueryStates } from "@/components/ui/query-states";
import { pageCount, pageNumber } from "@/lib/api/pagination";
import { useBackupDrillHistory } from "@/components/settings/hooks";
import { useLatestBackupDrill } from "@/components/settings/backup-drill-hooks";
import type { BackupDrillResultView } from "@/lib/api/settings";
import { BareButton } from "@/components/form/bare-button";
import { RelativeTime } from "@/components/admin/table-cells";

function statusToVariant(status: BackupDrillResultView["status"]) {
  switch (status) {
    case "success":
      return "active" as const;
    case "partial":
      return "warning" as const;
    case "failure":
      return "error" as const;
    case "running":
      return "connecting" as const;
    default:
      return "disconnected" as const;
  }
}

function durationLabel(
  startedAt?: string,
  finishedAt?: string,
  seconds?: number | null,
) {
  if (seconds != null) return `${seconds}s`;
  if (!startedAt || !finishedAt) return "—";
  const ms = new Date(finishedAt).getTime() - new Date(startedAt).getTime();
  if (!Number.isFinite(ms) || ms < 0) return "—";
  return `${Math.round(ms / 1000)}s`;
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <MetricCard
      dense
      label={label}
      value={<span className="font-mono">{value || "—"}</span>}
    />
  );
}

export function LatestDrillCard() {
  const drillQuery = useLatestBackupDrill();

  return (
    <QueryStates
      query={drillQuery}
      loadingTitle="Loading latest restore drill"
      permission="settings:read"
      errorTitle="Failed to load the latest restore drill"
      isEmpty={(result) => result.latest == null}
      empty={
        <EmptyState
          icon={ShieldCheck}
          title="No restore drill has run"
          description="The weekly drill restores the latest dump into a scratch Postgres and records the result here."
          className="rounded-xl border border-dashed border-border bg-card p-6"
          // terminal: the drill runs on a schedule (CronJob); there's no manual trigger here.
          terminal
        />
      }
    >
      {(data) => {
        const latest = data.latest!;
        const age = data.latestSuccessAgeSeconds;
        const stale = age != null && age > 7 * 24 * 3600;
        return (
          <div className="rounded-xl border border-border bg-card p-6 space-y-4">
            <div className="flex items-start justify-between gap-4">
              <div className="space-y-1">
                <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
                  Latest restore drill
                </p>
                <div className="flex items-center gap-3">
                  <StatusBadge
                    status={statusToVariant(latest.status)}
                    label={latest.status}
                    size="sm"
                  />
                  <span
                    className={cn(
                      "text-xs",
                      stale ? "text-status-warning" : "text-muted-foreground",
                    )}
                  >
                    {formatRelativeTime(latest.finishedAt ?? latest.startedAt)}
                  </span>
                </div>
                {latest.errorMessage && (
                  <p className="text-sm text-status-error mt-2">
                    {latest.errorMessage}
                  </p>
                )}
              </div>
              <div className="grid grid-cols-2 gap-3 text-xs">
                <Stat
                  label="Schema version"
                  value={
                    latest.schemaVersion != null
                      ? String(latest.schemaVersion)
                      : "—"
                  }
                />
                <Stat
                  label="Duration"
                  value={durationLabel(latest.startedAt, latest.finishedAt)}
                />
                {latest.backupKey && (
                  <Stat label="Source dump" value={latest.backupKey} />
                )}
              </div>
            </div>
            {stale && (
              <div className="rounded-lg border border-status-warning/30 bg-status-warning/5 px-3 py-2 text-xs text-status-warning">
                Last successful drill is over a week old. Restore confidence is
                decaying — check the drill CronJob.
              </div>
            )}
          </div>
        );
      }}
    </QueryStates>
  );
}

export function HistoryTable() {
  const [page, setPage] = useState(1);
  const historyQuery = useBackupDrillHistory({ page, page_size: 25 });
  const data = historyQuery.data;
  const rows = data?.data ?? [];
  const currentPage = data ? pageNumber(data.pagination) : page;
  const totalPages = data ? pageCount(data.pagination) : undefined;

  const columns: Column<BackupDrillResultView>[] = [
    {
      key: "startedAt",
      header: "Started",
      kind: "age",
      size: 105,
      accessor: (row) => (
        <span className="text-xs text-muted-foreground font-mono">
          <RelativeTime value={row.startedAt} />
        </span>
      ),
    },
    {
      key: "status",
      header: "Status",
      kind: "status",
      size: 104,
      accessor: (row) => (
        <StatusBadge
          status={statusToVariant(row.status)}
          label={row.status}
          size="sm"
        />
      ),
    },
    {
      key: "schemaVersion",
      header: "Schema",
      kind: "version",
      size: 98,
      accessor: (row) => (
        <span className="text-xs font-mono text-muted-foreground">
          {row.schemaVersion != null ? row.schemaVersion : "—"}
        </span>
      ),
    },
    {
      key: "duration",
      header: "Duration",
      kind: "count",
      size: 112,
      accessor: (row) => (
        <span className="text-xs font-mono tabular-nums text-muted-foreground">
          {durationLabel(row.startedAt, row.finishedAt)}
        </span>
      ),
    },
    {
      key: "error",
      header: "Error",
      kind: "text",
      grow: true,
      minSize: 160,
      sortable: false,
      accessor: (row) => (
        <span className="text-xs text-status-error">
          {row.errorMessage || "—"}
        </span>
      ),
    },
  ];

  return (
    <div className="space-y-3">
      <h2 className="text-base font-semibold text-foreground">
        Restore drill history
      </h2>
      <QueryStates
        query={historyQuery}
        loadingTitle="Loading restore drill history"
        permission="settings:read"
        errorTitle="Failed to load restore drill history"
        isEmpty={(result) => result.data.length === 0}
        empty={
          <EmptyState
            icon={ShieldCheck}
            title="No restore drill history"
            description="Completed restore drills will appear here after the scheduled validation runs."
            className="rounded-xl border border-dashed border-border bg-card p-6"
            // terminal: history accrues from the scheduled CronJob, not a UI action.
            terminal
          />
        }
      >
        <DataTable
          data={rows}
          columns={columns}
          keyExtractor={(row) => row.id}
          emptyState={{
            title: "No restore drills available",
            description:
              "Resources will appear here when they are available in this scope.",
          }}
          pageSize={25}
        />
      </QueryStates>
      {data && (data.pagination.offset > 0 || data.pagination.has_more) && (
        <div className="flex items-center justify-end gap-2">
          <BareButton
            onClick={() => setPage((p) => Math.max(1, p - 1))}
            disabled={page === 1}
            className="h-8 px-3 rounded-lg border border-border text-xs font-medium disabled:opacity-50"
          >
            Previous
          </BareButton>
          <p className="text-xs text-muted-foreground">
            Page {currentPage}
            {totalPages === undefined ? "" : ` of ${totalPages}`}
          </p>
          <BareButton
            onClick={() => setPage((p) => p + 1)}
            disabled={!data.pagination.has_more}
            className="h-8 px-3 rounded-lg border border-border text-xs font-medium disabled:opacity-50"
          >
            Next
          </BareButton>
        </div>
      )}
    </div>
  );
}
