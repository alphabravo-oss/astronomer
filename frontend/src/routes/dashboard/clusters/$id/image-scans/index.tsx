import { createFileRoute } from "@tanstack/react-router";
import { InfoCallout } from "@/components/ui/info-callout";
import { PageHeader, PageShell } from "@/components/ui/page";
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

import { useMemo, useState } from "react";
import { useClock } from "@/lib/hooks/use-clock";
import { QueryStates } from "@/components/ui/query-states";
import { Loader2, RefreshCw } from "lucide-react";
import { useCluster } from "@/lib/hooks/clusters";
import {
  exportImageVulnsCSVPath,
  type CVESeverity,
  type ImageVulnReport,
} from "@/lib/api/cluster-vulnerabilities";
import { Download } from "lucide-react";
import { Tooltip } from "@/components/ui/tooltip";
import { ScanProgressBanner } from "./-scan-progress-banner";
import { useImageScanQueries } from "./-use-image-scans";
import {
  DiffCard,
  HistoryCard,
  ImageDrawer,
  ImagesTable,
  LocalClusterScanUnavailable,
  SeverityTiles,
} from "./-sections";
import { Select } from "@/components/ui/select";
import { BareButton } from "@/components/form/bare-button";

function ClusterImageScansPage() {
  const now = useClock(1000);
  const params = Route.useParams();
  const clusterId = params.id;
  const { data: cluster } = useCluster(clusterId);

  const [namespace, setNamespace] = useState<string>("");
  const [severityFilter, setSeverityFilter] = useState<CVESeverity | "">("");
  const [openReport, setOpenReport] = useState<ImageVulnReport | null>(null);
  const scansEnabled = !!cluster && !cluster.isLocal;

  const {
    summary,
    images,
    reportHistory,
    rescan,
    history,
    diff,
    progress,
    lastRescanAt,
  } = useImageScanQueries({ clusterId, scansEnabled, namespace, openReport });

  // Distinct namespace pick list, derived from the loaded reports.
  const namespaces = useMemo(
    () => reportNamespaces(images.data?.data ?? []),
    [images.data],
  );

  // Selection belongs to the current result set, not an asynchronous effect.
  if (
    openReport &&
    images.data &&
    !images.data.data.some((r) => r.id === openReport.id)
  ) {
    setOpenReport(null);
  }

  if (cluster?.isLocal) return <LocalClusterScanUnavailable />;

  if (scansEnabled && summary.isError)
    return (
      <QueryStates query={summary} permission="image_scans:read">
        {() => null}
      </QueryStates>
    );
  if (scansEnabled && images.isError)
    return (
      <QueryStates query={images} permission="image_scans:read">
        {() => null}
      </QueryStates>
    );

  return (
    <PageShell>
      <p className="sr-only" role="status" aria-live="polite">
        {rescan.isPending
          ? `Vulnerability rescan ${rescan.operationState.phase}`
          : ""}
      </p>
      <PageHeader
        title="Image Scans"
        description="Aggregated CVE counts from the in-cluster Trivy operator. Astronomer ingests VulnerabilityReport CRDs continuously."
        actions={
          <>
            <Tooltip content="Download all current image-scan rows as CSV">
              <a
                className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded-md border border-border bg-background hover:bg-muted"
                href={exportImageVulnsCSVPath(clusterId)}
                download
              >
                <Download className="h-4 w-4" />
                Export CSV
              </a>
            </Tooltip>
            <BareButton
              className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded-md border border-border bg-background hover:bg-muted disabled:opacity-50 font-normal"
              onClick={() => rescan.mutate()}
              disabled={rescan.isPending}
            >
              {rescan.isPending ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <RefreshCw className="h-4 w-4" />
              )}
              Trigger rescan
            </BareButton>
          </>
        }
      />

      <InfoCallout>
        Cluster-wide security inventory. Use this page’s namespace filter; the
        navigation namespace selection does not filter these reports.
      </InfoCallout>

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
        dispatchedRecently={!!lastRescanAt && now - lastRescanAt < 30_000}
      />

      <SeverityTiles isLoading={summary.isLoading} summary={summary.data} />

      <div className="text-xs text-muted-foreground">
        Last scan:{" "}
        {summary.data?.lastScannedAt
          ? new Date(summary.data.lastScannedAt).toLocaleString()
          : "never"}
        {" · "}reports: {summary.data?.reportCount ?? 0}
        {" · "}snapshots stored: {history.data?.totalCount ?? 0}
      </div>

      <section className="grid grid-cols-1 lg:grid-cols-2 gap-3">
        <DiffCard diff={diff.data} />
        <HistoryCard history={history.data} />
      </section>

      {/* Filters */}
      <section className="flex items-center gap-3 flex-wrap">
        <label
          className="text-sm text-muted-foreground"
          htmlFor="field-1a64459b-394"
        >
          Namespace
        </label>
        <Select
          id="field-1a64459b-394"
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
        </Select>
      </section>

      <ImagesTable
        clusterId={clusterId}
        isLoading={images.isLoading}
        rows={images.data?.data}
        onOpen={setOpenReport}
      />

      {openReport && (
        <ImageDrawer
          clusterId={clusterId}
          report={openReport}
          severityFilter={severityFilter}
          onSeverityChange={setSeverityFilter}
          onClose={() => setOpenReport(null)}
          reportHistory={reportHistory}
        />
      )}
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/clusters/$id/image-scans/")({
  component: ClusterImageScansPage,
});

function reportNamespaces(reports: Array<{ namespace?: string }>) {
  return [
    ...new Set(
      reports.flatMap((report) => (report.namespace ? [report.namespace] : [])),
    ),
  ].sort();
}
