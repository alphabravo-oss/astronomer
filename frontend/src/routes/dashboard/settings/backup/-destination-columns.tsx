import { Pencil, Play, Trash2 } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import type { Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { cronToHuman } from "@/components/backups/cron";
import { RelativeTime } from "@/components/admin/table-cells";
import type { ManagementBackupDestinationView } from "@/lib/api/settings";

export function destinationColumns({
  onRun,
  runDisabled,
  onEdit,
  onRemove,
}: {
  onRun: (row: ManagementBackupDestinationView) => void;
  runDisabled: boolean;
  onEdit: (row: ManagementBackupDestinationView) => void;
  onRemove: (row: ManagementBackupDestinationView) => void;
}): Column<ManagementBackupDestinationView>[] {
  return [
    {
      key: "name",
      header: "Name",
      kind: "name",
      accessor: (row) => (
        <div className="min-w-0">
          <p className="truncate text-sm text-foreground">{row.name}</p>
          <p className="truncate font-mono text-2xs text-muted-foreground">
            {row.bucket}
          </p>
        </div>
      ),
    },
    {
      key: "schedule",
      header: "Schedule",
      kind: "text",
      size: 150,
      minSize: 120,
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.schedule ? cronToHuman(row.schedule) : "—"}
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
          status={
            row.reconcileStatus === "failed"
              ? "error"
              : row.reconcileStatus === "running" ||
                  row.reconcileStatus === "pending" ||
                  row.reconcileStatus === "retrying"
                ? "connecting"
                : row.enabled
                  ? "active"
                  : "disconnected"
          }
          label={
            row.reconcileStatus === "failed"
              ? "failed"
              : row.reconcileStatus === "running" ||
                  row.reconcileStatus === "pending" ||
                  row.reconcileStatus === "retrying"
                ? row.reconcileStatus
                : row.enabled
                  ? row.source === "helm"
                    ? "helm"
                    : "scheduled"
                  : "paused"
          }
          size="sm"
        />
      ),
    },
    {
      key: "last",
      header: "Last job",
      kind: "age",
      size: 105,
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          <RelativeTime
            value={row.lastJob?.completionTime ?? row.lastJob?.startTime}
            fallback="never"
          />
        </span>
      ),
    },
    {
      key: "actions",
      header: "",
      kind: "actions",
      size: 112,
      maxSize: 112,
      sortable: false,
      accessor: (row) =>
        row.readOnly ? (
          <span className="text-2xs text-muted-foreground">Helm-managed</span>
        ) : (
          <div className="flex items-center justify-end gap-1">
            <ActionButton
              intent="ghost"
              size="icon"
              aria-label="Run"
              tooltip="Run backup now"
              icon={<Play className="h-3.5 w-3.5" />}
              onClick={() => onRun(row)}
              disabled={runDisabled || !row.enabled}
            />
            <ActionButton
              intent="ghost"
              size="icon"
              aria-label="Edit"
              tooltip="Edit destination"
              icon={<Pencil className="h-3.5 w-3.5" />}
              onClick={() => onEdit(row)}
            />
            <ActionButton
              intent="ghost"
              size="icon"
              aria-label="Remove"
              tooltip="Remove destination"
              icon={<Trash2 className="h-3.5 w-3.5" />}
              onClick={() => onRemove(row)}
            />
          </div>
        ),
    },
  ];
}
