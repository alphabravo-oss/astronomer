import { DrawerShell } from "@/components/ui/drawer-shell";
import { ReportCVEs } from "@/components/security/report-cves";
import { Select } from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/operator-table";
import { ShieldAlert, TrendingDown, TrendingUp, Minus } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import { Tooltip } from "@/components/ui/tooltip";
import { SkeletonText } from "@/components/ui/skeleton";
import { BARE_BUTTON } from "@/lib/bare-button";
import type {
  CVESeverity,
  ImageVulnDiff,
  ImageVulnHistoryResponse,
  ImageVulnReport,
  ImageVulnReportHistoryResponse,
  ImageVulnSummary,
} from "@/lib/api/cluster-vulnerabilities";
import { HistorySparkline } from "./-history-sparkline";

export const SEVERITIES: {
  key: keyof ImageVulnSummary;
  label: string;
  tone: string;
}[] = [
  {
    key: "critical",
    label: "Critical",
    tone: "bg-status-error/10 text-status-error border-status-error/30",
  },
  {
    key: "high",
    label: "High",
    tone: "bg-status-high/10 text-status-high border-status-high/30",
  },
  {
    key: "medium",
    label: "Medium",
    tone: "bg-status-warning/10 text-status-warning border-status-warning/30",
  },
  {
    key: "low",
    label: "Low",
    tone: "bg-status-info/10 text-status-info border-status-info/30",
  },
];

export function SeverityTiles({
  isLoading,
  summary,
}: {
  isLoading: boolean;
  summary?: ImageVulnSummary;
}) {
  return (
    <section className="grid grid-cols-2 md:grid-cols-4 gap-3">
      {SEVERITIES.map((s) => (
        <div key={s.key} className={`border rounded-lg p-4 ${s.tone}`}>
          <div className="text-xs uppercase tracking-wide">{s.label}</div>
          <div className="text-3xl font-bold mt-1">
            {isLoading ? "—" : (summary?.[s.key] ?? 0)}
          </div>
        </div>
      ))}
    </section>
  );
}

export function DiffCard({ diff }: { diff?: ImageVulnDiff }) {
  return (
    <div className="border border-border rounded-lg p-4 space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-foreground">
          What changed in the last 24h
        </h3>
        {diff?.hasComparison && diff.prior && (
          <span className="text-xs text-muted-foreground">
            vs {new Date(diff.prior.scannedAt).toLocaleString()}
          </span>
        )}
      </div>
      {!diff || !diff.hasComparison ? (
        <p className="text-xs text-muted-foreground">
          Not enough scan history yet — we&apos;ll surface a diff once a second
          snapshot lands (typically within an hour of trivy-operator&apos;s
          schedule).
        </p>
      ) : (
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
          {(["critical", "high", "medium", "low"] as const).map((sev) => {
            const d = diff.delta?.[sev] ?? 0;
            const tone =
              d > 0
                ? "text-status-error border-status-error/30 bg-status-error/5"
                : d < 0
                  ? "text-status-success border-status-success/30 bg-status-success/5"
                  : "text-muted-foreground border-border";
            const Icon = d > 0 ? TrendingUp : d < 0 ? TrendingDown : Minus;
            return (
              <div key={sev} className={`border rounded-sm p-2 ${tone}`}>
                <div className="text-[10px] uppercase tracking-wide opacity-80">
                  {sev}
                </div>
                <div className="flex items-baseline justify-between mt-1">
                  <div className="text-xl font-semibold tabular-nums">
                    {d > 0 ? "+" : ""}
                    {d}
                  </div>
                  <Icon className="h-3.5 w-3.5" />
                </div>
                <div className="text-[10px] opacity-70 mt-0.5">
                  {diff.prior?.[sev] ?? 0} → {diff.latest?.[sev] ?? 0}
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

export function HistoryCard({
  history,
}: {
  history?: ImageVulnHistoryResponse;
}) {
  return (
    <div className="border border-border rounded-lg p-4 space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-foreground">
          Scan history (30 days)
        </h3>
        <span className="text-xs text-muted-foreground">
          {history?.totalCount ?? 0} scans
        </span>
      </div>
      {!history || history.snapshots.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          No history yet — once trivy-operator publishes a second
          VulnerabilityReport this chart will show the trend.
        </p>
      ) : (
        <>
          {/* Critical+High sparkline. Inline SVG so we don't drag
                  in a chart library for a 10-point trend line. The
                  y-axis is normalised to the max critical count in
                  the window so a small swing reads as a visible
                  movement instead of a flat line. */}
          <HistorySparkline points={history.snapshots.slice().reverse()} />
          {/* Recent scans table */}
          <div className="text-xs space-y-1 max-h-32 overflow-y-auto pr-1">
            {history.snapshots.slice(0, 6).map((p) => (
              <div
                key={p.scannedAt}
                className="flex items-center justify-between gap-2 py-0.5"
              >
                <span className="text-muted-foreground tabular-nums">
                  {new Date(p.scannedAt).toLocaleString()}
                </span>
                <span className="flex items-center gap-1.5 text-foreground">
                  <span className="text-status-error font-medium tabular-nums">
                    {p.critical}
                  </span>
                  <span className="text-status-high tabular-nums">
                    {p.high}
                  </span>
                  <span className="text-status-warning tabular-nums">
                    {p.medium}
                  </span>
                  <span className="text-status-info tabular-nums">{p.low}</span>
                </span>
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

export function ImagesTable({
  clusterId,
  isLoading,
  rows,
  onOpen,
}: {
  clusterId: string;
  isLoading: boolean;
  rows?: ImageVulnReport[];
  onOpen: (report: ImageVulnReport) => void;
}) {
  return (
    <section className="border border-border rounded-lg overflow-hidden">
      <Table className="w-full text-sm">
        <TableHeader className="bg-muted/50 text-left text-xs uppercase tracking-wide">
          <TableRow>
            <TableHead className="px-3 py-2">Image</TableHead>
            <TableHead className="px-3 py-2">Namespace</TableHead>
            <TableHead className="px-3 py-2">Workload</TableHead>
            <TableHead className="px-3 py-2 text-right">Critical</TableHead>
            <TableHead className="px-3 py-2 text-right">High</TableHead>
            <TableHead className="px-3 py-2 text-right">Total</TableHead>
            <TableHead className="px-3 py-2">Scanned</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading && (
            <TableRow>
              <TableCell colSpan={7} className="px-3 py-6" aria-busy="true">
                <span className="sr-only">Loading…</span>
                <SkeletonText lines={3} />
              </TableCell>
            </TableRow>
          )}
          {!isLoading && (rows?.length ?? 0) === 0 && (
            <TableRow>
              <TableCell colSpan={7} className="px-3 py-8 text-center">
                <div className="inline-flex flex-col items-center gap-2 text-muted-foreground">
                  <ShieldAlert className="h-6 w-6" />
                  <div className="font-medium text-foreground">
                    No vulnerability reports yet
                  </div>
                  <p className="text-xs max-w-md">
                    Install trivy-operator on this cluster and reports will
                    populate within the first scan window (typically 5–15 min).
                    Already installed? Use the rescan button above.
                  </p>
                  <div className="flex gap-2 mt-2">
                    <a
                      href={`/dashboard/clusters/${clusterId}/tools`}
                      className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-primary text-primary-foreground text-xs font-medium hover:opacity-90"
                    >
                      Install via Tools
                    </a>
                    <a
                      href={`/dashboard/clusters/${clusterId}/adoption`}
                      className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border text-xs font-medium hover:bg-muted"
                    >
                      View adoption status
                    </a>
                  </div>
                </div>
              </TableCell>
            </TableRow>
          )}
          {rows?.map((r) => {
            const total =
              r.criticalCount +
              r.highCount +
              r.mediumCount +
              r.lowCount +
              r.unknownCount;
            return (
              <TableRow
                key={r.id}
                className="border-t border-border hover:bg-muted/40 cursor-pointer"
                onClick={() => onOpen(r)}
              >
                <TableCell className="px-3 py-2 font-mono text-xs">
                  <ActionButton
                    {...BARE_BUTTON}
                    className="text-primary hover:underline focus-visible:outline focus-visible:outline-ring inline-block font-normal"
                    aria-label={`View CVEs for ${r.imageRepo}:${r.imageTag}`}
                    onClick={(event) => {
                      event.stopPropagation();
                      onOpen(r);
                    }}
                  >
                    {r.imageRepo}:{r.imageTag}
                  </ActionButton>
                </TableCell>
                <TableCell className="px-3 py-2">{r.namespace}</TableCell>
                <TableCell className="px-3 py-2">
                  {r.workloadKind} / {r.workloadName}
                </TableCell>
                <TableCell className="px-3 py-2 text-right font-semibold text-status-error">
                  {r.criticalCount}
                </TableCell>
                <TableCell className="px-3 py-2 text-right font-semibold text-status-high">
                  {r.highCount}
                </TableCell>
                <TableCell className="px-3 py-2 text-right">{total}</TableCell>
                <TableCell className="px-3 py-2 text-xs text-muted-foreground">
                  {new Date(r.scannedAt).toLocaleString()}
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </section>
  );
}

export function ImageDrawer({
  clusterId,
  report: openReport,
  severityFilter,
  onSeverityChange,
  onClose,
  reportHistory,
}: {
  clusterId: string;
  report: ImageVulnReport;
  severityFilter: CVESeverity | "";
  onSeverityChange: (severity: CVESeverity | "") => void;
  onClose: () => void;
  reportHistory: {
    isLoading: boolean;
    data?: ImageVulnReportHistoryResponse | null;
  };
}) {
  return (
    <DrawerShell
      title={`${openReport.imageRepo}:${openReport.imageTag}`}
      onClose={() => onClose()}
      subtitle={
        <>
          <p className="font-mono">{openReport.imageDigest || "(no digest)"}</p>
          <p>
            {openReport.namespace} · {openReport.workloadKind}/
            {openReport.workloadName}
          </p>
        </>
      }
      bodyClassName="space-y-3"
    >
      <div className="flex items-center gap-3">
        <label
          className="text-sm text-muted-foreground"
          htmlFor="field-1a64459b-407"
        >
          CVE severity
        </label>
        <Select
          id="field-1a64459b-407"
          className="border border-border bg-background rounded-md px-2 py-1 text-sm"
          value={severityFilter}
          onChange={(e) => onSeverityChange(e.target.value as CVESeverity | "")}
        >
          <option value="">All</option>
          <option value="CRITICAL">Critical only</option>
          <option value="HIGH">High only</option>
          <option value="MEDIUM">Medium only</option>
          <option value="LOW">Low only</option>
        </Select>
      </div>
      {/* Per-image scan history. Two snapshots minimum to render
                the sparkline; until then the panel shows a one-liner
                so operators know more history will accumulate. */}
      <section className="border border-border rounded-md p-3 space-y-2 bg-muted/20">
        <div className="flex items-center justify-between">
          <h3 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
            Scan history
          </h3>
          <span className="text-[10px] text-muted-foreground tabular-nums">
            {reportHistory.data?.totalCount ?? 0} snapshot
            {(reportHistory.data?.totalCount ?? 0) === 1 ? "" : "s"}
          </span>
        </div>
        {reportHistory.isLoading && (
          <div aria-busy="true">
            <span className="sr-only">Loading history…</span>
            <SkeletonText lines={2} />
          </div>
        )}
        {reportHistory.data && reportHistory.data.snapshots.length === 0 && (
          <p className="text-xs text-muted-foreground">
            No snapshots yet — once trivy-operator re-scans this workload its
            history will appear here.
          </p>
        )}
        {reportHistory.data && reportHistory.data.snapshots.length > 0 && (
          <>
            <HistorySparkline
              points={reportHistory.data.snapshots.slice().reverse()}
            />
            <div className="text-xs space-y-1 max-h-40 overflow-y-auto pr-1">
              {reportHistory.data.snapshots.slice(0, 10).map((p) => (
                <div
                  key={p.scannedAt}
                  className="flex items-center justify-between gap-2 py-0.5"
                >
                  <span className="text-muted-foreground tabular-nums">
                    {new Date(p.scannedAt).toLocaleString()}
                  </span>
                  <span className="flex items-center gap-1.5">
                    <Tooltip content="Critical">
                      <span className="text-status-error font-medium tabular-nums">
                        {p.critical}
                      </span>
                    </Tooltip>
                    <Tooltip content="High">
                      <span className="text-status-high tabular-nums">
                        {p.high}
                      </span>
                    </Tooltip>
                    <Tooltip content="Medium">
                      <span className="text-status-warning tabular-nums">
                        {p.medium}
                      </span>
                    </Tooltip>
                    <Tooltip content="Low">
                      <span className="text-status-info tabular-nums">
                        {p.low}
                      </span>
                    </Tooltip>
                  </span>
                </div>
              ))}
            </div>
          </>
        )}
      </section>

      <ReportCVEs
        clusterId={clusterId}
        reportId={openReport.id}
        severity={severityFilter}
      />
    </DrawerShell>
  );
}

export function LocalClusterScanUnavailable() {
  return (
    <div className="flex flex-col items-center justify-center h-64 text-muted-foreground gap-2 max-w-md mx-auto text-center p-4">
      <ShieldAlert className="h-8 w-8 mb-2" />
      <p className="text-sm font-medium text-foreground">
        Image scans aren&apos;t available on the management plane&apos;s own
        cluster.
      </p>
      <p className="text-xs">
        Image scanning depends on trivy-operator running in a remote cluster and
        reachable over the agent tunnel. Register a managed cluster, install
        trivy-operator from the Catalog, and scans will appear here.
      </p>
    </div>
  );
}
