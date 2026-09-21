import { Plus, Trash2 } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { cn } from "@/lib/utils";
import type { NodeTaint } from "@/types";

const taintColumns: Column<NodeTaint>[] = [
  {
    key: "key",
    header: "Key",
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.key}
      </span>
    ),
  },
  {
    key: "value",
    header: "Value",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.value || "-"}
      </span>
    ),
  },
  {
    key: "effect",
    header: "Effect",
    accessor: (row) => (
      <span
        className={cn(
          "px-1.5 py-0.5 rounded-sm text-2xs",
          row.effect === "NoSchedule"
            ? "bg-status-warning/10 text-status-warning"
            : row.effect === "NoExecute"
              ? "bg-status-error/10 text-status-error"
              : "bg-muted text-muted-foreground",
        )}
      >
        {row.effect}
      </span>
    ),
  },
];

export function TaintsTab({
  taints,
  canUpdate,
  blockedReason,
  addTaintPending,
  removeTaintPending,
  onOpenAddTaint,
  onRemoveTaint,
}: {
  taints: NodeTaint[];
  canUpdate: boolean;
  blockedReason?: string;
  addTaintPending: boolean;
  removeTaintPending: boolean;
  onOpenAddTaint: () => void;
  onRemoveTaint: (taint: NodeTaint) => void;
}) {
  return (
    <div className="space-y-3">
      <div className="flex justify-end">
        <button
          onClick={onOpenAddTaint}
          disabled={addTaintPending || !canUpdate}
          title={blockedReason}
          className="inline-flex items-center gap-1.5 h-8 px-3 rounded-sm text-xs font-medium
            bg-primary text-primary-foreground hover:bg-primary/90 transition-colors disabled:cursor-not-allowed disabled:opacity-50"
        >
          <Plus className="h-3.5 w-3.5" /> Add Taint
        </button>
      </div>
      <DataTable
        data={taints}
        columns={[
          ...taintColumns,
          {
            key: "actions",
            header: "",
            accessor: (row) => (
              <button
                onClick={() => onRemoveTaint(row)}
                disabled={removeTaintPending || !canUpdate}
                title={blockedReason}
                className="p-1.5 rounded-sm text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors disabled:cursor-not-allowed disabled:opacity-50"
              >
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            ),
            sortable: false,
            align: "center" as const,
          },
        ]}
        keyExtractor={(r) => `${r.key}-${r.effect}`}
        emptyState={{
          title: "No taints on this node",
          description:
            "Resources will appear here when they are available in this scope.",
        }}
      />
    </div>
  );
}
