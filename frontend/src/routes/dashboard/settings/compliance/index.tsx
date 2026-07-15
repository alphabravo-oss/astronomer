import { createFileRoute } from '@tanstack/react-router';
/**
 * /dashboard/settings/compliance — audit export bundles.
 *
 * The current backend streams the ZIP body inline (200). The polling code is
 * retained for future durable background export jobs, but production exports
 * download directly today.
 */
import { useEffect, useRef, useState } from 'react';
import { Link } from '@/lib/link';
import {
  ArrowLeft,
  Download,
  FileArchive,
  Loader2,
} from 'lucide-react';
import { toastError, toastInfo, toastSuccess } from '@/lib/toast';
import { cn, formatBytes, formatRelativeTime } from '@/lib/utils';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import {
  downloadComplianceExportBlob,
  getComplianceExport,
  requestComplianceExport,
  type ComplianceExportSummary,
} from '@/lib/api/settings';

function todayIso() {
  return new Date().toISOString().slice(0, 10);
}

function thirtyDaysAgoIso() {
  const d = new Date();
  d.setDate(d.getDate() - 30);
  return d.toISOString().slice(0, 10);
}

function downloadBlob(blob: Blob, filename: string) {
  const url = window.URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  window.URL.revokeObjectURL(url);
}

function StatusPill({ status }: { status: ComplianceExportSummary['status'] }) {
  const palette: Record<string, string> = {
    pending: 'bg-muted text-muted-foreground border-border',
    running: 'bg-blue-500/10 text-blue-600 dark:text-blue-400 border-blue-500/30',
    ready: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border-emerald-500/30',
    failed: 'bg-rose-500/10 text-rose-600 dark:text-rose-400 border-rose-500/30',
  };
  const key = status ?? 'pending';
  return (
    <span className={cn('text-xs px-2 py-0.5 rounded border font-medium capitalize', palette[key])}>
      {key}
    </span>
  );
}

function ComplianceForm() {
  const [from, setFrom] = useState(thirtyDaysAgoIso());
  const [to, setTo] = useState(todayIso());
  const [submitting, setSubmitting] = useState(false);
  const [job, setJob] = useState<ComplianceExportSummary | null>(null);
  const pollTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Poll the background job every 3s while it's still working. Resolves
  // either by surfacing the download URL on completion, or by toasting an
  // error if the backend gives up.
  useEffect(() => {
    if (!job || job.status === 'ready' || job.status === 'failed') return;
    pollTimer.current = setTimeout(async () => {
      try {
        const refreshed = await getComplianceExport(job.id);
        setJob(refreshed);
      } catch (err) {
        const msg = err instanceof Error ? err.message : 'Polling failed';
        toastError(msg);
      }
    }, 3000);
    return () => {
      if (pollTimer.current) clearTimeout(pollTimer.current);
    };
  }, [job]);

  const handleExport = async () => {
    if (!from || !to) {
      toastError('Both dates are required');
      return;
    }
    if (from > to) {
      toastError('"From" must be before "to"');
      return;
    }
    setSubmitting(true);
    setJob(null);
    try {
      const result = await requestComplianceExport({ from, to });
      if (result.kind === 'blob') {
        downloadBlob(result.blob, result.filename);
        toastSuccess('Export downloaded');
      } else {
        setJob(result.job);
        toastInfo('Export queued — polling for completion');
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Failed to start export';
      toastError(msg);
    } finally {
      setSubmitting(false);
    }
  };

  const handleDownloadReady = async () => {
    if (!job || !job.downloadUrl) return;
    try {
      const blob = await downloadComplianceExportBlob(job.downloadUrl);
      downloadBlob(blob, `compliance-${job.from}_${job.to}.zip`);
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Download failed';
      toastError(msg);
    }
  };

  return (
    <div className="space-y-6">
      <div className="rounded-xl border border-border bg-card p-6 space-y-4">
        <div>
          <h2 className="text-base font-semibold text-foreground">Export range</h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Includes RBAC config, audit log, platform settings, and webhook history for the
            chosen window. The result is a signed ZIP suitable for compliance archives.
          </p>
        </div>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">From</label>
            <input
              type="date"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
              className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
            />
          </div>
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">To</label>
            <input
              type="date"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
            />
          </div>
        </div>
        <div className="flex justify-end">
          <button
            type="button"
            onClick={handleExport}
            disabled={submitting}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {submitting ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Download className="h-3.5 w-3.5" />}
            Export
          </button>
        </div>
      </div>

      {job && (
        <div className="rounded-xl border border-border bg-card p-6 space-y-4">
          <div className="flex items-start justify-between gap-3">
            <div>
              <div className="flex items-center gap-2">
                <h2 className="text-base font-semibold text-foreground">Background export</h2>
                <StatusPill status={job.status} />
              </div>
              <p className="text-xs text-muted-foreground mt-0.5">
                Queued {formatRelativeTime(job.createdAt)}
                {job.completedAt && ` · completed ${formatRelativeTime(job.completedAt)}`}
                {job.sizeBytes != null && ` · ${formatBytes(job.sizeBytes)}`}
              </p>
            </div>
            {job.status === 'ready' && (
              <button
                type="button"
                onClick={handleDownloadReady}
                className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity"
              >
                <Download className="h-3.5 w-3.5" />
                Download ZIP
              </button>
            )}
          </div>
          {job.progress != null && job.status !== 'ready' && (
            <div className="space-y-1">
              <div className="h-1.5 rounded-full bg-muted overflow-hidden">
                <div
                  className="h-full bg-blue-500 transition-all"
                  style={{ width: `${Math.max(0, Math.min(100, job.progress))}%` }}
                />
              </div>
              <p className="text-xs text-muted-foreground tabular-nums">{Math.round(job.progress)}%</p>
            </div>
          )}
          {job.status === 'failed' && job.errorMessage && (
            <p className="text-sm text-status-error">{job.errorMessage}</p>
          )}
        </div>
      )}
    </div>
  );
}

function CompliancePage() {
  return (
    <SettingsAuthGate>
      <div className="max-w-3xl mx-auto space-y-6">
        <Link
          href="/dashboard/settings"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Settings
        </Link>
        <div>
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Settings · Compliance</p>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1 flex items-center gap-2">
            <FileArchive className="h-5 w-5 text-muted-foreground" />
            Compliance exports
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            Build a ZIP of audit + RBAC + config for a date range. Large windows may take
            longer, but the export downloads directly when complete.
          </p>
        </div>
        <ComplianceForm />
        <div className="border rounded p-4 bg-card">
          <h2 className="font-semibold text-sm">Compliance baselines</h2>
          <p className="text-sm text-muted-foreground mt-1">
            One-click preset profiles (PCI-DSS, HIPAA, FedRAMP, SOC 2) that snapshot
            and apply the related platform settings, quota plans, audit retention,
            and alert rules.
          </p>
          <Link
            href="/dashboard/settings/compliance/baselines"
            className="inline-block mt-3 text-sm px-3 py-1.5 rounded border bg-background hover:bg-muted"
          >
            Open baselines
          </Link>
        </div>
      </div>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute('/dashboard/settings/compliance/')({
  component: CompliancePage,
});
