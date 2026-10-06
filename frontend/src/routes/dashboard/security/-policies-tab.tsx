import { Info, Play, Trash2 } from "lucide-react";
import {
  DataTable,
  type Column,
  type DataTableProps,
} from "@/components/ui/data-table";
import { TimestampCell } from "@/components/ui/cell-primitives";
import { StatusBadge } from "@/components/ui/status-badge";
import { cn } from "@/lib/utils";
import type { ClusterSecurityPolicy, PodSecurityLevel } from "@/types";
import { psaLevelColors } from "./-psa-constants";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { ActionButton } from "@/components/ui/action-button";

export type SecurityPolicyRow = ClusterSecurityPolicy & {
  clusterName: string;
  templateName: string;
  enforceLevel: PodSecurityLevel | "unknown";
  auditLevel: PodSecurityLevel | "unknown";
  warnLevel: PodSecurityLevel | "unknown";
};

function policyColumns(
  onApply: (row: SecurityPolicyRow) => void,
  applyPending: boolean,
  onRemove: (row: SecurityPolicyRow) => void,
  canUpdate: boolean,
  canDelete: boolean,
): Column<SecurityPolicyRow>[] {
  return [
    {
      key: "cluster",
      header: "Cluster",
      kind: "name",
      minSize: 200,
      accessor: (row) => (
        <span className="font-medium text-foreground text-sm">
          {row.clusterName}
        </span>
      ),
    },
    {
      key: "template",
      header: "Template",
      kind: "text",
      size: 200,
      minSize: 160,
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">
          {row.templateName}
        </span>
      ),
    },
    {
      key: "enforce",
      header: "Enforce",
      kind: "badge",
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
      kind: "badge",
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
      kind: "badge",
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
      key: "syncStatus",
      header: "Sync Status",
      kind: "status",
      accessor: (row) => <StatusBadge status={row.syncStatus} />,
    },
    {
      key: "appliedAt",
      header: "Applied",
      kind: "age",
      size: 112,
      accessor: (row) =>
        row.appliedAt ? (
          <TimestampCell value={row.appliedAt} />
        ) : (
          <span className="text-xs text-muted-foreground">Not applied</span>
        ),
    },
    {
      key: "actions",
      header: "",
      accessor: (row) => (
        <div className="flex items-center gap-1">
          <ActionButton
            onClick={() => onApply(row)}
            disabled={applyPending || !canUpdate}
            disabledReason={!canUpdate ? "Requires security:update" : undefined}
            className="inline-flex items-center gap-1 px-2 py-1 rounded-sm text-xs text-muted-foreground
              hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
            tooltip="Apply to cluster"
          >
            <Play className="h-3 w-3" />
            Apply
          </ActionButton>
          <ActionButton
            onClick={() => onRemove(row)}
            disabled={!canDelete}
            disabledReason={!canDelete ? "Requires security:delete" : undefined}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            tooltip="Remove policy"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </ActionButton>
        </div>
      ),
      sortable: false,
      kind: "actions",
      size: 128,
      minSize: 128,
      maxSize: 128,
    },
  ];
}

export function PoliciesTab({
  rows,
  loading,
  serverSide,
  onApply,
  applyPending,
  onRemove,
}: {
  rows: SecurityPolicyRow[];
  loading: boolean;
  serverSide: DataTableProps<SecurityPolicyRow>["serverSide"];
  onApply: (row: SecurityPolicyRow) => void;
  applyPending: boolean;
  onRemove: (row: SecurityPolicyRow) => void;
}) {
  const update = usePermissionDecision("security", "update");
  const remove = usePermissionDecision("security", "delete");
  const columns = policyColumns(
    onApply,
    applyPending,
    onRemove,
    update.allowed,
    remove.allowed,
  );
  return (
    <div className="space-y-4">
      <div className="rounded-lg border border-border bg-muted/30 p-4 flex items-start gap-2">
        <Info className="h-4 w-4 text-primary mt-0.5 shrink-0" />
        <p className="text-xs text-muted-foreground leading-relaxed">
          A security policy binds a PSA template to a cluster. Until you assign
          and apply a template here, Pod Security Admission is not enforced —
          defining or seeding a template alone changes nothing on your clusters.
        </p>
      </div>
      <DataTable
        data={rows}
        columns={columns}
        keyExtractor={(row) => row.id}
        searchPlaceholder="Search cluster policies..."
        loading={loading}
        serverSide={serverSide}
        searchable={false}
        emptyState={{
          title: "No security policies assigned",
          description:
            "Resources will appear here when they are available in this scope.",
        }}
      />
    </div>
  );
}
