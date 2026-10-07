import { Activity, Pencil, Send, Trash2 } from "lucide-react";
import type { Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import type { SIEMForwarder } from "@/types";
import { BareButton } from "@/components/form/bare-button";
import { CappedChips, RelativeTime } from "@/components/admin/table-cells";
import { transportLabel } from "./-options";

export function siemForwarderColumns({
  onStatus,
  onTest,
  testDisabled,
  onEdit,
  onDelete,
}: {
  onStatus: (row: SIEMForwarder) => void;
  onTest: (row: SIEMForwarder) => void;
  testDisabled: boolean;
  onEdit: (row: SIEMForwarder) => void;
  onDelete: (row: SIEMForwarder) => void;
}): Column<SIEMForwarder>[] {
  return [
    {
      key: "name",
      header: "Name",
      kind: "name",
      accessor: (row) => (
        <div className="min-w-0">
          <p className="truncate font-medium text-foreground">{row.name}</p>
          <p className="truncate font-mono text-2xs text-muted-foreground">
            {row.endpoint}
          </p>
        </div>
      ),
    },
    {
      key: "transport",
      header: "Transport",
      kind: "badge",
      size: 104,
      sortable: false,
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded-sm bg-muted text-muted-foreground">
          {transportLabel(row.transport)}
        </span>
      ),
      sortAccessor: (row) => row.transport,
    },
    {
      key: "filters",
      header: "Event filters",
      kind: "badge",
      size: 140,
      minSize: 130,
      maxSize: 280,
      sortable: false,
      accessor: (row) => (
        <CappedChips
          items={row.eventFilters ?? []}
          max={1}
          mono
          empty="All events"
        />
      ),
      searchAccessor: (row) => (row.eventFilters ?? []).join(" "),
    },
    {
      key: "status",
      header: "Status",
      kind: "status",
      size: 98,
      accessor: (row) => (
        <StatusBadge
          status={row.enabled ? "active" : "disconnected"}
          label={row.enabled ? "Enabled" : "Disabled"}
          size="sm"
        />
      ),
      sortAccessor: (row) => (row.enabled ? "1" : "0"),
    },
    {
      key: "updated",
      header: "Updated",
      kind: "age",
      size: 105,
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          <RelativeTime value={row.updatedAt} />
        </span>
      ),
    },
    {
      key: "actions",
      header: "",
      kind: "actions",
      size: 120,
      maxSize: 120,
      sortable: false,
      accessor: (row) => (
        <div className="flex items-center gap-1">
          <BareButton
            aria-label="View status"
            onClick={() => onStatus(row)}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            tooltip="View status"
          >
            <Activity className="h-3.5 w-3.5" />
          </BareButton>
          <BareButton
            aria-label="Send test event"
            onClick={() => onTest(row)}
            disabled={testDisabled}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
            tooltip="Send test event"
          >
            <Send className="h-3.5 w-3.5" />
          </BareButton>
          <BareButton
            aria-label="Edit forwarder"
            onClick={() => onEdit(row)}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            tooltip="Edit forwarder"
          >
            <Pencil className="h-3.5 w-3.5" />
          </BareButton>
          <BareButton
            aria-label="Delete forwarder"
            onClick={() => onDelete(row)}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            tooltip="Delete forwarder"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </BareButton>
        </div>
      ),
    },
  ];
}
