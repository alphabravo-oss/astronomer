import { Link as RouterLink } from "@tanstack/react-router";
import type { useQuery } from "@tanstack/react-query";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/operator-table";
import { ActionButton } from "@/components/ui/action-button";
import { QueryStates } from "@/components/ui/query-states";
import { permissionDeniedReason } from "@/lib/permission-hooks";
import type { PermissionDecision } from "@/lib/permissions";
import type { PaginatedResponse } from "@/types";
import {
  AlertTriangle,
  ArrowUpCircle,
  Box,
  ExternalLink,
  Trash2,
  Wrench,
} from "lucide-react";
import type { ClusterAppRow } from "@/lib/api/cluster-apps";

// Coarse status → tone mapping. We don't try to enumerate every
// helm-release state; just bucket into the four colors operators
// recognise: green=happy, blue=in flight, amber=needs attention,
// red=broken. Anything we don't know maps to muted.
function statusTone(status: string): string {
  const s = status.toLowerCase();
  if (s === "installed" || s === "adopted" || s === "ready") {
    return "bg-status-success/10 text-status-success border-status-success/30";
  }
  if (
    s.startsWith("installing") ||
    s.startsWith("upgrading") ||
    s === "pending_install" ||
    s === "pending_upgrade"
  ) {
    return "bg-status-info/10 text-status-info border-status-info/30";
  }
  if (s.startsWith("uninstalling") || s === "pending_uninstall") {
    return "bg-status-warning/10 text-status-warning border-status-warning/30";
  }
  if (s.includes("fail") || s === "errored" || s === "broken") {
    return "bg-status-error/10 text-status-error border-status-error/30";
  }
  return "bg-muted text-muted-foreground border-border";
}

// Cheap "stale install" detector. A release in a transient state
// (installing / pending_*) should converge to installed within
// minutes — the platform-baseline tools resolve in seconds, even
// kube-prom-stack settles in <10 min. Anything still pending past
// the threshold is either failed silently or stuck on the agent
// side; surface that as an amber warning so the operator notices.
const TRANSIENT_STATES = new Set([
  "installing",
  "upgrading",
  "uninstalling",
  "pending_install",
  "pending_upgrade",
  "pending_uninstall",
]);
const STALE_THRESHOLD_MS = 10 * 60 * 1000;

function isStale(row: ClusterAppRow): { stale: boolean; ageMin: number } {
  const s = row.status.toLowerCase();
  if (!TRANSIENT_STATES.has(s)) return { stale: false, ageMin: 0 };
  const updated = Date.parse(row.updatedAt);
  if (Number.isNaN(updated)) return { stale: false, ageMin: 0 };
  const ageMs = Date.now() - updated;
  return {
    stale: ageMs > STALE_THRESHOLD_MS,
    ageMin: Math.round(ageMs / 60_000),
  };
}

export function InstalledView({
  clusterId,
  q,
  onUpgrade,
  onUninstall,
  onDeleteFailed,
  updateDecision,
  deleteDecision,
}: {
  clusterId: string;
  q: ReturnType<typeof useQuery<PaginatedResponse<ClusterAppRow>>>;
  onUpgrade: (row: ClusterAppRow) => void;
  onUninstall: (row: ClusterAppRow) => void;
  onDeleteFailed: () => void;
  updateDecision: PermissionDecision;
  deleteDecision: PermissionDecision;
}) {
  return (
    <QueryStates
      query={q}
      loadingTitle="Loading installed apps…"
      isEmpty={(page) => page.data.length === 0}
      empty={
        <div className="rounded-lg border border-dashed border-border p-8 text-center space-y-3">
          <Box className="h-8 w-8 mx-auto text-muted-foreground" />
          <p className="text-sm font-medium text-foreground">
            No apps installed yet
          </p>
          <p className="text-xs text-muted-foreground max-w-md mx-auto">
            Browse the catalog and install your first chart. Quick Start deploys
            metrics exporters and, unless opted out, Trivy image scanning
            through Flux. Inspect those baseline deployments in Delivery. Full
            monitoring and logging are separate add-ons.
          </p>
          <div className="flex items-center justify-center gap-2 pt-2">
            <RouterLink
              to="/dashboard/clusters/$id/apps"
              params={{ id: clusterId }}
              search={{ section: "browse" }}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-primary text-primary-foreground text-xs font-medium hover:opacity-90"
            >
              Browse catalog
            </RouterLink>
            <RouterLink
              to="/dashboard/clusters/$id/tools"
              params={{ id: clusterId }}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border text-xs font-medium hover:bg-muted"
            >
              Open Tools
            </RouterLink>
          </div>
        </div>
      }
    >
      {(page) => {
        const items = page.data;
        const staleCount = items.filter((r) => isStale(r).stale).length;
        const failedCount = items.filter((r) => {
          const s = r.status.toLowerCase();
          return s === "failed_install" || s === "failed_uninstall";
        }).length;
        return (
          <div className="space-y-3">
            {failedCount > 0 && (
              <div className="rounded-md border border-status-error/40 bg-status-error/5 px-3 py-2 text-xs flex items-start gap-2">
                <AlertTriangle className="h-4 w-4 text-status-error shrink-0 mt-0.5" />
                <div className="flex-1">
                  <div className="font-medium text-foreground">
                    {failedCount} failed install{failedCount === 1 ? "" : "s"}{" "}
                    on this cluster
                  </div>
                  <p className="text-muted-foreground mt-0.5">
                    Releases in{" "}
                    <code className="font-mono">failed_install</code> /{" "}
                    <code className="font-mono">failed_uninstall</code> never
                    deployed cleanly. The helm release itself is either missing
                    or already gone, so they can&apos;t be uninstalled through
                    the normal flow — use the bulk delete to clear them.
                  </p>
                </div>
                <ActionButton
                  type="button"
                  onClick={onDeleteFailed}
                  size="sm"
                  icon={<Trash2 className="h-3 w-3" />}
                  disabled={!deleteDecision.allowed}
                  disabledReason={
                    !deleteDecision.allowed
                      ? permissionDeniedReason(deleteDecision)
                      : undefined
                  }
                  className="border-status-error/40 text-status-error hover:bg-status-error/10"
                >
                  Delete {failedCount} failed
                </ActionButton>
              </div>
            )}
            {staleCount > 0 && (
              <div className="rounded-md border border-status-warning/40 bg-status-warning/5 px-3 py-2 text-xs flex items-start gap-2">
                <AlertTriangle className="h-4 w-4 text-status-warning shrink-0 mt-0.5" />
                <div>
                  <div className="font-medium text-foreground">
                    {staleCount} release{staleCount === 1 ? "" : "s"} stuck in a
                    transient state for over 10 minutes
                  </div>
                  <p className="text-muted-foreground mt-0.5">
                    The helm operation may have stalled. Common causes: the
                    agent tunnel dropped, the helm chart failed validation, or a
                    long-running install (kube-prom-stack, istio) is still
                    pulling images. Check the worker queue or re-trigger the
                    operation.
                  </p>
                </div>
              </div>
            )}
            <div className="border border-border rounded-lg overflow-hidden">
              <Table className="w-full text-sm">
                <TableHeader className="bg-muted/50 text-left text-xs uppercase tracking-wide">
                  <TableRow>
                    <TableHead className="px-3 py-2">Release</TableHead>
                    <TableHead className="px-3 py-2">Chart</TableHead>
                    <TableHead className="px-3 py-2">Namespace</TableHead>
                    <TableHead className="px-3 py-2">Version</TableHead>
                    <TableHead className="px-3 py-2">Status</TableHead>
                    <TableHead className="px-3 py-2 text-right">
                      Actions
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((row) => (
                    <InstalledRow
                      key={row.id}
                      row={row}
                      clusterId={clusterId}
                      onUpgrade={onUpgrade}
                      onUninstall={onUninstall}
                      updateDecision={updateDecision}
                      deleteDecision={deleteDecision}
                    />
                  ))}
                </TableBody>
              </Table>
            </div>
          </div>
        );
      }}
    </QueryStates>
  );
}

function InstalledRow({
  row,
  clusterId,
  onUpgrade,
  onUninstall,
  updateDecision,
  deleteDecision,
}: {
  row: ClusterAppRow;
  clusterId: string;
  onUpgrade: (row: ClusterAppRow) => void;
  onUninstall: (row: ClusterAppRow) => void;
  updateDecision: PermissionDecision;
  deleteDecision: PermissionDecision;
}) {
  const isTool = row.sourceKind === "tool";
  // Upgrade requires the parent chartId; Tools installs (chart_version_id
  // null) have no chartId so we can't drive the version dropdown.
  const canUpgrade = !isTool && !!row.chartId;
  const { stale, ageMin } = isStale(row);
  return (
    <TableRow className="border-t border-border hover:bg-muted/40">
      <TableCell className="px-3 py-2 font-mono text-xs">
        {row.releaseName}
      </TableCell>
      <TableCell className="px-3 py-2">
        <div className="flex items-center gap-2">
          {row.chartIconUrl ? (
            <img src={row.chartIconUrl} alt="" className="h-5 w-5 rounded-sm" />
          ) : (
            <Box className="h-5 w-5 text-muted-foreground" />
          )}
          {/* Backend's displayName falls back to releaseName when chart
              metadata is missing, which duplicates the Release column for
              failed installs that never resolved a chart_version. Drop that
              shape to "—" with a hover hint so the operator sees "no chart
              info" rather than "same string twice". */}
          {row.displayName && row.displayName !== row.releaseName ? (
            <span className="text-foreground">{row.displayName}</span>
          ) : (
            <span
              className="text-muted-foreground italic"
              title="No chart metadata recorded for this release — the install likely failed before the chart version was resolved."
            >
              —
            </span>
          )}
          {isTool && (
            <RouterLink
              to="/dashboard/clusters/$id/tools"
              params={{ id: clusterId }}
              className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded-sm border border-border bg-muted text-muted-foreground hover:bg-accent"
              title="This release is managed by the Tools tab. Open Tools to upgrade or uninstall."
            >
              <Wrench className="h-3 w-3" /> Tools
            </RouterLink>
          )}
        </div>
        {row.repoName && (
          <div className="text-[11px] text-muted-foreground mt-0.5">
            {row.repoName}
            {row.chartCategory ? ` · ${row.chartCategory}` : ""}
          </div>
        )}
      </TableCell>
      <TableCell className="px-3 py-2 text-xs text-muted-foreground font-mono">
        {row.namespace}
      </TableCell>
      <TableCell className="px-3 py-2 text-xs tabular-nums">
        {row.chartVersion || <span className="text-muted-foreground">—</span>}
      </TableCell>
      <TableCell className="px-3 py-2">
        <div className="inline-flex items-center gap-1.5">
          <span
            className={`inline-flex items-center px-2 py-0.5 rounded-sm border text-[11px] font-medium ${statusTone(row.status)}`}
          >
            {row.status}
          </span>
          {stale && (
            <span
              className="inline-flex items-center gap-1 text-[10px] text-status-warning"
              title={`Stuck in '${row.status}' for ${ageMin} min. The helm operation may have stalled — check the worker queue or the cluster's agent connectivity.`}
            >
              <AlertTriangle className="h-3 w-3" /> stale {ageMin}m
            </span>
          )}
        </div>
      </TableCell>
      <TableCell className="px-3 py-2 text-right">
        {isTool ? (
          <RouterLink
            to="/dashboard/clusters/$id/tools"
            params={{ id: clusterId }}
            className="text-xs text-muted-foreground hover:text-foreground inline-flex items-center gap-1"
          >
            Manage <ExternalLink className="h-3 w-3" />
          </RouterLink>
        ) : (
          <div className="inline-flex items-center gap-1">
            <ActionButton
              onClick={() => onUpgrade(row)}
              disabled={!canUpgrade || !updateDecision.allowed}
              disabledReason={
                !canUpgrade
                  ? "Upgrade unavailable for this release"
                  : !updateDecision.allowed
                    ? permissionDeniedReason(updateDecision)
                    : undefined
              }
              title="Upgrade to a newer chart version or edit values"
              size="sm"
              icon={<ArrowUpCircle className="h-3 w-3" />}
              className="h-7 px-2"
            >
              Upgrade
            </ActionButton>
            <ActionButton
              onClick={() => onUninstall(row)}
              disabled={!deleteDecision.allowed}
              disabledReason={
                !deleteDecision.allowed
                  ? permissionDeniedReason(deleteDecision)
                  : undefined
              }
              title="Uninstall this release"
              size="sm"
              icon={<Trash2 className="h-3 w-3" />}
              className="h-7 px-2 border-status-error/40 text-status-error hover:bg-status-error/10"
            >
              Uninstall
            </ActionButton>
          </div>
        )}
      </TableCell>
    </TableRow>
  );
}
