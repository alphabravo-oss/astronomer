import { createFileRoute } from "@tanstack/react-router";
/**
 * /dashboard/settings/compliance — audit export bundles.
 *
 * The current backend streams the ZIP body inline (200). The polling code is
 * retained for future durable background export jobs, but production exports
 * download directly today.
 */
import { useState } from "react";
import { Link } from "@/lib/link";
import { ArrowLeft, Download, FileArchive, Plus } from "lucide-react";
import { toastError, toastSuccess } from "@/lib/toast";
import { downloadBlob } from "@/lib/utils";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { ModalShell } from "@/components/ui/modal-shell";
import { PageHeader, PageShell } from "@/components/ui/page";
import { requestComplianceExport } from "@/lib/api/settings";

function todayIso() {
  return new Date().toISOString().slice(0, 10);
}

function thirtyDaysAgoIso() {
  const d = new Date();
  d.setDate(d.getDate() - 30);
  return d.toISOString().slice(0, 10);
}

function ComplianceForm() {
  const [open, setOpen] = useState(false);
  const [from, setFrom] = useState(thirtyDaysAgoIso());
  const [to, setTo] = useState(todayIso());
  const [submitting, setSubmitting] = useState(false);

  const handleExport = async () => {
    if (!from || !to) {
      toastError("Both dates are required");
      return;
    }
    if (from > to) {
      toastError('"From" must be before "to"');
      return;
    }
    setSubmitting(true);
    try {
      const result = await requestComplianceExport({ from, to });
      setOpen(false);
      downloadBlob(result.blob, result.filename);
      toastSuccess("Export downloaded");
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Failed to start export";
      toastError(msg);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="space-y-6">
      <div className="rounded-xl border border-border bg-card p-6 flex items-start justify-between gap-4">
        <div>
          <h2 className="text-base font-semibold text-foreground">
            Audit export
          </h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Bundles RBAC config, audit log, platform settings, and webhook
            history for a date window into a signed ZIP suitable for compliance
            archives.
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
              <label
                className="text-sm font-medium text-foreground"
                htmlFor="field-d2e1f880-164"
              >
                From
              </label>
              <Input
                id="field-d2e1f880-164"
                type="date"
                value={from}
                onChange={(e) => setFrom(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <label
                className="text-sm font-medium text-foreground"
                htmlFor="field-d2e1f880-172"
              >
                To
              </label>
              <Input
                id="field-d2e1f880-172"
                type="date"
                value={to}
                onChange={(e) => setTo(e.target.value)}
              />
            </div>
          </div>
        </ModalShell>
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
            One-click preset profiles (PCI-DSS, HIPAA, FedRAMP, SOC 2) that
            snapshot and apply the related platform settings, quota plans, audit
            retention, and alert rules.
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

export const Route = createFileRoute("/dashboard/settings/compliance/")({
  component: CompliancePage,
});
