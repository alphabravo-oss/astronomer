/** Column definitions for the Gatekeeper constraints table. */
import { Trash2 } from "lucide-react";
import type { Column } from "@/components/ui/data-table";
import { ActionButton } from "@/components/ui/action-button";
import { Tooltip } from "@/components/ui/tooltip";
import { BARE_BUTTON } from "@/lib/bare-button";
import { cn } from "@/lib/utils";
import type { GatekeeperConstraint } from "@/types";

export function gatekeeperColumns({
  canWrite,
  reason,
  onDelete,
}: {
  canWrite: boolean;
  reason?: string;
  onDelete: (row: GatekeeperConstraint) => void;
}): Column<GatekeeperConstraint>[] {
  return [
    {
      key: "name",
      header: "Name",
      kind: "name",
      minSize: 200,
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.name}</p>
          <p className="text-2xs font-mono text-muted-foreground">{row.kind}</p>
        </div>
      ),
    },
    {
      key: "source",
      header: "Source",
      kind: "badge",
      size: 100,
      accessor: (row) => (
        <span
          className={cn(
            "text-xs px-2 py-0.5 rounded-sm capitalize font-medium",
            row.source === "custom"
              ? "bg-status-info/10 text-status-info"
              : "bg-muted text-muted-foreground",
          )}
        >
          {row.source}
        </span>
      ),
      sortAccessor: (row) => row.source,
    },
    {
      key: "enforcement",
      header: "Enforcement",
      kind: "badge",
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded-sm bg-muted text-muted-foreground font-mono">
          {row.enforcementAction || "—"}
        </span>
      ),
      sortAccessor: (row) => row.enforcementAction,
    },
    {
      key: "violations",
      header: "Violations",
      kind: "count",
      accessor: (row) => (
        <span
          className={cn(
            "tabular-nums text-sm font-medium",
            row.violationCount > 0
              ? "text-status-error"
              : "text-muted-foreground",
          )}
        >
          {row.violationCount}
        </span>
      ),
      sortAccessor: (row) => row.violationCount,
    },
    {
      key: "status",
      header: "Status",
      kind: "status",
      minSize: 160,
      size: 200,
      maxSize: 280,
      accessor: (row) =>
        row.source === "custom" ? (
          <div className="space-y-0.5">
            <span
              className={cn(
                "inline-flex rounded-sm px-2 py-0.5 text-xs font-medium capitalize",
                row.syncStatus === "synced"
                  ? "bg-status-success/10 text-status-success"
                  : row.syncStatus === "failed"
                    ? "bg-status-error/10 text-status-error"
                    : "bg-status-warning/10 text-status-warning",
              )}
            >
              {row.desiredState === "absent"
                ? row.syncStatus === "synced"
                  ? "deleted"
                  : "deleting"
                : row.syncStatus}
            </span>
            {row.lastError ? (
              <Tooltip content={row.lastError}>
                <p
                  data-cell-clip=""
                  className="truncate text-2xs text-status-error"
                >
                  {row.lastError}
                </p>
              </Tooltip>
            ) : null}
          </div>
        ) : (
          <span className="text-xs text-muted-foreground">managed</span>
        ),
      sortAccessor: (row) => row.syncStatus ?? "",
    },
    {
      key: "actions",
      header: "",
      kind: "actions",
      sortable: false,
      accessor: (row) =>
        row.source === "custom" && row.desiredState !== "absent" ? (
          <ActionButton
            {...BARE_BUTTON}
            tooltip={canWrite ? "Delete constraint" : undefined}
            disabledReason={canWrite ? undefined : reason}
            aria-label="Delete constraint"
            onClick={(e) => {
              e.stopPropagation();
              if (canWrite) onDelete(row);
            }}
            disabled={!canWrite}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-muted-foreground inline-block font-normal"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </ActionButton>
        ) : null,
    },
  ];
}
