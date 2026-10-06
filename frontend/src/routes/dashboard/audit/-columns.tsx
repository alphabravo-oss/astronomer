import { cn, formatDate, formatRelativeTime } from "@/lib/utils";
import type { Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { CappedChips } from "@/components/admin/table-cells";
import type { AuditLogEntry } from "@/types";

export function auditColumns(
  usersById: Map<string, string>,
): Column<AuditLogEntry>[] {
  return [
    {
      key: "time",
      header: "Time",
      kind: "date",
      size: 176,
      maxSize: 220,
      accessor: (row) => (
        <div>
          <div className="text-xs font-mono text-foreground">
            {formatDate(rowTime(row))}
          </div>
          <div className="text-2xs text-muted-foreground">
            {formatRelativeTime(rowTime(row))}
          </div>
        </div>
      ),
      sortAccessor: (row) => rowTime(row),
    },
    {
      key: "actor",
      header: "Actor",
      kind: "name",
      grow: false,
      size: 152,
      minSize: 128,
      accessor: (row) => (
        <div>
          <div className="truncate text-sm text-foreground">
            {actorLabel(row, usersById)}
          </div>
          <div className="truncate text-2xs text-muted-foreground">
            {row.actorAuthMethod || row.source || "—"}
          </div>
        </div>
      ),
      sortAccessor: (row) => actorLabel(row, usersById),
    },
    {
      key: "action",
      header: "Action",
      kind: "name",
      minSize: 200,
      accessor: (row) => (
        <div>
          <div className="truncate font-mono text-xs text-foreground">
            {row.action}
          </div>
          <span
            className={cn(
              "mt-1 inline-flex rounded-sm px-1.5 py-0.5 text-2xs",
              actionClassStyle(row.actionClass),
            )}
          >
            {row.actionClass || "mutation"}
          </span>
        </div>
      ),
      sortAccessor: (row) => row.action,
    },
    {
      key: "target",
      header: "Target",
      kind: "name",
      grow: false,
      size: 152,
      minSize: 128,
      accessor: (row) => (
        <div>
          <div className="truncate text-sm text-foreground">
            {targetName(row)}
          </div>
          <div className="truncate text-2xs text-muted-foreground">
            {row.resourceType || "—"}
          </div>
        </div>
      ),
      sortAccessor: targetName,
    },
    {
      key: "scope",
      header: "Scope",
      kind: "badge",
      size: 176,
      minSize: 140,
      maxSize: 240,
      accessor: (row) => {
        return (
          <CappedChips items={scopeLabels(row)} max={1} mono empty="global" />
        );
      },
      searchAccessor: (row) => scopeLabels(row).join(" ") || "global",
      sortAccessor: (row) => scopeLabels(row).join(" "),
    },
    {
      key: "result",
      header: "Result",
      kind: "status",
      size: 96,
      accessor: (row) => (
        <div className="space-y-1">
          <StatusBadge
            status={statusForBadge(row.status)}
            label={row.status || "success"}
            size="sm"
          />
          <div className="text-2xs text-muted-foreground">
            {row.statusCode ?? 0}
          </div>
        </div>
      ),
      sortAccessor: (row) => row.statusCode ?? 0,
      searchAccessor: (row) =>
        `${row.status || "success"} ${row.statusCode ?? 0}`,
    },
  ];
}

export function rowTime(row: AuditLogEntry): string {
  return row.createdAt || row.timestamp;
}

export function actorLabel(
  row: AuditLogEntry,
  usersById: Map<string, string>,
): string {
  const id = row.userId || row.user;
  if (id && usersById.has(id)) return usersById.get(id) || id;
  return row.user || row.userId || "system";
}

export function targetName(row: AuditLogEntry): string {
  return row.resourceName || row.resourceId || row.path || "—";
}

export function rowDetail(row: AuditLogEntry): Record<string, unknown> {
  return row.detail && typeof row.detail === "object" ? row.detail : {};
}

export function detailString(row: AuditLogEntry, ...keys: string[]): string {
  const detail = rowDetail(row);
  for (const key of keys) {
    const value = detail[key];
    if (typeof value === "string" && value.trim()) return value;
  }
  return "";
}

export function scopeLabels(row: AuditLogEntry): string[] {
  const out = new Set<string>();
  const cluster =
    row.resourceType === "cluster"
      ? row.resourceId || row.resourceName
      : detailString(row, "cluster_id", "clusterId", "cluster", "cluster_name");
  const project =
    row.resourceType === "project"
      ? row.resourceId || row.resourceName
      : detailString(row, "project_id", "projectId", "project", "project_name");
  if (cluster) out.add(`cluster:${cluster}`);
  if (project) out.add(`project:${project}`);
  return Array.from(out);
}

export function actionClassStyle(actionClass?: string): string {
  switch (actionClass) {
    case "read":
      return "bg-info/10 text-info";
    case "auth":
      return "bg-status-warning/10 text-status-warning";
    case "system":
      return "bg-muted text-muted-foreground";
    default:
      return "bg-primary/10 text-primary";
  }
}

export function statusForBadge(status?: string): string {
  if (status === "error" || status === "failure") return "error";
  return "active";
}
