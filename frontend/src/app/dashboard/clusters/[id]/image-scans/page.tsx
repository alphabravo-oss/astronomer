'use client';

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
import { useParams } from 'next/navigation';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import {
  AlertTriangle,
  Loader2,
  RefreshCw,
  ShieldAlert,
  X,
} from 'lucide-react';

import { useCluster } from '@/lib/hooks';
import {
  getImageVulnReport,
  getImageVulnSummary,
  listVulnerableImages,
  triggerImageVulnRescan,
  type CVESeverity,
  type ImageVulnReport,
  type ImageVulnSummary,
} from '@/lib/api/cluster-detail';

const qk = {
  summary: (id: string) => ['clusters', id, 'image-vulns', 'summary'] as const,
  images: (id: string, ns: string) =>
    ['clusters', id, 'image-vulns', 'images', ns] as const,
  report: (id: string, reportId: string, sev: string) =>
    ['clusters', id, 'image-vulns', 'report', reportId, sev] as const,
};

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

export default function ClusterImageScansPage() {
  const params = useParams();
  const clusterId = params.id as string;
  const queryClient = useQueryClient();
  const { data: cluster } = useCluster(clusterId);

  const [namespace, setNamespace] = useState<string>('');
  const [severityFilter, setSeverityFilter] = useState<CVESeverity | ''>('');
  const [openReport, setOpenReport] = useState<ImageVulnReport | null>(null);

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

  const summary = useQuery({
    queryKey: qk.summary(clusterId),
    queryFn: () => getImageVulnSummary(clusterId),
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
  });

  const images = useQuery({
    queryKey: qk.images(clusterId, namespace),
    queryFn: () => listVulnerableImages(clusterId, { namespace: namespace || undefined, limit: 20 }),
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
  });

  const reportDetail = useQuery({
    queryKey: openReport
      ? qk.report(clusterId, openReport.id, severityFilter || '')
      : ['noop'],
    queryFn: () =>
      openReport
        ? getImageVulnReport(clusterId, openReport.id, {
            severity: (severityFilter || undefined) as CVESeverity | undefined,
            limit: 100,
          })
        : Promise.resolve(null),
    enabled: !!openReport,
  });

  const rescan = useMutation({
    mutationFn: () => triggerImageVulnRescan(clusterId),
    onSuccess: (data) => {
      if (data.triggered) {
        toast.success('Trivy operator nudged — re-scans will appear shortly');
      } else {
        toast.warning(`Rescan not triggered: ${data.reason ?? 'unknown'}`);
      }
      queryClient.invalidateQueries({ queryKey: qk.summary(clusterId) });
    },
    onError: (err) => toast.error(`Rescan failed: ${(err as Error).message}`),
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
      </header>

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
      </div>

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
        <table className="w-full text-sm">
          <thead className="bg-muted/50 text-left text-xs uppercase tracking-wide">
            <tr>
              <th className="px-3 py-2">Image</th>
              <th className="px-3 py-2">Namespace</th>
              <th className="px-3 py-2">Workload</th>
              <th className="px-3 py-2 text-right">Critical</th>
              <th className="px-3 py-2 text-right">High</th>
              <th className="px-3 py-2 text-right">Total</th>
              <th className="px-3 py-2">Scanned</th>
            </tr>
          </thead>
          <tbody>
            {images.isLoading && (
              <tr>
                <td colSpan={7} className="px-3 py-6 text-center text-muted-foreground">
                  <Loader2 className="inline h-4 w-4 animate-spin mr-2" />
                  Loading…
                </td>
              </tr>
            )}
            {!images.isLoading && (images.data?.items.length ?? 0) === 0 && (
              <tr>
                <td colSpan={7} className="px-3 py-6 text-center text-muted-foreground">
                  No vulnerability reports yet — install trivy-operator from
                  Catalog or wait for the next scan window.
                </td>
              </tr>
            )}
            {images.data?.items.map((r) => {
              const total =
                r.criticalCount + r.highCount + r.mediumCount + r.lowCount + r.unknownCount;
              return (
                <tr
                  key={r.id}
                  className="border-t border-border hover:bg-muted/40 cursor-pointer"
                  onClick={() => setOpenReport(r)}
                >
                  <td className="px-3 py-2 font-mono text-xs">
                    {r.imageRepo}:{r.imageTag}
                  </td>
                  <td className="px-3 py-2">{r.namespace}</td>
                  <td className="px-3 py-2">
                    {r.workloadKind} / {r.workloadName}
                  </td>
                  <td className="px-3 py-2 text-right font-semibold text-red-500">
                    {r.criticalCount}
                  </td>
                  <td className="px-3 py-2 text-right font-semibold text-orange-500">
                    {r.highCount}
                  </td>
                  <td className="px-3 py-2 text-right">{total}</td>
                  <td className="px-3 py-2 text-xs text-muted-foreground">
                    {new Date(r.scannedAt).toLocaleString()}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
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
