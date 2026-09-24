import { useMemo, useState } from "react";
import { useClock } from "@/lib/hooks/use-clock";
import { Link as RouterLink } from "@tanstack/react-router";
import { useNavigate } from "@tanstack/react-router";
import { useCISScans } from "@/components/security/hooks";
import { useEntityNames } from "@/lib/hooks/entity-names";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { PermissionState } from "@/components/ui/empty-state";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { DataTable, type Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { ActionButton } from "@/components/ui/action-button";
import { formatRelativeTime, cn } from "@/lib/utils";
import { pageCountLabel, pageTableCount } from "@/lib/api/pagination";
import type { CISScanListItem } from "@/types";
import {
  Plus,
  AlertTriangle,
  CheckCircle2,
  XCircle,
  MinusCircle,
} from "lucide-react";

/**
 * CIS Scans tab body. Lives outside `page.tsx` so the parent stays a thin
 * tab shell and so the failure-summary panel + table can be tested in
 * isolation. Live updates piggy-back on `cluster.k8s_changed`: when any
 * cluster's K8s state moves we refetch the list — the cis-operator emits a
 * ClusterScan/Report mutation which surfaces through that channel.
 */
function recentFailedScans(scans: CISScanListItem[], now: number) {
  const dayMs = 24 * 60 * 60 * 1000;
  return scans
    .filter((scan) => {
      if (!scan.completedAt) return false;
      const completed = new Date(scan.completedAt).getTime();
      return (
        Number.isFinite(completed) &&
        now - completed < dayMs &&
        (scan.failed ?? 0) > 0
      );
    })
    .sort((a, b) => (b.failed ?? 0) - (a.failed ?? 0))
    .slice(0, 3);
}

export function CISScansTab() {
  const now = useClock();
  const navigate = useNavigate();
  const [pageIndex, setPageIndex] = useState(0);
  const read = usePermissionDecision("security", "read");
  const create = usePermissionDecision("security", "create");
  const scansQuery = useCISScans(
    { page: pageIndex + 1, pageSize: 25 },
    { enabled: read.allowed },
  );
  const scansPage =
    scansQuery.isError || !read.allowed ? undefined : scansQuery.data;
  const { clusters } = useEntityNames({
    clusterIds: scansPage?.data.map((scan) => scan.clusterId),
  });

  // Cross-cluster signal: any K8s mutation invalidates the scan list so a
  // newly-completed ingest pops up without a manual refresh.
  useLiveQueryInvalidation("cluster.k8s_changed", [["cis", "scans"]]);
  const clusterById = useMemo(
    () => new Map(clusters.map((c) => [c.id, c.displayName || c.name])),
    [clusters],
  );

  const scans = useMemo(() => scansPage?.data ?? [], [scansPage]);

  // Recent failures = scans with at least one failed check, completed in
  // the last 24h. Surfaces the most actionable item at the top.
  const recentFailures = useMemo(
    () => recentFailedScans(scans, now),
    [scans, now],
  );

  const columns: Column<CISScanListItem>[] = [
    {
      key: "cluster",
      header: "Cluster",
      accessor: (row) => (
        <span className="font-medium text-foreground text-sm">
          {clusterById.get(row.clusterId) ?? row.clusterId.slice(0, 8)}
        </span>
      ),
      sortAccessor: (row) => clusterById.get(row.clusterId) ?? row.clusterId,
    },
    {
      key: "profile",
      header: "Profile",
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.scanType}
        </span>
      ),
      sortAccessor: (row) => row.scanType,
    },
    {
      key: "runAt",
      header: "Run At",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.completedAt
            ? formatRelativeTime(row.completedAt)
            : row.startedAt
              ? `Started ${formatRelativeTime(row.startedAt)}`
              : formatRelativeTime(row.createdAt)}
        </span>
      ),
      sortAccessor: (row) => row.completedAt ?? row.startedAt ?? row.createdAt,
    },
    {
      key: "status",
      header: "Status",
      accessor: (row) => <StatusBadge status={row.status} />,
      sortAccessor: (row) => row.status,
    },
    {
      key: "pass",
      header: "Pass",
      align: "right",
      accessor: (row) => (
        <span className="tabular-nums text-status-success text-sm">
          {row.passed ?? 0}
        </span>
      ),
      sortAccessor: (row) => row.passed ?? 0,
    },
    {
      key: "fail",
      header: "Fail",
      align: "right",
      accessor: (row) => (
        <span
          className={cn(
            "tabular-nums text-sm",
            (row.failed ?? 0) > 0
              ? "text-status-error font-medium"
              : "text-muted-foreground",
          )}
        >
          {row.failed ?? 0}
        </span>
      ),
      sortAccessor: (row) => row.failed ?? 0,
    },
    {
      key: "warn",
      header: "Warn",
      align: "right",
      accessor: (row) => (
        <span
          className={cn(
            "tabular-nums text-sm",
            (row.warned ?? 0) > 0
              ? "text-status-warning"
              : "text-muted-foreground",
          )}
        >
          {row.warned ?? 0}
        </span>
      ),
      sortAccessor: (row) => row.warned ?? 0,
    },
    {
      key: "skip",
      header: "Skip",
      align: "right",
      accessor: (row) => (
        <span className="tabular-nums text-sm text-muted-foreground">
          {row.skipped ?? 0}
        </span>
      ),
      sortAccessor: (row) => row.skipped ?? 0,
    },
    {
      key: "actions",
      header: "",
      sortable: false,
      accessor: (row) => (
        <RouterLink
          to="/dashboard/security/scans/$scanId"
          params={{ scanId: row.id }}
          onClick={(e) => e.stopPropagation()}
          className="text-xs text-primary hover:underline"
        >
          View
        </RouterLink>
      ),
    },
  ];

  if (!read.allowed) return <PermissionState permission="security:read" />;

  return (
    <div className="space-y-4">
      {/* Recent failure summary — only renders when something needs attention. */}
      {recentFailures.length > 0 && (
        <div className="rounded-lg border border-status-error/30 bg-status-error/5 p-4">
          <div className="flex items-start gap-3">
            <AlertTriangle className="h-5 w-5 text-status-error shrink-0 mt-0.5" />
            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium text-foreground">
                {recentFailures.reduce((sum, s) => sum + (s.failed ?? 0), 0)}{" "}
                failed checks on this page across {recentFailures.length} recent
                scan
                {recentFailures.length === 1 ? "" : "s"}
              </p>
              <ul className="mt-2 space-y-1.5">
                {recentFailures.map((s) => (
                  <li key={s.id} className="flex items-center gap-2 text-xs">
                    <RouterLink
                      to="/dashboard/security/scans/$scanId"
                      params={{ scanId: s.id }}
                      className="text-foreground hover:text-primary hover:underline font-medium"
                    >
                      {clusterById.get(s.clusterId) ?? s.clusterId.slice(0, 8)}
                    </RouterLink>
                    <span className="text-muted-foreground font-mono">
                      {s.scanType}
                    </span>
                    <span className="text-status-error font-medium tabular-nums">
                      {s.failed} failed
                    </span>
                    <span className="text-muted-foreground">·</span>
                    <span className="text-muted-foreground">
                      {s.completedAt && formatRelativeTime(s.completedAt)}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </div>
      )}

      {/* Aggregate count strip — useful at-a-glance summary. */}
      {scans.length > 0 && (
        <p className="text-xs text-muted-foreground">
          Check totals on this page
        </p>
      )}
      <ScanAggregateStrip scans={scans} />

      {/* Header bar with the New Scan CTA. */}
      <div className="flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          {pageCountLabel(scansPage)} historical scans
        </p>
        <ActionButton
          intent="primary"
          disabled={!create.allowed}
          disabledReason="Starting a scan requires security:create."
          icon={<Plus className="h-4 w-4" />}
          onClick={() => void navigate({ to: "/dashboard/security/scans/new" })}
        >
          New Scan
        </ActionButton>
      </div>

      <DataTable
        data={scans}
        columns={columns.map((column) => ({ ...column, sortable: false }))}
        keyExtractor={(row) => row.id}
        loading={scansQuery.isLoading}
        isError={scansQuery.isError}
        error={scansQuery.error}
        errorMessage="Failed to load CIS scans."
        onRetry={() => void scansQuery.refetch()}
        searchable={false}
        pageSize={25}
        serverSide={{
          ...pageTableCount(scansPage),
          pagination: { pageIndex, pageSize: 25 },
          onPaginationChange: (next) => setPageIndex(next.pageIndex),
        }}
        emptyState={{
          title: "No CIS scans yet",
          description:
            "Run a benchmark to identify security findings across your clusters.",
          action: create.allowed
            ? { label: "New scan", href: "/dashboard/security/scans/new" }
            : undefined,
        }}
        onRowClick={(row) =>
          void navigate({ to: `/dashboard/security/scans/${row.id}` })
        }
      />
    </div>
  );
}

/** Strip of pass/fail/warn/skip totals for the visible page only. */
function ScanAggregateStrip({ scans }: { scans: CISScanListItem[] }) {
  const totals = useMemo(() => {
    return scans.reduce(
      (acc, s) => {
        acc.passed += s.passed ?? 0;
        acc.failed += s.failed ?? 0;
        acc.warned += s.warned ?? 0;
        acc.skipped += s.skipped ?? 0;
        return acc;
      },
      { passed: 0, failed: 0, warned: 0, skipped: 0 },
    );
  }, [scans]);

  if (scans.length === 0) return null;

  const cells: {
    label: string;
    value: number;
    icon: React.ElementType;
    color: string;
  }[] = [
    {
      label: "Passed",
      value: totals.passed,
      icon: CheckCircle2,
      color: "text-status-success",
    },
    {
      label: "Failed",
      value: totals.failed,
      icon: XCircle,
      color: "text-status-error",
    },
    {
      label: "Warned",
      value: totals.warned,
      icon: AlertTriangle,
      color: "text-status-warning",
    },
    {
      label: "Skipped",
      value: totals.skipped,
      icon: MinusCircle,
      color: "text-muted-foreground",
    },
  ];

  return (
    <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
      {cells.map((c) => {
        const Icon = c.icon;
        return (
          <div
            key={c.label}
            className="rounded-lg border border-border bg-card p-3"
          >
            <div className="flex items-center gap-2">
              <Icon className={cn("h-4 w-4", c.color)} />
              <span className="text-xs text-muted-foreground">{c.label}</span>
            </div>
            <p
              className={cn(
                "mt-1 text-2xl font-semibold tabular-nums",
                c.color,
              )}
            >
              {c.value.toLocaleString()}
            </p>
          </div>
        );
      })}
    </div>
  );
}

/**
 * Centralised empty-state hint shown on the overview tab strip when the
 * backend reports `source: 'fallback'` for the first cluster's profiles —
 * cis-operator isn't installed yet. Kept here so the wizard and overview
 * stay in sync.
 */
export const CIS_NOT_INSTALLED_HINT =
  "cis-operator is not installed on this cluster. The scan will use the static profile fallback " +
  "and may not produce findings until the operator is deployed.";
