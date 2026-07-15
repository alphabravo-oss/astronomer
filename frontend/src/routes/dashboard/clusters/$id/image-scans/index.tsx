import { createFileRoute } from '@tanstack/react-router';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * Cluster Image Scans tab — sprint 062.
 *
 * Three panels:
 *   1. Header tiles per severity (Critical / High / Medium / Low) +
 *      last_scanned_at.
 *   2. Top-images table — sortable by Critical/High by default. Filter
 *      by namespace. Each row opens a per-image drawer with the
 *      severity-coded CVE list.
 *   3. Trigger rescan button (POST). Nil-safe when the operator is
 *      missing — the API returns triggered=false with a reason string
 *      and the UI surfaces it as a non-blocking warning.
 *
 * RBAC: cluster:read for everything, no write-side mutation beyond the
 * rescan nudge (which the backend gates as cluster:read by design).
 */

import { useEffect, useMemo, useState } from 'react';
import { useParams } from '@/lib/navigation';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess, toastWarning } from '@/lib/toast';
import {
  AlertTriangle,
  Loader2,
  RefreshCw,
  ShieldAlert,
  X,
} from 'lucide-react';

import { queryKeys, useCluster } from '@/lib/hooks';
import {
  getImageVulnReport,
  getImageVulnReportHistory,
  getImageVulnSummary,
  getImageVulnHistory,
  getImageVulnDiff,
  getImageVulnProgress,
  exportImageVulnsCSVPath,
  listVulnerableImages,
  triggerImageVulnRescan,
  type CVESeverity,
  type ImageVulnReport,
  type ImageVulnSummary,
} from '@/lib/api/cluster-detail';
import { Download, TrendingDown, TrendingUp, Minus } from 'lucide-react';

const SEVERITIES: { key: keyof ImageVulnSummary; label: string; tone: string }[] = [
  { key: 'critical', label: 'Critical', tone: 'bg-red-500/10 text-red-500 border-red-500/30' },
  { key: 'high', label: 'High', tone: 'bg-orange-500/10 text-orange-500 border-orange-500/30' },
  { key: 'medium', label: 'Medium', tone: 'bg-amber-500/10 text-amber-500 border-amber-500/30' },
  { key: 'low', label: 'Low', tone: 'bg-sky-500/10 text-sky-500 border-sky-500/30' },
];

function cveToneFor(severity: CVESeverity): string {
  switch (severity) {
    case 'CRITICAL':
      return 'bg-red-500/10 text-red-500 border-red-500/30';
    case 'HIGH':
      return 'bg-orange-500/10 text-orange-500 border-orange-500/30';
    case 'MEDIUM':
      return 'bg-amber-500/10 text-amber-500 border-amber-500/30';
    case 'LOW':
      return 'bg-sky-500/10 text-sky-500 border-sky-500/30';
    default:
      return 'bg-muted text-muted-foreground border-border';
  }
}

function ClusterImageScansPage() {
  const params = useParams();
  const clusterId = params.id as string;
  const queryClient = useQueryClient();
  const { data: cluster } = useCluster(clusterId);

  const [namespace, setNamespace] = useState<string>('');
  const [severityFilter, setSeverityFilter] = useState<CVESeverity | ''>('');
  const [openReport, setOpenReport] = useState<ImageVulnReport | null>(null);
  // Sprint 081: after a manual "Trigger rescan" click we accelerate
  // the progress poll for ~60s and show a "dispatched" banner state.
  // Trivy completes scans in <30s on small clusters, so a slow idle
  // poll would miss the scanning state entirely; this stretches the
  // user-visible feedback window so the click always produces a
  // legible response.
  const [lastRescanAt, setLastRescanAt] = useState<number | null>(null);
  const scansEnabled = !!cluster && !cluster.isLocal;

  const summary = useQuery({
    queryKey: queryKeys.clusterPages.imageVulnSummary(clusterId),
    queryFn: () => getImageVulnSummary(clusterId),
    enabled: scansEnabled,
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
  });

  const images = useQuery({
    queryKey: queryKeys.clusterPages.imageVulnImages(clusterId, namespace),
    queryFn: () => listVulnerableImages(clusterId, { namespace: namespace || undefined, limit: 20 }),
    enabled: scansEnabled,
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
  });

  const reportDetail = useQuery({
    queryKey: openReport
      ? queryKeys.clusterPages.imageVulnReport(clusterId, openReport.id, severityFilter || '')
      : ['noop'],
    queryFn: () =>
      openReport
        ? getImageVulnReport(clusterId, openReport.id, {
            severity: (severityFilter || undefined) as CVESeverity | undefined,
            limit: 100,
          })
        : Promise.resolve(null),
    enabled: scansEnabled && !!openReport,
  });

  // Per-image scan history for the drawer. Only fires when a row is
  // open; refreshes on the same 30s cadence as the cluster aggregates
  // so the drawer stays in sync if a new snapshot lands while open.
  const reportHistory = useQuery({
    queryKey: openReport
      ? queryKeys.clusterPages.imageVulnReportHistory(clusterId, openReport.id)
      : ['noop-rh'],
    queryFn: () =>
      openReport
        ? getImageVulnReportHistory(clusterId, openReport.id, { limit: 50 })
        : Promise.resolve(null),
    enabled: scansEnabled && !!openReport,
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
  });

  const rescan = useMutation({
    mutationFn: () => triggerImageVulnRescan(clusterId),
    onSuccess: (data) => {
      if (data.triggered) {
        toastSuccess('Trivy operator nudged — re-scans will appear shortly');
        // Mark the click time so the progress banner enters
        // "dispatched, waiting for jobs" mode and the progress query
        // polls at 1.5s for the next 60s — long enough to catch the
        // 5-15s scan window most clusters produce.
        setLastRescanAt(Date.now());
      } else {
        toastWarning(`Rescan not triggered: ${data.reason ?? 'unknown'}`);
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.imageVulnSummary(clusterId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.imageVulnHistory(clusterId, 24 * 30) });
      queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.imageVulnDiff(clusterId, 24) });
      queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.imageVulnProgress(clusterId) });
    },
    onError: (err) => toastApiError('Rescan failed', err),
  });

  // Sprint 081: scan history sparkline (last 30 days) + diff vs 24h
  // ago. Both refresh on the same 30s cadence as the summary so the
  // page stays in sync as new Trivy reports flow in.
  const history = useQuery({
    queryKey: queryKeys.clusterPages.imageVulnHistory(clusterId, 24 * 30),
    queryFn: () => getImageVulnHistory(clusterId, { sinceHours: 24 * 30, limit: 200 }),
    enabled: scansEnabled,
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
  });
  const diff = useQuery({
    queryKey: queryKeys.clusterPages.imageVulnDiff(clusterId, 24),
    queryFn: () => getImageVulnDiff(clusterId, 24),
    enabled: scansEnabled,
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
  });

  // Live scan-in-progress polling.
  //   • scanning detected            → 3s   (catch progress as it changes)
  //   • rescan clicked within 60s    → 1.5s (most scans finish < polling)
  //   • otherwise                    → 30s  (don't hammer the tunnel)
  const progress = useQuery({
    queryKey: queryKeys.clusterPages.imageVulnProgress(clusterId),
    queryFn: () => getImageVulnProgress(clusterId),
    enabled: scansEnabled,
    refetchInterval: (query) => {
      if (query.state.data?.scanning) return 3_000;
      if (lastRescanAt && Date.now() - lastRescanAt < 60_000) return 1_500;
      return 30_000;
    },
    refetchIntervalInBackground: false,
  });

  // Distinct namespace pick list, derived from the loaded reports.
  const namespaces = useMemo(() => {
    const set = new Set<string>();
    images.data?.items.forEach((r) => r.namespace && set.add(r.namespace));
    return Array.from(set).sort();
  }, [images.data]);

  // Auto-close drawer when its underlying row vanishes.
  useEffect(() => {
    if (!openReport) return;
    const stillThere = images.data?.items.some((r) => r.id === openReport.id);
    if (stillThere === false) setOpenReport(null);
  }, [images.data, openReport]);

  if (cluster?.isLocal) {
    return (
      <div className="flex flex-col items-center justify-center h-64 text-muted-foreground gap-2 max-w-md mx-auto text-center p-4">
        <ShieldAlert className="h-8 w-8 mb-2" />
        <p className="text-sm font-medium text-foreground">
          Image scans aren&apos;t available on the management plane&apos;s own cluster.
        </p>
        <p className="text-xs">
          Image scanning depends on trivy-operator running in a remote cluster
          and reachable over the agent tunnel. Register a managed cluster, install
          trivy-operator from the Catalog, and scans will appear here.
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-6 p-4">
      <header className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold flex items-center gap-2">
            <ShieldAlert className="h-6 w-6" /> Image Scans
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            Aggregated CVE counts from the in-cluster Trivy operator. Astronomer
            ingests VulnerabilityReport CRDs continuously.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <a
            className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded-md border border-border bg-background hover:bg-muted"
            href={exportImageVulnsCSVPath(clusterId)}
            download
            title="Download all current image-scan rows as CSV"
          >
            <Download className="h-4 w-4" />
            Export CSV
          </a>
          <button
            className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded-md border border-border bg-background hover:bg-muted disabled:opacity-50"
            onClick={() => rescan.mutate()}
            disabled={rescan.isPending}
          >
            {rescan.isPending ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <RefreshCw className="h-4 w-4" />
            )}
            Trigger rescan
          </button>
        </div>
      </header>

      {/* Scan-in-progress banner. Render states:
           • dispatched — operator clicked rescan in the last 60s; we
                          haven't observed scanning yet (trivy may not
                          have spawned jobs OR finished too fast).
           • scanning   — animated "Scanning N pods…" + progress bar.
           • idle+ready — quiet success bar with last-scan age.
           • !ready     — amber warning trivy-operator is unreachable.
          Sprint 081. */}
      <ScanProgressBanner
        clusterId={clusterId}
        progress={progress.data}
        dispatchedRecently={!!lastRescanAt && Date.now() - lastRescanAt < 30_000}
      />

      {/* Severity tiles */}
      <section className="grid grid-cols-2 md:grid-cols-4 gap-3">
        {SEVERITIES.map((s) => (
          <div
            key={s.key}
            className={`border rounded-lg p-4 ${s.tone}`}
          >
            <div className="text-xs uppercase tracking-wide">{s.label}</div>
            <div className="text-3xl font-bold mt-1">
              {summary.isLoading ? '—' : (summary.data?.[s.key] ?? 0)}
            </div>
          </div>
        ))}
      </section>

      <div className="text-xs text-muted-foreground">
        Last scan:{' '}
        {summary.data?.lastScannedAt
          ? new Date(summary.data.lastScannedAt).toLocaleString()
          : 'never'}
        {' · '}reports: {summary.data?.reportCount ?? 0}
        {' · '}snapshots stored: {history.data?.totalCount ?? 0}
      </div>

      {/* What changed since the last scan (sprint 081). Two cards
          side-by-side: severity-delta tiles + the per-scan sparkline.
          Renders "first scan, no comparison yet" gracefully when
          there's only one snapshot. */}
      <section className="grid grid-cols-1 lg:grid-cols-2 gap-3">
        {/* Delta card */}
        <div className="border border-border rounded-lg p-4 space-y-3">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-medium text-foreground">What changed in the last 24h</h3>
            {diff.data?.hasComparison && diff.data.prior && (
              <span className="text-xs text-muted-foreground">
                vs {new Date(diff.data.prior.scannedAt).toLocaleString()}
              </span>
            )}
          </div>
          {!diff.data || !diff.data.hasComparison ? (
            <p className="text-xs text-muted-foreground">
              Not enough scan history yet — we&apos;ll surface a diff once a second snapshot lands (typically within an hour of trivy-operator&apos;s schedule).
            </p>
          ) : (
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
              {(['critical', 'high', 'medium', 'low'] as const).map((sev) => {
                const d = diff.data.delta?.[sev] ?? 0;
                const tone =
                  d > 0
                    ? 'text-red-500 border-red-500/30 bg-red-500/5'
                    : d < 0
                      ? 'text-emerald-500 border-emerald-500/30 bg-emerald-500/5'
                      : 'text-muted-foreground border-border';
                const Icon = d > 0 ? TrendingUp : d < 0 ? TrendingDown : Minus;
                return (
                  <div key={sev} className={`border rounded p-2 ${tone}`}>
                    <div className="text-[10px] uppercase tracking-wide opacity-80">{sev}</div>
                    <div className="flex items-baseline justify-between mt-1">
                      <div className="text-xl font-semibold tabular-nums">
                        {d > 0 ? '+' : ''}
                        {d}
                      </div>
                      <Icon className="h-3.5 w-3.5" />
                    </div>
                    <div className="text-[10px] opacity-70 mt-0.5">
                      {diff.data.prior?.[sev] ?? 0} → {diff.data.latest?.[sev] ?? 0}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* Sparkline + history list */}
        <div className="border border-border rounded-lg p-4 space-y-3">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-medium text-foreground">Scan history (30 days)</h3>
            <span className="text-xs text-muted-foreground">
              {history.data?.totalCount ?? 0} scans
            </span>
          </div>
          {(!history.data || history.data.snapshots.length === 0) ? (
            <p className="text-xs text-muted-foreground">
              No history yet — once trivy-operator publishes a second VulnerabilityReport this chart will show the trend.
            </p>
          ) : (
            <>
              {/* Critical+High sparkline. Inline SVG so we don't drag
                  in a chart library for a 10-point trend line. The
                  y-axis is normalised to the max critical count in
                  the window so a small swing reads as a visible
                  movement instead of a flat line. */}
              <HistorySparkline points={history.data.snapshots.slice().reverse()} />
              {/* Recent scans table */}
              <div className="text-xs space-y-1 max-h-32 overflow-y-auto pr-1">
                {history.data.snapshots.slice(0, 6).map((p) => (
                  <div
                    key={p.scannedAt}
                    className="flex items-center justify-between gap-2 py-0.5"
                  >
                    <span className="text-muted-foreground tabular-nums">
                      {new Date(p.scannedAt).toLocaleString()}
                    </span>
                    <span className="flex items-center gap-1.5 text-foreground">
                      <span className="text-red-500 font-medium tabular-nums">{p.critical}</span>
                      <span className="text-orange-500 tabular-nums">{p.high}</span>
                      <span className="text-amber-500 tabular-nums">{p.medium}</span>
                      <span className="text-sky-500 tabular-nums">{p.low}</span>
                    </span>
                  </div>
                ))}
              </div>
            </>
          )}
        </div>
      </section>

      {/* Filters */}
      <section className="flex items-center gap-3 flex-wrap">
        <label className="text-sm text-muted-foreground">Namespace</label>
        <select
          className="border border-border bg-background rounded-md px-2 py-1 text-sm"
          value={namespace}
          onChange={(e) => setNamespace(e.target.value)}
        >
          <option value="">All namespaces</option>
          {namespaces.map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
        <label className="text-sm text-muted-foreground ml-4">CVE severity</label>
        <select
          className="border border-border bg-background rounded-md px-2 py-1 text-sm"
          value={severityFilter}
          onChange={(e) => setSeverityFilter(e.target.value as CVESeverity | '')}
        >
          <option value="">All</option>
          <option value="CRITICAL">Critical only</option>
          <option value="HIGH">High only</option>
          <option value="MEDIUM">Medium only</option>
          <option value="LOW">Low only</option>
        </select>
      </section>

      {/* Top images */}
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
            {images.isLoading && (
              <TableRow>
                <TableCell colSpan={7} className="px-3 py-6 text-center text-muted-foreground">
                  <Loader2 className="inline h-4 w-4 animate-spin mr-2" />
                  Loading…
                </TableCell>
              </TableRow>
            )}
            {!images.isLoading && (images.data?.items.length ?? 0) === 0 && (
              <TableRow>
                <TableCell colSpan={7} className="px-3 py-8 text-center">
                  <div className="inline-flex flex-col items-center gap-2 text-muted-foreground">
                    <ShieldAlert className="h-6 w-6" />
                    <div className="font-medium text-foreground">No vulnerability reports yet</div>
                    <p className="text-xs max-w-md">
                      Install trivy-operator on this cluster and reports will populate within the
                      first scan window (typically 5–15 min). Already installed? Use the rescan
                      button above.
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
            {images.data?.items.map((r) => {
              const total =
                r.criticalCount + r.highCount + r.mediumCount + r.lowCount + r.unknownCount;
              return (
                <TableRow
                  key={r.id}
                  className="border-t border-border hover:bg-muted/40 cursor-pointer"
                  onClick={() => setOpenReport(r)}
                >
                  <TableCell className="px-3 py-2 font-mono text-xs">
                    {r.imageRepo}:{r.imageTag}
                  </TableCell>
                  <TableCell className="px-3 py-2">{r.namespace}</TableCell>
                  <TableCell className="px-3 py-2">
                    {r.workloadKind} / {r.workloadName}
                  </TableCell>
                  <TableCell className="px-3 py-2 text-right font-semibold text-red-500">
                    {r.criticalCount}
                  </TableCell>
                  <TableCell className="px-3 py-2 text-right font-semibold text-orange-500">
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

      {/* Per-image drawer */}
      {openReport && (
        <aside className="fixed right-0 top-0 z-40 h-full w-full max-w-2xl bg-background border-l border-border shadow-xl overflow-y-auto">
          <header className="sticky top-0 bg-background border-b border-border px-4 py-3 flex items-start justify-between gap-2">
            <div>
              <h2 className="text-lg font-semibold">
                {openReport.imageRepo}:{openReport.imageTag}
              </h2>
              <p className="text-xs text-muted-foreground font-mono mt-1">
                {openReport.imageDigest || '(no digest)'}
              </p>
              <p className="text-xs text-muted-foreground">
                {openReport.namespace} · {openReport.workloadKind}/{openReport.workloadName}
              </p>
            </div>
            <button
              className="p-1 rounded hover:bg-muted"
              onClick={() => setOpenReport(null)}
              aria-label="Close"
            >
              <X className="h-4 w-4" />
            </button>
          </header>
          <div className="p-4 space-y-3">
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
                  {(reportHistory.data?.totalCount ?? 0) === 1 ? '' : 's'}
                </span>
              </div>
              {reportHistory.isLoading && (
                <div className="text-xs text-muted-foreground">
                  <Loader2 className="inline h-3 w-3 animate-spin mr-1.5" />
                  Loading history…
                </div>
              )}
              {reportHistory.data && reportHistory.data.snapshots.length === 0 && (
                <p className="text-xs text-muted-foreground">
                  No snapshots yet — once trivy-operator re-scans this workload its history will appear here.
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
                          <span className="text-red-500 font-medium tabular-nums" title="Critical">{p.critical}</span>
                          <span className="text-orange-500 tabular-nums" title="High">{p.high}</span>
                          <span className="text-amber-500 tabular-nums" title="Medium">{p.medium}</span>
                          <span className="text-sky-500 tabular-nums" title="Low">{p.low}</span>
                        </span>
                      </div>
                    ))}
                  </div>
                </>
              )}
            </section>

            {reportDetail.isLoading && (
              <div className="text-sm text-muted-foreground">
                <Loader2 className="inline h-4 w-4 animate-spin mr-2" />
                Loading CVEs…
              </div>
            )}
            {reportDetail.data && (
              <>
                <div className="text-xs text-muted-foreground">
                  {reportDetail.data.vulnerabilityTotal} CVE
                  {reportDetail.data.vulnerabilityTotal === 1 ? '' : 's'} matching filter
                </div>
                <ul className="space-y-2">
                  {reportDetail.data.vulnerabilities.map((c) => (
                    <li
                      key={c.id}
                      className={`border rounded-md p-3 ${cveToneFor(c.severity)}`}
                    >
                      <div className="flex items-center justify-between gap-2">
                        <a
                          href={c.primaryLink || '#'}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="font-mono text-sm font-semibold underline"
                        >
                          {c.vulnerabilityId}
                        </a>
                        <span className="text-xs uppercase tracking-wide">
                          {c.severity}
                          {c.cvssScore != null && ` · CVSS ${c.cvssScore.toFixed(1)}`}
                        </span>
                      </div>
                      {c.title && (
                        <div className="mt-1 text-sm">{c.title}</div>
                      )}
                      <div className="mt-1 text-xs text-muted-foreground font-mono">
                        {c.pkgName} {c.installedVersion} → fixed in{' '}
                        {c.fixedVersion || '(no fix yet)'}
                      </div>
                    </li>
                  ))}
                </ul>
                {reportDetail.data.vulnerabilities.length === 0 && (
                  <div className="rounded-md border border-border p-3 text-sm text-muted-foreground inline-flex items-center gap-2">
                    <AlertTriangle className="h-4 w-4" />
                    No CVEs match the current filter.
                  </div>
                )}
              </>
            )}
          </div>
        </aside>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------
// HistorySparkline — minimal inline SVG trend line for the scan
// history card. Two stacked polylines (critical + high), normalised
// to the max across the window so small swings still register.
// Inline rather than dragging in recharts/visx because (a) we only
// need a 60×30 sparkline, (b) it stays consistent with the other
// metric tiles on the cluster overview (which don't ship a chart
// library either), (c) ~30 lines beats ~300KB on the bundle.
// ---------------------------------------------------------------------
function HistorySparkline({
  points,
}: {
  points: Array<{ scannedAt: string; critical: number; high: number }>;
}) {
  if (points.length < 2) {
    return (
      <div className="h-14 flex items-center justify-center text-xs text-muted-foreground">
        At least 2 scans needed to render a trend
      </div>
    );
  }
  const width = 320;
  const height = 56;
  const padX = 4;
  const padY = 4;
  const innerW = width - padX * 2;
  const innerH = height - padY * 2;
  const maxY = Math.max(
    1,
    ...points.map((p) => p.critical),
    ...points.map((p) => p.high),
  );
  const xs = (i: number) =>
    padX + (i * innerW) / Math.max(1, points.length - 1);
  const ys = (v: number) => padY + innerH - (v / maxY) * innerH;
  const path = (key: 'critical' | 'high') =>
    points.map((p, i) => `${i === 0 ? 'M' : 'L'} ${xs(i)} ${ys(p[key])}`).join(' ');

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      className="w-full h-14"
      preserveAspectRatio="none"
      aria-label="Scan history trend"
    >
      {/* baseline */}
      <line
        x1={padX}
        x2={width - padX}
        y1={height - padY}
        y2={height - padY}
        stroke="currentColor"
        strokeOpacity={0.1}
      />
      {/* high (orange) — drawn under so critical is on top */}
      <path
        d={path('high')}
        fill="none"
        stroke="#f97316"
        strokeWidth={1.5}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      {/* critical (red) */}
      <path
        d={path('critical')}
        fill="none"
        stroke="#dc2626"
        strokeWidth={1.5}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      {/* dots on the latest point so the user knows where "now" is */}
      <circle cx={xs(points.length - 1)} cy={ys(points[points.length - 1].critical)} r={2.5} fill="#dc2626" />
      <circle cx={xs(points.length - 1)} cy={ys(points[points.length - 1].high)} r={2.5} fill="#f97316" />
    </svg>
  );
}

// Sprint 081 — live scan-in-progress banner. Three render states map
// 1:1 to the operator's mental model of trivy: actively scanning,
// idle+up-to-date, operator-not-ready. Polling logic lives in the
// parent (3s while scanning, 30s idle); this component stays pure.
function ScanProgressBanner({
  clusterId,
  progress,
  dispatchedRecently,
}: {
  clusterId: string;
  progress?: import('@/lib/api/cluster-detail').ImageVulnProgress;
  dispatchedRecently?: boolean;
}) {
  if (!progress) {
    return (
      <div className="rounded-lg border border-border bg-muted/30 px-4 py-2 text-sm text-muted-foreground">
        Loading scan state…
      </div>
    );
  }
  // "dispatched" state — operator clicked rescan in the last 30s but
  // the backend hasn't observed scanning jobs yet. Most likely trivy
  // hasn't created the Jobs yet (~1-3s lag) OR the scan already
  // finished between our polls. Either way, surface the click so the
  // user knows their action was received.
  if (dispatchedRecently && !progress.scanning) {
    return (
      <div className="rounded-lg border border-sky-500/40 bg-sky-500/5 px-4 py-3 text-sm flex items-center gap-3">
        <Loader2 className="h-4 w-4 animate-spin text-sky-500 flex-shrink-0" />
        <span className="text-foreground">
          Rescan dispatched — waiting for trivy-operator to spawn new scan jobs…
        </span>
      </div>
    );
  }
  if (!progress.trivyOperatorReady) {
    // Trivy is opt-in: a cluster may simply not have it (the operator may run
    // a different scanner like NeuVector, or none). Make this a calm, clearly
    // actionable notice rather than an error — and point straight to Tools.
    return (
      <div className="rounded-lg border border-sky-500/40 bg-sky-500/5 px-4 py-3 text-sm flex items-start gap-3">
        <ShieldAlert className="h-4 w-4 text-sky-500 flex-shrink-0 mt-0.5" />
        <div className="flex-1">
          <div className="font-medium text-foreground">Image scanning isn&apos;t enabled on this cluster</div>
          <div className="text-xs text-muted-foreground mt-0.5">
            Astronomer&apos;s built-in scanning uses the Trivy operator, which
            isn&apos;t installed here — so there are no vulnerability reports. If
            you already use a different scanner (e.g. NeuVector), you can ignore
            this. To turn on Astronomer scanning, install Trivy from the Tools
            tab; reports appear within the first scan window (~5–15 min).
          </div>
          <a
            href={`/dashboard/clusters/${clusterId}/tools`}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 mt-2 rounded-md bg-primary text-primary-foreground text-xs font-medium hover:opacity-90"
          >
            Enable Trivy from Tools
          </a>
        </div>
      </div>
    );
  }
  if (progress.scanning) {
    const total = progress.activeJobs + progress.completedJobs + progress.failedJobs;
    const done = progress.completedJobs + progress.failedJobs;
    const pct = total > 0 ? Math.round((done / total) * 100) : 0;
    return (
      <div className="rounded-lg border border-sky-500/40 bg-sky-500/5 px-4 py-3 text-sm space-y-2">
        <div className="flex items-center justify-between gap-3">
          <div className="flex items-center gap-2 text-foreground">
            <Loader2 className="h-4 w-4 animate-spin text-sky-500" />
            <span className="font-medium">
              Scanning {progress.activeJobs} workload{progress.activeJobs === 1 ? '' : 's'}…
            </span>
            <span className="text-xs text-muted-foreground tabular-nums">
              ({done}/{total} complete{progress.failedJobs > 0 ? `, ${progress.failedJobs} failed` : ''})
            </span>
          </div>
          <span className="text-xs text-muted-foreground tabular-nums">{pct}%</span>
        </div>
        <div className="h-1.5 w-full rounded-full bg-sky-500/15 overflow-hidden">
          <div
            className="h-full bg-sky-500 transition-all duration-500"
            style={{ width: total > 0 ? `${Math.max(5, pct)}%` : '50%' }}
          />
        </div>
      </div>
    );
  }
  const age = progress.lastScanAgeSeconds;
  let ageStr = 'never';
  if (age != null) {
    if (age < 60) ageStr = `${age}s ago`;
    else if (age < 3600) ageStr = `${Math.round(age / 60)}m ago`;
    else if (age < 86400) ageStr = `${Math.round(age / 3600)}h ago`;
    else ageStr = `${Math.round(age / 86400)}d ago`;
  }
  return (
    <div className="rounded-lg border border-emerald-500/40 bg-emerald-500/5 px-4 py-2.5 text-sm flex items-center gap-2">
      <ShieldAlert className="h-4 w-4 text-emerald-500 flex-shrink-0" />
      <span className="text-foreground">
        All scans current — {progress.reportsCount} workload{progress.reportsCount === 1 ? '' : 's'} indexed, last scan {ageStr}.
      </span>
    </div>
  );
}

export const Route = createFileRoute('/dashboard/clusters/$id/image-scans/')({
  component: ClusterImageScansPage,
});
