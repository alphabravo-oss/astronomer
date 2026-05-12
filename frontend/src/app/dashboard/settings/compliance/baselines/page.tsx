'use client';

/**
 * /dashboard/settings/compliance/baselines — sprint 17, migration 064.
 *
 * Four preset compliance profiles (PCI-DSS 4.0 / HIPAA / FedRAMP Moderate
 * / SOC 2). Each card renders the controls the baseline encodes and a
 * "View diff" drawer + Apply / Revert action. Active card is badged.
 */
import { useEffect, useState } from 'react';
import Link from 'next/link';
import { ArrowLeft, CheckCircle2, History, Loader2, Shield, Undo2 } from 'lucide-react';
import { toast } from 'sonner';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import {
  applyComplianceBaseline,
  getActiveComplianceBaseline,
  getComplianceBaselineDiff,
  listComplianceBaselineApplications,
  listComplianceBaselines,
  revertComplianceBaselineApplication,
  type ComplianceBaseline,
  type ComplianceBaselineApplication,
  type ComplianceBaselineDiff,
} from '@/lib/api/settings';

function ActiveBadge() {
  return (
    <span className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-full border bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border-emerald-500/30 font-medium">
      <CheckCircle2 className="w-3 h-3" /> Active
    </span>
  );
}

function BaselineCard({
  b,
  onViewDiff,
  onApply,
  onRevert,
  latestApplicationId,
}: {
  b: ComplianceBaseline;
  onViewDiff: (b: ComplianceBaseline) => void;
  onApply: (b: ComplianceBaseline) => void;
  onRevert: (id: string) => void;
  latestApplicationId: string | null;
}) {
  return (
    <div className="rounded-lg border bg-card p-5 flex flex-col gap-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <Shield className="w-4 h-4 text-muted-foreground" />
            <h3 className="font-semibold">{b.name}</h3>
            {b.active ? <ActiveBadge /> : null}
          </div>
          <p className="text-sm text-muted-foreground mt-1">{b.description}</p>
        </div>
        <span className="text-xs text-muted-foreground">v{b.version}</span>
      </div>

      <dl className="grid grid-cols-2 gap-2 text-sm">
        <div>
          <dt className="text-xs text-muted-foreground">Audit retention</dt>
          <dd>{b.spec.audit_retention_days} days</dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">Pod Security Standard</dt>
          <dd>{b.spec.pss_profile ?? '—'}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">TOTP required</dt>
          <dd>{b.spec.totp_required ? 'Yes' : 'No'}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">SMTP required</dt>
          <dd>{b.spec.required_smtp ? 'Yes' : 'No'}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">Quota plans</dt>
          <dd>{b.spec.quota_plans?.length ?? 0}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">Alert rules</dt>
          <dd>{b.spec.alert_rules?.length ?? 0}</dd>
        </div>
      </dl>

      <div className="flex gap-2 mt-auto pt-2">
        <button
          type="button"
          onClick={() => onViewDiff(b)}
          className="text-sm px-3 py-1.5 rounded border bg-background hover:bg-muted"
        >
          View diff
        </button>
        <button
          type="button"
          onClick={() => onApply(b)}
          className="text-sm px-3 py-1.5 rounded bg-primary text-primary-foreground hover:opacity-90"
        >
          Apply baseline
        </button>
        {b.active && latestApplicationId ? (
          <button
            type="button"
            onClick={() => onRevert(latestApplicationId)}
            className="text-sm px-3 py-1.5 rounded border bg-background hover:bg-muted flex items-center gap-1"
          >
            <Undo2 className="w-3.5 h-3.5" /> Revert
          </button>
        ) : null}
      </div>
    </div>
  );
}

function DiffDrawer({
  diff,
  onClose,
}: {
  diff: ComplianceBaselineDiff | null;
  onClose: () => void;
}) {
  if (!diff) return null;
  return (
    <div className="fixed inset-0 z-30 bg-black/40 flex items-end sm:items-center sm:justify-end">
      <div className="bg-card border-l w-full sm:max-w-lg h-full overflow-auto p-5">
        <div className="flex items-center justify-between mb-3">
          <h2 className="font-semibold">{diff.baseline_name} — change preview</h2>
          <button onClick={onClose} className="text-sm">
            Close
          </button>
        </div>
        {diff.changes.length === 0 ? (
          <p className="text-sm text-muted-foreground">No changes — baseline already matches current state.</p>
        ) : (
          <table className="text-sm w-full">
            <thead className="text-xs text-muted-foreground">
              <tr>
                <th className="text-left py-1">Field</th>
                <th className="text-left py-1">Current</th>
                <th className="text-left py-1">Target</th>
              </tr>
            </thead>
            <tbody>
              {diff.changes.map((key) => (
                <tr key={key} className="border-t">
                  <td className="py-1 pr-2 font-mono text-xs">{key}</td>
                  <td className="py-1 pr-2 font-mono text-xs text-muted-foreground break-all">
                    {JSON.stringify(diff.current[key] ?? null)}
                  </td>
                  <td className="py-1 font-mono text-xs break-all">
                    {JSON.stringify(diff.target[key] ?? null)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

export default function ComplianceBaselinesPage() {
  const [baselines, setBaselines] = useState<ComplianceBaseline[]>([]);
  const [history, setHistory] = useState<ComplianceBaselineApplication[]>([]);
  const [diff, setDiff] = useState<ComplianceBaselineDiff | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);

  const latestApplicationId = history[0]?.id ?? null;

  const reload = async () => {
    setLoading(true);
    try {
      const [bs, hist] = await Promise.all([
        listComplianceBaselines(),
        listComplianceBaselineApplications().catch(() => [] as ComplianceBaselineApplication[]),
      ]);
      // Active = highest-priority match from /active.
      try {
        const active = await getActiveComplianceBaseline();
        const slug = active.active?.baseline_slug;
        if (slug) {
          bs.forEach((b) => {
            b.active = b.slug === slug;
          });
        }
      } catch {
        /* ignore */
      }
      setBaselines(bs);
      setHistory(hist);
    } catch (err) {
      toast.error('Failed to load compliance baselines');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    reload();
  }, []);

  const handleViewDiff = async (b: ComplianceBaseline) => {
    try {
      const d = await getComplianceBaselineDiff(b.id);
      setDiff(d);
    } catch {
      toast.error(`Failed to compute diff for ${b.name}`);
    }
  };

  const handleApply = async (b: ComplianceBaseline) => {
    if (!confirm(`Apply ${b.name}? Current state will be snapshotted for revert.`)) return;
    setBusy(true);
    try {
      await applyComplianceBaseline(b.id);
      toast.success(`Applied ${b.name}`);
      await reload();
    } catch (err) {
      const status = (err as { response?: { status?: number; data?: { error?: string } } })?.response?.status;
      const msg = (err as { response?: { data?: { error?: string } } })?.response?.data?.error;
      if (status === 409) {
        toast.error(msg ?? 'Apply refused — guardrail triggered (audit retention downgrade?)');
      } else {
        toast.error('Apply failed');
      }
    } finally {
      setBusy(false);
    }
  };

  const handleRevert = async (id: string) => {
    if (!confirm('Revert the most-recent baseline application?')) return;
    setBusy(true);
    try {
      await revertComplianceBaselineApplication(id);
      toast.success('Reverted');
      await reload();
    } catch (err) {
      const status = (err as { response?: { status?: number } })?.response?.status;
      if (status === 409) {
        toast.error('Cannot revert — a newer application exists. Revert the latest first.');
      } else {
        toast.error('Revert failed');
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <SettingsAuthGate>
      <div className="max-w-5xl mx-auto p-6 space-y-6">
        <div className="flex items-center gap-2">
          <Link href="/dashboard/settings/compliance" className="text-sm text-muted-foreground inline-flex items-center gap-1">
            <ArrowLeft className="w-4 h-4" /> Compliance
          </Link>
        </div>
        <div>
          <h1 className="text-2xl font-semibold">Compliance baselines</h1>
          <p className="text-sm text-muted-foreground mt-1">
            One-click preset profiles for PCI-DSS, HIPAA, FedRAMP-Moderate, and SOC 2.
            Each baseline snapshots prior state on apply so a revert restores it.
          </p>
        </div>

        {loading ? (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="w-4 h-4 animate-spin" /> Loading
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {baselines.map((b) => (
              <BaselineCard
                key={b.id}
                b={b}
                onViewDiff={handleViewDiff}
                onApply={handleApply}
                onRevert={handleRevert}
                latestApplicationId={latestApplicationId}
              />
            ))}
          </div>
        )}

        <section>
          <h2 className="text-lg font-semibold flex items-center gap-2 mt-6">
            <History className="w-4 h-4" /> Application history
          </h2>
          {history.length === 0 ? (
            <p className="text-sm text-muted-foreground mt-2">No baseline has been applied yet.</p>
          ) : (
            <ul className="mt-2 text-sm divide-y border rounded">
              {history.map((h) => (
                <li key={h.id} className="px-3 py-2 flex items-center justify-between gap-3">
                  <span className="font-medium">{h.baseline_name}</span>
                  <span className="text-muted-foreground">{h.status}</span>
                  <span className="text-xs text-muted-foreground">{h.applied_at}</span>
                </li>
              ))}
            </ul>
          )}
        </section>

        <DiffDrawer diff={diff} onClose={() => setDiff(null)} />
        {busy ? (
          <div className="fixed inset-0 z-40 bg-black/30 flex items-center justify-center">
            <Loader2 className="w-6 h-6 animate-spin text-white" />
          </div>
        ) : null}
      </div>
    </SettingsAuthGate>
  );
}
