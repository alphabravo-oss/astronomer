import { Pencil, Trash2 } from "lucide-react";
import type { Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { ActionMenu } from "@/components/ui/action-menu";
import { getConnectorMeta } from "@/components/auth/connector-meta";
import type { DexConnector } from "@/types";

export function connectorColumns(actions: {
  onEdit: (row: DexConnector) => void;
  onDelete: (row: DexConnector) => void;
}): Column<DexConnector>[] {
  return [
    {
      key: "type",
      header: "Type",
      kind: "badge",
      size: 136,
      minSize: 120,
      maxSize: 200,
      accessor: (row) => {
        const meta = getConnectorMeta(row.type);
        const Icon = meta.icon;
        return (
          <div className="flex items-center gap-2">
            <Icon className="h-4 w-4 text-muted-foreground shrink-0" />
            <span className="text-sm text-foreground">
              {meta.label || row.type}
            </span>
          </div>
        );
      },
      sortAccessor: (row) => row.type,
    },
    {
      key: "name",
      header: "Name",
      kind: "id",
      size: 144,
      minSize: 120,
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.name}
        </span>
      ),
      sortAccessor: (row) => row.name,
    },
    {
      key: "displayName",
      header: "Display Name",
      kind: "name",
      accessor: (row) => (
        <span className="text-sm text-foreground">
          {row.displayName || "—"}
        </span>
      ),
      sortAccessor: (row) => row.displayName,
    },
    {
      key: "status",
      header: "Status",
      kind: "status",
      size: 104,
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
      key: "actions",
      header: "",
      kind: "actions",
      rowActions: true,
      sortable: false,
      accessor: (row) => (
        <ActionMenu
          items={[
            {
              label: "Edit",
              icon: <Pencil className="h-3.5 w-3.5" />,
              onClick: () => actions.onEdit(row),
            },
            {
              label: "Delete",
              icon: <Trash2 className="h-3.5 w-3.5" />,
              onClick: () => actions.onDelete(row),
              variant: "destructive",
              separator: true,
            },
          ]}
        />
      ),
    },
  ];
}
