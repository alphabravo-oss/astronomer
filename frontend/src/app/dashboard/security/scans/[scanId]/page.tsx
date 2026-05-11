'use client';

import { useMemo, useState } from 'react';
import Link from 'next/link';
import { useParams, useRouter } from 'next/navigation';
import { useCISScan, useClusters, useCreateCISScan } from '@/lib/hooks';
import { useLiveQueryInvalidation } from '@/lib/live-events';
import { cisScanReportCSVUrl } from '@/lib/api';
import { StatusBadge } from '@/components/ui/status-badge';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { DataTable, type Column } from '@/components/ui/data-table';
import {
  severityClass,
  findingStatusClass,
  severityRank,
  SEVERITY_ORDER,
  STATUS_ORDER,
} from '@/components/security/severity';
import { formatDate, cn } from '@/lib/utils';
import type { CISFinding, CISFindingSeverity, CISFindingStatus } from '@/types';
import {
  ArrowLeft,
  ChevronRight,
  Download,
  RefreshCw,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  MinusCircle,
  ChevronDown,
  Loader2,
} from 'lucide-react';

/**
 * Phase B5 — single CIS scan detail page.
 *
 * The query hook (`useCISScan`) handles polling every 10s while `status` is
 * non-terminal and stops on completion / failure. We additionally subscribe
 * to `cluster.k8s_changed` for the scan's cluster — the cis-operator emits
 * a ClusterScanReport mutation when results are ready, which triggers a
 * refetch even between polls.
 */
export default function ScanDetailPage() {
  const params = useParams<{ scanId: string }>();
  const scanId = params.scanId;
  const router = useRouter();

  const { data: scan, isLoading, error } = useCISScan(scanId);
  const { data: clustersPage } = useClusters({ pageSize: 200 });
  const createScan = useCreateCISScan();

  const cluster = useMemo(
    () => clustersPage?.data.find((c) => c.id === scan?.clusterId),
    [clustersPage, scan?.clusterId],
  );

  const isTerminal = scan?.status === 'completed' || scan?.status === 'failed';

  // Live invalidate this exact scan when the cis-operator mutates state in
  // its cluster. Hooked after we know the cluster_id but kept at the top
  // level so the hook is mounted unconditionally.
  useLiveQueryInvalidation(
    'cluster.k8s_changed',
    scan && !isTerminal ? [['cis', 'scans', 'detail', scanId]] : [],
  );

  const [showRerun, setShowRerun] = useState(false);

  if (isLoading || !scan) {
    return (
      <div className="space-y-4">
        <div className="h-8 w-48 rounded bg-muted animate-pulse" />
        <div className="h-32 rounded-lg bg-muted animate-pulse" />
      </div>
    );
  }
  if (error) {
    return (
      <div className="rounded-lg border border-status-error/30 bg-status-error/5 p-4">
        <p className="text-sm text-status-error">
          Failed to load scan: {(error as Error)?.message ?? String(error)}
        </p>
      </div>
    );
  }

  async function handleRerun() {
    setShowRerun(false);
    if (!scan) return;
    const newScan = await createScan.mutateAsync({
      cluster_id: scan.clusterId,
      profile: scan.scanType,
    });
    router.push(`/dashboard/security/scans/${newScan.id}`);
  }

  return (
    <div className="space-y-6">
      {/* Breadcrumb */}
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Link href="/dashboard/security" className="hover:text-foreground transition-colors">
          Security
        </Link>
        <ChevronRight className="h-3.5 w-3.5" />
        <span className="text-foreground font-mono text-xs">
          {scan.clusterScanName ?? scan.id.slice(0, 8)}
        </span>
      </div>

      {/* Header */}
      <div className="flex items-start justify-between gap-4 flex-wrap">
        <div className="space-y-1">
          <div className="flex items-center gap-3 flex-wrap">
            <button
              onClick={() => router.push('/dashboard/security')}
              className="text-muted-foreground hover:text-foreground transition-colors"
              aria-label="Back"
            >
              <ArrowLeft className="h-4 w-4" />
            </button>
            <h1 className="text-2xl font-semibold text-foreground tracking-tight">
              {cluster?.displayName ?? cluster?.name ?? scan.clusterId.slice(0, 8)}
            </h1>
            <StatusBadge status={scan.status} />
            {scan.status === 'running' && (
              <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                <Loader2 className="h-3 w-3 animate-spin" />
                Polling for results
              </span>
            )}
          </div>
          <div className="flex items-center gap-3 text-xs text-muted-foreground">
            <span>
              Profile <span className="font-mono text-foreground">{scan.scanType}</span>
            </span>
            <span>·</span>
            {scan.startedAt && (
              <span>Started {formatDate(scan.startedAt, 'MMM d, HH:mm')}</span>
            )}
            {scan.completedAt && (
              <>
                <span>·</span>
                <span>Completed {formatDate(scan.completedAt, 'MMM d, HH:mm')}</span>
              </>
            )}
          </div>
        </div>

        <div className="flex items-center gap-2">
          {/* Anchor with `download` so the browser saves the CSV instead of
              navigating. The link goes through the API base URL so the auth
              cookie / proxy still applies. */}
          <a
            href={cisScanReportCSVUrl(scan.id)}
            download={`cis-scan-${scan.id}.csv`}
            className={cn(
              'inline-flex items-center gap-2 h-9 px-4 rounded-lg border border-border',
              'text-sm font-medium text-muted-foreground hover:text-foreground hover:bg-accent transition-colors',
              !isTerminal && 'opacity-50 pointer-events-none',
            )}
            aria-disabled={!isTerminal}
            title={isTerminal ? 'Download CSV report' : 'Available once the scan completes'}
          >
            <Download className="h-4 w-4" />
            Export CSV
          </a>
          <button
            type="button"
            onClick={() => setShowRerun(true)}
            disabled={createScan.isPending}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {createScan.isPending ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <RefreshCw className="h-4 w-4" />
            )}
            Re-run Scan
          </button>
        </div>
      </div>

      {scan.errorMessage && (
        <div className="rounded-md border border-status-error/30 bg-status-error/5 p-3">
          <p className="text-sm font-medium text-status-error">Scan failed</p>
          <p className="text-xs text-status-error/80 mt-1 font-mono">{scan.errorMessage}</p>
        </div>
      )}

      {/* Summary cards */}
      <SummaryStrip scan={scan} />

      {/* Findings table */}
      <FindingsSection findings={scan.findings ?? []} status={scan.status} />

      <ConfirmDialog
        open={showRerun}
        onClose={() => setShowRerun(false)}
        onConfirm={handleRerun}
        title="Re-run CIS scan?"
        description={`This will queue a new scan against ${cluster?.displayName ?? 'this cluster'} using the ${scan.scanType} profile.`}
        confirmText="Re-run"
        loading={createScan.isPending}
      />
    </div>
  );
}

function SummaryStrip({ scan }: { scan: import('@/types').CISScanDetail }) {
  const cells = [
    {
      label: 'Passed',
      value: scan.passed ?? 0,
      icon: CheckCircle2,
      color: 'text-status-success',
      bg: 'bg-status-success/5 border-status-success/20',
    },
    {
      label: 'Failed',
      value: scan.failed ?? 0,
      icon: XCircle,
      color: 'text-status-error',
      bg: 'bg-status-error/5 border-status-error/20',
    },
    {
      label: 'Warned',
      value: scan.warned ?? 0,
      icon: AlertTriangle,
      color: 'text-status-warning',
      bg: 'bg-status-warning/5 border-status-warning/20',
    },
    {
      label: 'Skipped',
      value: scan.skipped ?? 0,
      icon: MinusCircle,
      color: 'text-muted-foreground',
      bg: 'bg-muted/30 border-border',
    },
  ];

  return (
    <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
      {cells.map((c) => {
        const Icon = c.icon;
        return (
          <div
            key={c.label}
            className={cn('rounded-lg border p-4', c.bg)}
          >
            <div className="flex items-center gap-2">
              <Icon className={cn('h-4 w-4', c.color)} />
              <span className="text-xs text-muted-foreground">{c.label}</span>
            </div>
            <p className={cn('mt-2 text-3xl font-semibold tabular-nums', c.color)}>
              {c.value.toLocaleString()}
            </p>
          </div>
        );
      })}
    </div>
  );
}

function FindingsSection({
  findings,
  status,
}: {
  findings: CISFinding[];
  status: string;
}) {
  const [severityFilter, setSeverityFilter] = useState<CISFindingSeverity | 'all'>('all');
  const [statusFilter, setStatusFilter] = useState<CISFindingStatus | 'all'>('all');
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const filtered = useMemo(() => {
    return findings
      .filter((f) => {
        if (severityFilter !== 'all' && (f.severity ?? '').toLowerCase() !== severityFilter) {
          return false;
        }
        if (statusFilter !== 'all' && (f.status ?? '').toLowerCase() !== statusFilter) {
          return false;
        }
        return true;
      })
      .sort((a, b) => {
        // Group failed-critical first, then by severity within each status.
        const aFailed = (a.status ?? '').toLowerCase() === 'fail' ? 0 : 1;
        const bFailed = (b.status ?? '').toLowerCase() === 'fail' ? 0 : 1;
        if (aFailed !== bFailed) return aFailed - bFailed;
        return severityRank(a.severity) - severityRank(b.severity);
      });
  }, [findings, severityFilter, statusFilter]);

  const columns: Column<CISFinding>[] = [
    {
      key: 'expand',
      header: '',
      sortable: false,
      width: '32px',
      accessor: (row) => (
        <ChevronDown
          className={cn(
            'h-3.5 w-3.5 text-muted-foreground transition-transform',
            expanded.has(row.testId) && 'rotate-180',
          )}
        />
      ),
    },
    {
      key: 'testId',
      header: 'Test ID',
      width: '120px',
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">{row.testId}</span>
      ),
      sortAccessor: (row) => row.testId,
    },
    {
      key: 'description',
      header: 'Description',
      accessor: (row) => (
        <span className="text-sm text-foreground line-clamp-2">{row.description}</span>
      ),
      sortAccessor: (row) => row.description,
    },
    {
      key: 'severity',
      header: 'Severity',
      width: '100px',
      accessor: (row) => (
        <span
          className={cn(
            'inline-flex items-center px-2 py-0.5 rounded text-2xs font-medium uppercase tracking-wide',
            severityClass(row.severity),
          )}
        >
          {row.severity}
        </span>
      ),
      sortAccessor: (row) => severityRank(row.severity),
    },
    {
      key: 'status',
      header: 'Status',
      width: '90px',
      accessor: (row) => (
        <span
          className={cn(
            'inline-flex items-center px-2 py-0.5 rounded text-2xs font-medium uppercase',
            findingStatusClass(row.status),
          )}
        >
          {row.status}
        </span>
      ),
      sortAccessor: (row) => row.status,
    },
    {
      key: 'remediation',
      header: 'Remediation',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground line-clamp-1">
          {row.remediation || '—'}
        </span>
      ),
      sortable: false,
    },
  ];

  if (findings.length === 0) {
    return (
      <div className="rounded-lg border border-dashed border-border p-12 text-center">
        <p className="text-sm text-muted-foreground">
          {status === 'running' || status === 'pending'
            ? 'Scan in progress — findings will appear here when the cis-operator publishes the report.'
            : 'No findings for this scan.'}
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-3 flex-wrap">
        <h2 className="text-sm font-medium text-foreground">
          Findings <span className="text-muted-foreground tabular-nums">({filtered.length})</span>
        </h2>
        <div className="flex items-center gap-2">
          <FilterPills
            label="Severity"
            value={severityFilter}
            options={['all', ...SEVERITY_ORDER]}
            onChange={(v) => setSeverityFilter(v as CISFindingSeverity | 'all')}
          />
          <FilterPills
            label="Status"
            value={statusFilter}
            options={['all', ...STATUS_ORDER]}
            onChange={(v) => setStatusFilter(v as CISFindingStatus | 'all')}
          />
        </div>
      </div>

      <DataTable
        data={filtered}
        columns={columns}
        keyExtractor={(row) => row.testId}
        searchPlaceholder="Search test ID or description..."
        pageSize={50}
        emptyMessage="No findings match the current filters."
        onRowClick={(row) => {
          setExpanded((prev) => {
            const next = new Set(prev);
            if (next.has(row.testId)) next.delete(row.testId);
            else next.add(row.testId);
            return next;
          });
        }}
      />

      {/* Expanded remediation panels live below the table — `DataTable`
          doesn't support inline expansion, so we render a lightweight list
          of *currently expanded* findings. This keeps the table itself
          virtualization-friendly even with hundreds of rows. */}
      {expanded.size > 0 && (
        <div className="space-y-2">
          {Array.from(expanded).map((id) => {
            const f = findings.find((x) => x.testId === id);
            if (!f) return null;
            return (
              <div
                key={id}
                className="rounded-md border border-border bg-muted/20 p-4 space-y-2"
              >
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="font-mono text-xs text-muted-foreground">{f.testId}</span>
                  <span
                    className={cn(
                      'inline-flex items-center px-2 py-0.5 rounded text-2xs font-medium uppercase',
                      severityClass(f.severity),
                    )}
                  >
                    {f.severity}
                  </span>
                  <span
                    className={cn(
                      'inline-flex items-center px-2 py-0.5 rounded text-2xs font-medium uppercase',
                      findingStatusClass(f.status),
                    )}
                  >
                    {f.status}
                  </span>
                  <button
                    onClick={() =>
                      setExpanded((p) => {
                        const next = new Set(p);
                        next.delete(id);
                        return next;
                      })
                    }
                    className="ml-auto text-xs text-muted-foreground hover:text-foreground"
                  >
                    Collapse
                  </button>
                </div>
                <p className="text-sm text-foreground">{f.description}</p>
                {f.remediation && (
                  <div>
                    <p className="text-2xs font-medium text-muted-foreground uppercase tracking-wide mb-1">
                      Remediation
                    </p>
                    <p className="text-xs text-foreground whitespace-pre-wrap">{f.remediation}</p>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function FilterPills<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: T[];
  onChange: (next: T) => void;
}) {
  return (
    <div className="inline-flex items-center gap-1 rounded-md border border-border bg-card p-1">
      <span className="text-2xs text-muted-foreground px-1.5 hidden sm:inline">{label}:</span>
      {options.map((opt) => (
        <button
          key={String(opt)}
          type="button"
          onClick={() => onChange(opt)}
          className={cn(
            'px-2 py-0.5 rounded text-2xs font-medium uppercase transition-colors',
            value === opt
              ? 'bg-foreground text-background'
              : 'text-muted-foreground hover:text-foreground',
          )}
        >
          {String(opt)}
        </button>
      ))}
    </div>
  );
}

