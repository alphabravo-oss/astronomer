import {
  AlertTriangle,
  CheckCircle2,
  Clock,
  Loader2,
  Pencil,
  RotateCcw,
  Trash2,
  XCircle,
} from "lucide-react";

import { DataTable, type Column } from "@/components/ui/data-table";
import { Switch } from "@/components/ui/switch";
import type {
  Snapshot,
  SnapshotPhase,
  SnapshotSchedule,
} from "@/lib/api/cluster-velero";
import { cn } from "@/lib/utils";
import { ActionButton } from "@/components/ui/action-button";
import { Tooltip } from "@/components/ui/tooltip";
import { ChipsCell, TimestampCell } from "@/components/tables/cells";
import { BARE_BUTTON } from "@/lib/bare-button";

function SnapshotPhasePill({ phase }: { phase: SnapshotPhase }) {
  const tone =
    {
      Completed:
        "bg-status-success/10 text-status-success border-status-success/20",
      InProgress: "bg-status-info/10 text-status-info border-status-info/20",
      New: "bg-status-info/10 text-status-info border-status-info/20",
      PartiallyFailed:
        "bg-status-warning/10 text-status-warning border-status-warning/20",
      Failed: "bg-status-error/10 text-status-error border-status-error/20",
      FailedValidation:
        "bg-status-error/10 text-status-error border-status-error/20",
      Deleting: "bg-muted text-muted-foreground border-border",
    }[phase] ?? "bg-muted text-muted-foreground border-border";
  const Icon =
    phase === "Completed"
      ? CheckCircle2
      : phase === "InProgress" || phase === "New"
        ? Loader2
        : phase === "Failed" || phase === "FailedValidation"
          ? XCircle
          : phase === "PartiallyFailed"
            ? AlertTriangle
            : Clock;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 px-2 py-0.5 rounded-sm text-xs border font-medium",
        tone,
      )}
    >
      <Icon
        className={cn(
          "h-3 w-3",
          (phase === "InProgress" || phase === "New") && "animate-spin",
        )}
      />
      {phase}
    </span>
  );
}

type TableState = {
  loading: boolean;
  isError?: boolean;
  error?: unknown;
  onRetry?: () => void;
};

type ScheduleTableProps = TableState & {
  schedules: SnapshotSchedule[];
  canWrite: boolean;
  disabledReason: string;
  onToggle: (schedule: SnapshotSchedule) => void;
  onEdit: (schedule: SnapshotSchedule) => void;
  onDelete: (schedule: SnapshotSchedule) => void;
};

export function SnapshotSchedulesTable({
  schedules,
  canWrite,
  disabledReason,
  onToggle,
  onEdit,
  onDelete,
  ...state
}: ScheduleTableProps) {
  const columns: Column<SnapshotSchedule>[] = [
    {
      key: "name",
      header: "Name",
      kind: "name",
      minSize: 200,
      accessor: (schedule) => schedule.name,
      sortAccessor: (schedule) => schedule.name,
    },
    {
      key: "cron",
      header: "Cron",
      kind: "text",
      size: 130,
      minSize: 120,
      accessor: (schedule) => (
        <span className="font-mono text-xs">{schedule.cron}</span>
      ),
      sortAccessor: (schedule) => schedule.cron,
    },
    {
      key: "namespaces",
      header: "Namespaces",
      kind: "badge",
      size: 220,
      minSize: 180,
      maxSize: 280,
      accessor: (schedule) => (
        <ChipsCell
          items={
            schedule.spec.includedNamespaces?.length
              ? schedule.spec.includedNamespaces
              : ["(all)"]
          }
        />
      ),
      sortAccessor: (schedule) =>
        schedule.spec.includedNamespaces?.join(",") ?? "",
    },
    {
      key: "enabled",
      header: "Enabled",
      kind: "status",
      size: 96,
      accessor: (schedule) => (
        <Tooltip
          content={canWrite ? undefined : disabledReason}
          wrap={!canWrite}
        >
          <Switch
            size="sm"
            checked={schedule.enabled}
            disabled={!canWrite}
            onCheckedChange={() => onToggle(schedule)}
            className={schedule.enabled ? "bg-primary" : undefined}
          />
        </Tooltip>
      ),
      sortAccessor: (schedule) => String(schedule.enabled),
    },
    {
      key: "lastRun",
      header: "Last run",
      kind: "date",
      accessor: (schedule) => (
        <TimestampCell
          value={schedule.lastRun}
          fallback="—"
          className="text-xs text-muted-foreground"
        />
      ),
      sortAccessor: (schedule) => schedule.lastRun ?? "",
    },
    {
      key: "actions",
      header: "",
      kind: "actions",
      size: 80,
      minSize: 80,
      maxSize: 80,
      sortable: false,
      rowActions: true,
      accessor: (schedule) => (
        <div className="flex items-center justify-end gap-1.5">
          <ActionButton
            {...BARE_BUTTON}
            tooltip={canWrite ? "Edit" : undefined}
            disabledReason={canWrite ? undefined : disabledReason}
            aria-label="Edit"
            onClick={() => onEdit(schedule)}
            disabled={!canWrite}
            className="inline-flex items-center justify-center h-7 w-7 rounded-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50 disabled:cursor-not-allowed font-normal"
          >
            <Pencil className="h-3.5 w-3.5" />
          </ActionButton>
          <ActionButton
            {...BARE_BUTTON}
            tooltip={canWrite ? "Delete" : undefined}
            disabledReason={canWrite ? undefined : disabledReason}
            aria-label="Delete"
            onClick={() => onDelete(schedule)}
            disabled={!canWrite}
            className="inline-flex items-center justify-center h-7 w-7 rounded-sm text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors disabled:opacity-50 disabled:cursor-not-allowed font-normal"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </ActionButton>
        </div>
      ),
    },
  ];
  return (
    <DataTable
      data={schedules}
      columns={columns}
      keyExtractor={(schedule) => schedule.id}
      loading={state.loading}
      isError={state.isError}
      error={state.error}
      onRetry={state.onRetry}
      errorMessage="Failed to load snapshot schedules"
      emptyState={{
        title: "No snapshot schedules",
        description:
          "Create a schedule to take cron-driven snapshots of selected namespaces.",
      }}
      searchPlaceholder="Search schedules…"
    />
  );
}

type SnapshotsTableProps = TableState & {
  snapshots: Snapshot[];
  canWrite: boolean;
  disabledReason: string;
  onRestore: (snapshot: Snapshot) => void;
  onDelete: (snapshot: Snapshot) => void;
};

export function SnapshotsTable({
  snapshots,
  canWrite,
  disabledReason,
  onRestore,
  onDelete,
  ...state
}: SnapshotsTableProps) {
  const columns: Column<Snapshot>[] = [
    {
      key: "name",
      header: "Name",
      kind: "name",
      minSize: 260,
      accessor: (snapshot) => snapshot.name,
      sortAccessor: (snapshot) => snapshot.name,
    },
    {
      key: "source",
      header: "Source",
      kind: "text",
      size: 200,
      minSize: 150,
      accessor: (snapshot) =>
        snapshot.source === "schedule" ? (
          <Tooltip content={snapshot.scheduleName}>
            <span>
              schedule
              {snapshot.scheduleName ? (
                <span className="ml-1 text-foreground">
                  / {snapshot.scheduleName}
                </span>
              ) : null}
            </span>
          </Tooltip>
        ) : (
          "ad-hoc"
        ),
      sortAccessor: (snapshot) => snapshot.source,
    },
    {
      key: "phase",
      header: "Phase",
      kind: "status",
      accessor: (snapshot) => <SnapshotPhasePill phase={snapshot.phase} />,
      sortAccessor: (snapshot) => snapshot.phase,
      filter: { label: "Phase" },
    },
    {
      key: "started",
      header: "Started",
      kind: "date",
      accessor: (snapshot) => (
        <TimestampCell
          value={snapshot.startTimestamp}
          fallback="—"
          className="text-xs text-muted-foreground"
        />
      ),
      sortAccessor: (snapshot) => snapshot.startTimestamp ?? "",
    },
    {
      key: "completed",
      header: "Completed",
      kind: "date",
      accessor: (snapshot) => (
        <TimestampCell
          value={snapshot.completionTimestamp}
          fallback="—"
          className="text-xs text-muted-foreground"
        />
      ),
      sortAccessor: (snapshot) => snapshot.completionTimestamp ?? "",
    },
    {
      key: "warningsErrors",
      header: "Warn / Err",
      kind: "count",
      size: 90,
      accessor: (snapshot) => (
        <span className="text-muted-foreground text-xs">
          {snapshot.warnings ?? 0} /{" "}
          <span className={snapshot.errors ? "text-status-error" : ""}>
            {snapshot.errors ?? 0}
          </span>
        </span>
      ),
      sortAccessor: (snapshot) =>
        (snapshot.warnings ?? 0) + (snapshot.errors ?? 0),
    },
    {
      key: "actions",
      header: "",
      kind: "actions",
      size: 120,
      minSize: 120,
      maxSize: 120,
      sortable: false,
      rowActions: true,
      accessor: (snapshot) => {
        const restorable =
          snapshot.phase === "Completed" ||
          snapshot.phase === "PartiallyFailed";
        return (
          <div className="flex items-center justify-end gap-1.5">
            <ActionButton
              {...BARE_BUTTON}
              tooltip={
                !canWrite
                  ? undefined
                  : !restorable
                    ? "Snapshot is not in a restorable state"
                    : "Restore"
              }
              disabledReason={!canWrite ? disabledReason : undefined}
              onClick={() => onRestore(snapshot)}
              disabled={!canWrite || !restorable}
              className="inline-flex items-center gap-1 h-7 px-2 rounded-sm text-xs text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50 disabled:cursor-not-allowed font-normal"
            >
              <RotateCcw className="h-3.5 w-3.5" />
              Restore
            </ActionButton>
            <ActionButton
              {...BARE_BUTTON}
              tooltip={canWrite ? "Delete" : undefined}
              disabledReason={canWrite ? undefined : disabledReason}
              aria-label="Delete"
              onClick={() => onDelete(snapshot)}
              disabled={!canWrite}
              className="inline-flex items-center justify-center h-7 w-7 rounded-sm text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors disabled:opacity-50 disabled:cursor-not-allowed font-normal"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </ActionButton>
          </div>
        );
      },
    },
  ];
  return (
    <DataTable
      data={snapshots}
      columns={columns}
      keyExtractor={(snapshot) => snapshot.id}
      loading={state.loading}
      isError={state.isError}
      error={state.error}
      onRetry={state.onRetry}
      errorMessage="Failed to load snapshots"
      emptyState={{
        title: "No snapshots yet",
        description:
          "Create one on demand, or set up a schedule to capture them automatically.",
      }}
      searchPlaceholder="Search snapshots…"
    />
  );
}
