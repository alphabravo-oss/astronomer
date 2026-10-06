import { AlertRuleInspection } from "./-alert-investigation";
import { useState } from "react";
import { Pencil, Trash2 } from "lucide-react";
import { useAlertRules, useDeleteAlertRule } from "@/lib/hooks/alerting";
import { NameSubCell } from "@/components/ui/cell-primitives";
import { DataTable, type Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { ActionButton } from "@/components/ui/action-button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { cn, statusBgColor } from "@/lib/utils";
import type { AlertRule } from "@/types";

export function RulesTab({
  onEdit,
  clusterId,
}: {
  onEdit: (rule: AlertRule) => void;
  clusterId?: string;
}) {
  const { data: rules, isLoading, isError, refetch } = useAlertRules(clusterId);
  const deleteRule = useDeleteAlertRule();
  const [deleteRuleTarget, setDeleteRuleTarget] = useState<AlertRule | null>(
    null,
  );

  const columns: Column<AlertRule>[] = [
    {
      key: "name",
      header: "Rule",
      kind: "name",
      minSize: 220,
      accessor: (row) => (
        <NameSubCell title={row.name} subtitle={row.description} />
      ),
      sortAccessor: (row) => row.name,
    },
    {
      key: "type",
      header: "Type",
      kind: "badge",
      size: 96,
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded-sm bg-muted text-muted-foreground capitalize">
          {row.type}
        </span>
      ),
    },
    {
      key: "severity",
      header: "Severity",
      kind: "badge",
      size: 112,
      accessor: (row) => (
        <span
          className={cn(
            "text-xs px-2 py-0.5 rounded-sm capitalize font-medium",
            statusBgColor(row.severity),
          )}
        >
          {row.severity}
        </span>
      ),
    },
    ...(clusterId
      ? []
      : [
          {
            key: "cluster",
            header: "Cluster",
            kind: "text",
            size: 144,
            minSize: 112,
            accessor: (row: AlertRule) => (
              <span className="text-sm text-muted-foreground">
                {row.clusterName || "All"}
              </span>
            ),
          } as Column<AlertRule>,
        ]),
    {
      key: "status",
      header: "Status",
      kind: "status",
      size: 112,
      accessor: (row) => (
        <StatusBadge
          status={row.enabled ? "active" : "disconnected"}
          label={row.enabled ? "Enabled" : "Disabled"}
        />
      ),
    },
    {
      key: "activeAlerts",
      header: "Active",
      accessor: (row) => (
        <span
          className={cn(
            "tabular-nums text-sm font-medium",
            row.activeAlerts > 0
              ? "text-status-error"
              : "text-muted-foreground",
          )}
        >
          {row.activeAlerts}
        </span>
      ),
      sortAccessor: (row) => row.activeAlerts,
      kind: "count",
    },
    {
      key: "actions",
      header: "",
      accessor: (row) => (
        <div className="flex items-center gap-1">
          <ActionButton
            size="icon"
            intent="ghost"
            tooltip="Edit rule"
            onClick={() => onEdit(row)}
            icon={<Pencil className="h-3.5 w-3.5" />}
          />
          <ActionButton
            size="icon"
            intent="ghost"
            tooltip="Delete rule"
            onClick={() => setDeleteRuleTarget(row)}
            icon={<Trash2 className="h-3.5 w-3.5" />}
            className="hover:text-status-error hover:bg-status-error/10"
          />
        </div>
      ),
      sortable: false,
      kind: "actions",
      size: 88,
      minSize: 88,
      maxSize: 88,
    },
  ];

  return (
    <>
      <AlertRuleInspection />
      <DataTable
        data={rules || []}
        columns={columns}
        keyExtractor={(row) => row.id}
        searchPlaceholder="Search alert rules..."
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        emptyState={{
          title: "No alert rules configured",
          description: "Create the first item to configure this feature.",
        }}
      />
      <ConfirmDialog
        open={!!deleteRuleTarget}
        onClose={() => setDeleteRuleTarget(null)}
        onConfirm={() => {
          if (!deleteRuleTarget) return;
          deleteRule.mutate(deleteRuleTarget.id, {
            onSuccess: () => setDeleteRuleTarget(null),
          });
        }}
        title="Delete Alert Rule"
        description={`Delete the alert rule "${deleteRuleTarget?.name}"? This action cannot be undone.`}
        confirmText="Delete"
        variant="destructive"
        loading={deleteRule.isPending}
      />
    </>
  );
}
