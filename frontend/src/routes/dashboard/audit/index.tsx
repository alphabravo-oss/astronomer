import { createFileRoute } from "@tanstack/react-router";

import { useId, useMemo, useState } from "react";
import { useDraft } from "@/lib/hooks/use-draft";
import {
  ChevronDown,
  Download,
  Filter,
  RefreshCw,
  Search,
  TerminalSquare,
  X,
} from "lucide-react";
import { Link as RouterLink } from "@tanstack/react-router";
import { DataTable } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { PageHeader, PageShell } from "@/components/ui/page";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import {
  ActivityDetailsDrawer,
  type ActivityDetailField,
} from "@/components/audit/activity-details-drawer";
import { useAuditLogs } from "@/lib/hooks/audit";
import { useSearchParam } from "@/lib/use-search-param";
import { pageCountLabel, pageTableCount } from "@/lib/api/pagination";
import { useEntityNames } from "@/lib/hooks/entity-names";
import { AuditScopeFilter } from "@/components/audit/scope-filter";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { PermissionState } from "@/components/ui/empty-state";
import { getAuditLogExportURL } from "@/lib/api/audit";
import { cn, formatDate, formatRelativeTime } from "@/lib/utils";
import { useDebouncedValue } from "@tanstack/react-pacer";
import type { AuditLogEntry } from "@/types";
import {
  PAGE_SIZE,
  auditFilterChips,
  buildAuditQuery,
  clearFilterValue,
  countActiveFilters,
  countAdvancedFilters,
  emptyFilters,
  type AuditFilters,
} from "./-filters";
import { BareButton } from "@/components/form/bare-button";
import {
  auditColumns,
  rowTime,
  actorLabel,
  targetName,
  rowDetail,
  statusForBadge,
} from "./-columns";

function useAuditNames(
  rows: AuditLogEntry[],
  clusterId: string,
  projectId: string,
) {
  const names = useEntityNames({
    userIds: rows.map((row) => row.userId),
    clusterIds: [clusterId],
    projectIds: [projectId],
  });
  return {
    usersById: new Map(
      names.users.map((user) => [
        user.id,
        user.displayName || user.username || user.email,
      ]),
    ),
    clusterNames: Object.fromEntries(
      names.clusters.map((cluster) => [
        cluster.id,
        cluster.displayName || cluster.name,
      ]),
    ),
    projectNames: Object.fromEntries(
      names.projects.map((project) => [
        project.id,
        project.displayName || project.name,
      ]),
    ),
  };
}

function AuditLogPage() {
  const [filters, setFilters] = useState<AuditFilters>(emptyFilters);
  const [qInput, setQInput] = useSearchParam("q", { debounceMs: 200 });
  const [qDebounced] = useDebouncedValue(qInput, { wait: 200 });
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const advancedFiltersId = useId();
  const [page, setPage] = useDraft(0, qDebounced);
  const [selected, setSelected] = useState<AuditLogEntry | null>(null);
  const queryParams = useMemo(
    () => buildAuditQuery({ ...filters, q: qDebounced }, page),
    [filters, qDebounced, page],
  );
  const read = usePermissionDecision("audit", "read");
  const auditQuery = useAuditLogs(queryParams, { enabled: read.allowed });
  const rows =
    !read.allowed || auditQuery.isError ? [] : (auditQuery.data?.data ?? []);
  const total = pageCountLabel(
    auditQuery.isError ? undefined : auditQuery.data,
  );
  const { usersById, clusterNames, projectNames } = useAuditNames(
    selected && read.allowed && !auditQuery.isError
      ? [...rows, selected]
      : rows,
    filters.clusterId,
    filters.projectId,
  );

  const activeFilterCount = countActiveFilters({ ...filters, q: qInput });
  const advancedCount = countAdvancedFilters(filters);
  const chips = auditFilterChips(
    { ...filters, q: qInput },
    { clusters: clusterNames, projects: projectNames },
  );
  const exportHref = getAuditLogExportURL({
    ...queryParams,
    limit: 500,
    offset: 0,
  });

  const updateFilter = <K extends keyof AuditFilters>(
    key: K,
    value: AuditFilters[K],
  ) => {
    if (key === "q") {
      setQInput(value);
      return;
    }
    setFilters((current) => ({ ...current, [key]: value }));
    setPage(0);
  };

  const clearAll = () => {
    setFilters(emptyFilters);
    setQInput("");
    setPage(0);
  };

  const columns = useMemo(() => auditColumns(usersById), [usersById]);

  if (!read.allowed) return <PermissionState permission="audit:read" />;
  return (
    <PageShell>
      <PageHeader
        title="Audit Log"
        description={
          filters.audience === "system"
            ? `${total.toLocaleString()} automated system events`
            : filters.audience === "all"
              ? `${total.toLocaleString()} events`
              : `${total.toLocaleString()} operator actions`
        }
        actions={
          <div className="flex items-center gap-2">
            <RouterLink
              to="/dashboard/audit/shell-sessions"
              className="inline-flex h-9 items-center gap-2 rounded-md border border-border bg-background px-4 text-sm font-medium text-foreground hover:bg-accent"
            >
              <TerminalSquare className="h-4 w-4" />
              Shell sessions
            </RouterLink>
            <a
              href={exportHref}
              className="inline-flex h-9 items-center gap-2 rounded-md border border-border bg-background px-4 text-sm font-medium text-foreground hover:bg-accent"
            >
              <Download className="h-4 w-4" />
              Export
            </a>
            <ActionButton
              icon={
                <RefreshCw
                  className={cn(
                    "h-4 w-4",
                    auditQuery.isFetching && "animate-spin",
                  )}
                />
              }
              onClick={() => auditQuery.refetch()}
            >
              Refresh
            </ActionButton>
          </div>
        }
      />

      <div className="space-y-2">
        <div className="flex flex-col gap-2 lg:flex-row lg:items-center">
          <div className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={qInput}
              onChange={(e) => updateFilter("q", e.target.value)}
              placeholder="Search actor, action, or resource…"
              className="pl-9"
              aria-label="Search audit log"
            />
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Select
              value={filters.audience}
              onChange={(e) => updateFilter("audience", e.target.value)}
              containerClassName="w-42"
              aria-label="Activity"
            >
              <option value="people">People</option>
              <option value="system">System</option>
              <option value="all">Everything</option>
            </Select>
            <Select
              value={filters.actionClass}
              onChange={(e) => updateFilter("actionClass", e.target.value)}
              containerClassName="w-34"
              aria-label="Event class"
            >
              <option value="all">All kinds</option>
              <option value="mutation">Changes</option>
              <option value="auth">Sign-in</option>
              <option value="read">Reads</option>
            </Select>
            <Select
              value={filters.result}
              onChange={(e) => updateFilter("result", e.target.value)}
              containerClassName="w-34"
              aria-label="Result"
            >
              <option value="all">Any result</option>
              <option value="success">Success</option>
              <option value="failure">Failure</option>
              <option value="error">Error</option>
            </Select>
            <ActionButton
              intent={advancedOpen || advancedCount > 0 ? "default" : "ghost"}
              icon={<Filter className="h-4 w-4" />}
              onClick={() => setAdvancedOpen((open) => !open)}
              aria-expanded={advancedOpen}
              aria-controls={advancedFiltersId}
            >
              Filters
              {advancedCount > 0 ? ` (${advancedCount})` : ""}
              <ChevronDown
                className={cn(
                  "h-3.5 w-3.5 transition-transform",
                  advancedOpen && "rotate-180",
                )}
              />
            </ActionButton>
            {activeFilterCount > 0 && (
              <ActionButton
                intent="ghost"
                icon={<X className="h-4 w-4" />}
                onClick={clearAll}
              >
                Clear
              </ActionButton>
            )}
          </div>
        </div>

        {chips.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {chips.map((chip) => (
              <BareButton
                key={chip.key}
                onClick={() =>
                  updateFilter(chip.key, clearFilterValue(chip.key))
                }
                className="inline-flex items-center gap-1 rounded-full border border-border bg-card px-2 py-0.5 text-xs text-muted-foreground hover:text-foreground"
              >
                {chip.label}
                <X className="h-3 w-3" />
              </BareButton>
            ))}
          </div>
        )}

        {advancedOpen && (
          <div
            id={advancedFiltersId}
            className="grid gap-3 rounded-lg border border-border bg-card p-3 md:grid-cols-2 xl:grid-cols-4"
          >
            <label className="space-y-1">
              <span className="text-xs font-medium text-muted-foreground">
                Actor
              </span>
              <Input
                value={filters.actor}
                onChange={(e) => updateFilter("actor", e.target.value)}
                placeholder="email, name, or user id"
              />
            </label>
            <label className="space-y-1">
              <span className="text-xs font-medium text-muted-foreground">
                Action
              </span>
              <Input
                value={filters.action}
                onChange={(e) => updateFilter("action", e.target.value)}
                placeholder="auth.login"
                className="font-mono"
              />
            </label>
            <label className="space-y-1">
              <span className="text-xs font-medium text-muted-foreground">
                Target
              </span>
              <Input
                value={filters.target}
                onChange={(e) => updateFilter("target", e.target.value)}
                placeholder="resource or path"
              />
            </label>
            <AuditScopeFilter
              kind="cluster"
              value={filters.clusterId}
              onChange={(id) => updateFilter("clusterId", id)}
            />
            <AuditScopeFilter
              kind="project"
              value={filters.projectId}
              onChange={(id) => updateFilter("projectId", id)}
            />
            <label className="space-y-1">
              <span className="text-xs font-medium text-muted-foreground">
                From
              </span>
              <Input
                type="datetime-local"
                value={filters.from}
                onChange={(e) => updateFilter("from", e.target.value)}
              />
            </label>
            <label className="space-y-1">
              <span className="text-xs font-medium text-muted-foreground">
                To
              </span>
              <Input
                type="datetime-local"
                value={filters.to}
                onChange={(e) => updateFilter("to", e.target.value)}
              />
            </label>
            <label className="space-y-1">
              <span className="text-xs font-medium text-muted-foreground">
                Correlation ID
              </span>
              <Input
                value={filters.correlationId}
                onChange={(e) => updateFilter("correlationId", e.target.value)}
                placeholder="optional"
                className="font-mono"
              />
            </label>
            <label className="space-y-1 xl:col-span-2">
              <span className="text-xs font-medium text-muted-foreground">
                Request ID
              </span>
              <Input
                value={filters.requestId}
                onChange={(e) => updateFilter("requestId", e.target.value)}
                placeholder="optional"
                className="font-mono"
              />
            </label>
          </div>
        )}
      </div>

      <DataTable
        data={rows}
        columns={columns.map((column) => ({ ...column, sortable: false }))}
        keyExtractor={(row) => row.id}
        searchable={false}
        exportCsv={{ filename: "audit-log" }}
        pageSize={PAGE_SIZE}
        loading={auditQuery.isLoading}
        isError={auditQuery.isError}
        error={auditQuery.error}
        permission="audit:read"
        onRetry={() => auditQuery.refetch()}
        emptyState={{
          title: "No audit events available",
          description:
            "New observations will appear here as they are reported.",
        }}
        onRowClick={setSelected}
        serverSide={{
          ...pageTableCount(auditQuery.isError ? undefined : auditQuery.data),
          pagination: { pageIndex: page, pageSize: PAGE_SIZE },
          onPaginationChange: (next) => setPage(next.pageIndex),
        }}
      />

      {selected && !auditQuery.isError && (
        <AuditDetailsDrawer
          row={selected}
          usersById={usersById}
          onClose={() => setSelected(null)}
        />
      )}
    </PageShell>
  );
}

function AuditDetailsDrawer({
  row,
  usersById,
  onClose,
}: {
  row: AuditLogEntry;
  usersById: Map<string, string>;
  onClose: () => void;
}) {
  const detail = rowDetail(row);
  const fields: ActivityDetailField[] = [
    { label: "ID", value: row.id },
    { label: "Time", value: rowTime(row) ? formatDate(rowTime(row)) : "—" },
    { label: "Actor", value: actorLabel(row, usersById) },
    { label: "Auth", value: row.actorAuthMethod || "—" },
    { label: "Action", value: row.action },
    { label: "Class", value: row.actionClass || "mutation" },
    {
      label: "Resource",
      value: `${row.resourceType || "—"}/${targetName(row)}`,
    },
    { label: "Method", value: row.httpMethod || "—" },
    {
      label: "Status",
      value: `${row.status || "success"} (${row.statusCode ?? 0})`,
    },
    { label: "Duration", value: `${row.durationMs ?? 0}ms` },
    { label: "Source", value: row.source || "—" },
    { label: "IP", value: row.sourceIP || row.ipAddress || "—" },
    { label: "Request", value: row.requestId || "—" },
    { label: "Correlation", value: row.correlationId || "—" },
    { label: "Path", value: row.path || "—" },
  ];

  return (
    <ActivityDetailsDrawer
      title={row.action}
      onClose={onClose}
      subtitle={
        <div className="flex items-center gap-2">
          <StatusBadge
            status={statusForBadge(row.status)}
            label={row.status || "success"}
            size="sm"
          />
          <span>{rowTime(row) ? formatRelativeTime(rowTime(row)) : "—"}</span>
        </div>
      }
      fields={fields}
      detail={detail}
    />
  );
}

export const Route = createFileRoute("/dashboard/audit/")({
  validateSearch: (search: Record<string, unknown>) =>
    search as { q?: string } & Record<string, unknown>,
  component: AuditLogPage,
});
