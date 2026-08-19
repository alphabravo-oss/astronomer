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
  Plus,
} from 'lucide-react';
import { toastError, toastInfo, toastSuccess } from '@/lib/toast';
import { cn, formatBytes, formatRelativeTime, downloadBlob } from '@/lib/utils';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { ModalShell } from '@/components/ui/modal-shell';
import { PageHeader, PageShell } from '@/components/ui/page';
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

function StatusPill({ status }: { status: ComplianceExportSummary['status'] }) {
  const palette: Record<string, string> = {
    pending: 'bg-muted text-muted-foreground border-border',
    running: 'bg-status-info/10 text-status-info border-status-info/30',
    ready: 'bg-status-success/10 text-status-success border-status-success/30',
    failed: 'bg-status-error/10 text-status-error border-status-error/30',
  };
  const key = status ?? 'pending';
  return (
    <span className={cn('text-xs px-2 py-0.5 rounded border font-medium capitalize', palette[key])}>
      {key}
    </span>
  );
}

function ComplianceForm() {
  const [open, setOpen] = useState(false);
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
      setOpen(false);
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
      <div className="rounded-xl border border-border bg-card p-6 flex items-start justify-between gap-4">
        <div>
          <h2 className="text-base font-semibold text-foreground">Audit export</h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Bundles RBAC config, audit log, platform settings, and webhook history for a date
            window into a signed ZIP suitable for compliance archives.
          </p>
        </div>
        <ActionButton
          intent="primary"
          icon={<Plus className="h-3.5 w-3.5" />}
          onClick={() => setOpen(true)}
        >
          New export
        </ActionButton>
      </div>

      {open && (
        <ModalShell
          title="New compliance export"
          subtitle="Pick the date window to bundle."
          titleIcon={<FileArchive className="h-4 w-4" />}
          onClose={() => setOpen(false)}
          footer={
            <div className="flex justify-end gap-2">
              <ActionButton onClick={() => setOpen(false)}>Cancel</ActionButton>
              <ActionButton
                intent="primary"
                onClick={handleExport}
                loading={submitting}
                icon={<Download className="h-3.5 w-3.5" />}
              >
                Export
              </ActionButton>
            </div>
          }
        >
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">From</label>
              <Input
                type="date"
                value={from}
                onChange={(e) => setFrom(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">To</label>
              <Input
                type="date"
                value={to}
                onChange={(e) => setTo(e.target.value)}
              />
            </div>
          </div>
        </ModalShell>
      )}

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
              <ActionButton
                intent="primary"
                icon={<Download className="h-3.5 w-3.5" />}
                onClick={handleDownloadReady}
              >
                Download ZIP
              </ActionButton>
            )}
          </div>
          {job.progress != null && job.status !== 'ready' && (
            <div className="space-y-1">
              <div className="h-1.5 rounded-full bg-muted overflow-hidden">
                <div
                  className="h-full bg-status-info transition-all"
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
      <PageShell>
        <Link
          href="/dashboard/settings"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Settings
        </Link>
        <PageHeader
          eyebrow="Settings · Compliance"
          title={
            <span className="flex items-center gap-2">
              <FileArchive className="h-5 w-5 text-muted-foreground" />
              Compliance exports
            </span>
          }
          description="Build a ZIP of audit + RBAC + config for a date range. Large windows may take longer, but the export downloads directly when complete."
        />
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
      </PageShell>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute('/dashboard/settings/compliance/')({
  component: CompliancePage,
});
