import { Lock, Pencil, Shield, Trash2 } from "lucide-react";
import {
  DataTable,
  type Column,
  type DataTableProps,
} from "@/components/ui/data-table";
import { cn } from "@/lib/utils";
import type { PodSecurityTemplate } from "@/types";
import { psaLevelColors } from "./-psa-constants";
import { PSAExplainer } from "./-psa-explainer";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { ActionButton } from "@/components/ui/action-button";

function templateColumns(
  onEdit: (row: PodSecurityTemplate) => void,
  onDelete: (row: PodSecurityTemplate) => void,
  canUpdate: boolean,
  canDelete: boolean,
): Column<PodSecurityTemplate>[] {
  return [
    {
      key: "name",
      header: "Name",
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Shield className="h-4 w-4 text-muted-foreground" />
          <span className="font-medium text-foreground">{row.name}</span>
          {row.isDefault && (
            <span className="text-2xs px-1.5 py-0.5 rounded-sm bg-primary/10 text-primary font-medium">
              Default
            </span>
          )}
          {row.isBuiltin && (
            <span className="inline-flex items-center gap-1 text-2xs px-1.5 py-0.5 rounded-sm bg-accent text-muted-foreground font-medium">
              <Lock className="h-2.5 w-2.5" />
              Built-in
            </span>
          )}
        </div>
      ),
    },
    {
      key: "enforce",
      header: "Enforce",
      accessor: (row) => (
        <span
          className={cn(
            "text-xs px-2 py-0.5 rounded-sm font-medium capitalize",
            psaLevelColors[row.enforceLevel],
          )}
        >
          {row.enforceLevel}
        </span>
      ),
    },
    {
      key: "audit",
      header: "Audit",
      accessor: (row) => (
        <span
          className={cn(
            "text-xs px-2 py-0.5 rounded-sm font-medium capitalize",
            psaLevelColors[row.auditLevel],
          )}
        >
          {row.auditLevel}
        </span>
      ),
    },
    {
      key: "warn",
      header: "Warn",
      accessor: (row) => (
        <span
          className={cn(
            "text-xs px-2 py-0.5 rounded-sm font-medium capitalize",
            psaLevelColors[row.warnLevel],
          )}
        >
          {row.warnLevel}
        </span>
      ),
    },
    {
      key: "description",
      header: "Description",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground truncate max-w-[200px] block">
          {row.description || "--"}
        </span>
      ),
      sortable: false,
    },
    {
      key: "actions",
      header: "",
      accessor: (row) => (
        <div className="flex items-center gap-1">
          <ActionButton
            onClick={() => onEdit(row)}
            disabled={row.isBuiltin || !canUpdate}
            disabledReason={!canUpdate ? "Requires security:update" : undefined}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-foreground hover:bg-accent
              transition-colors disabled:opacity-30 disabled:pointer-events-none"
            title={
              row.isBuiltin
                ? "Built-in templates cannot be edited"
                : "Edit template"
            }
          >
            <Pencil className="h-3.5 w-3.5" />
          </ActionButton>
          <ActionButton
            onClick={() => onDelete(row)}
            disabled={row.isDefault || row.isBuiltin || !canDelete}
            disabledReason={!canDelete ? "Requires security:delete" : undefined}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-status-error hover:bg-status-error/10
              transition-colors disabled:opacity-30 disabled:pointer-events-none"
            title={
              row.isBuiltin
                ? "Built-in templates cannot be deleted"
                : "Delete template"
            }
          >
            <Trash2 className="h-3.5 w-3.5" />
          </ActionButton>
        </div>
      ),
      sortable: false,
    },
  ];
}

export function TemplatesTab({
  templates,
  loading,
  serverSide,
  onEdit,
  onDelete,
}: {
  templates: PodSecurityTemplate[];
  loading: boolean;
  serverSide: DataTableProps<PodSecurityTemplate>["serverSide"];
  onEdit: (row: PodSecurityTemplate) => void;
  onDelete: (row: PodSecurityTemplate) => void;
}) {
  const update = usePermissionDecision("security", "update");
  const remove = usePermissionDecision("security", "delete");
  const columns = templateColumns(
    onEdit,
    onDelete,
    update.allowed,
    remove.allowed,
  );
  return (
    <div className="space-y-4">
      <PSAExplainer />
      <DataTable
        data={templates}
        columns={columns}
        keyExtractor={(row) => row.id}
        searchPlaceholder="Search templates..."
        loading={loading}
        serverSide={serverSide}
        searchable={false}
        emptyState={{
          title: "No PSA templates defined",
          description: "Create the first item to configure this feature.",
        }}
      />
    </div>
  );
}
