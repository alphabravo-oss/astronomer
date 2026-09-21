import { createFileRoute } from "@tanstack/react-router";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/operator-table";
import { DrawerShell } from "@/components/ui/drawer-shell";
import { OverlayShell } from "@/components/ui/overlay-shell";
/**
 * /dashboard/settings/compliance/baselines — sprint 17, migration 064.
 *
 * Four preset compliance profiles (PCI-DSS 4.0 / HIPAA / FedRAMP Moderate
 * / SOC 2). Each card renders the controls the baseline encodes and a
 * "View diff" drawer + Apply / Revert action. Active card is badged.
 */
import { useState } from "react";
import { useComplianceBaselines, useComplianceBaselineDiff } from "@/lib/hooks/policy-queries";
import { QueryStates } from "@/components/ui/query-states";
import { ResourceMasthead } from "@/components/ui/page";
import {
  CheckCircle2,
  History,
  Loader2,
  Shield,
  Undo2,
} from "lucide-react";
import { toastError, toastSuccess } from "@/lib/toast";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  applyComplianceBaseline,
  revertComplianceBaselineApplication,
  type ComplianceBaselineView,
} from "@/lib/api/settings";

function ActiveBadge() {
  return (
    <span className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-full border bg-status-success/10 text-status-success border-status-success/30 font-medium">
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
  b: ComplianceBaselineView;
  onViewDiff: (b: ComplianceBaselineView) => void;
  onApply: (b: ComplianceBaselineView) => void;
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
          <dt className="text-xs text-muted-foreground">
            Pod Security Standard
          </dt>
          <dd>{b.spec.pss_profile ?? "—"}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">TOTP required</dt>
          <dd>{b.spec.required_totp ? "Yes" : "No"}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">SMTP required</dt>
          <dd>{b.spec.required_smtp ? "Yes" : "No"}</dd>
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
          className="text-sm px-3 py-1.5 rounded-sm border bg-background hover:bg-muted"
        >
          View diff
        </button>
        <button
          type="button"
          onClick={() => onApply(b)}
          className="text-sm px-3 py-1.5 rounded-sm bg-primary text-primary-foreground hover:opacity-90"
        >
          Apply baseline
        </button>
        {b.active && latestApplicationId ? (
          <button
            type="button"
            onClick={() => onRevert(latestApplicationId)}
            className="text-sm px-3 py-1.5 rounded-sm border bg-background hover:bg-muted flex items-center gap-1"
          >
            <Undo2 className="w-3.5 h-3.5" /> Revert
          </button>
        ) : null}
      </div>
    </div>
  );
}

function DiffDrawer({
  baseline,
  onClose,
}: {
  baseline: ComplianceBaselineView;
  onClose: () => void;
}) {
  const query = useComplianceBaselineDiff(baseline.id);
  return (
    <DrawerShell
      title={`${baseline.name} - change preview`}
      onClose={onClose}
      panelClassName="sm:max-w-lg bg-card"
    >
      <QueryStates query={query}>{(diff) => diff.changes.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No changes — baseline already matches current state.
        </p>
      ) : (
        <Table className="text-sm w-full">
          <TableHeader className="text-xs text-muted-foreground">
            <TableRow>
              <TableHead className="text-left py-1">Field</TableHead>
              <TableHead className="text-left py-1">Current</TableHead>
              <TableHead className="text-left py-1">Target</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {diff.changes.map((key) => (
              <TableRow key={key} className="border-t">
                <TableCell className="py-1 pr-2 font-mono text-xs">
                  {key}
                </TableCell>
                <TableCell className="py-1 pr-2 font-mono text-xs text-muted-foreground break-all">
                  {JSON.stringify(diff.current[key] ?? null)}
                </TableCell>
                <TableCell className="py-1 font-mono text-xs break-all">
                  {JSON.stringify(diff.target[key] ?? null)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}</QueryStates>
    </DrawerShell>
  );
}

function ComplianceBaselinesPage() {
  const baselinesQuery = useComplianceBaselines();
  const baselines = baselinesQuery.data?.baselines ?? [];
  const history = baselinesQuery.data?.history ?? [];
  const [diffBaseline, setDiffBaseline] = useState<ComplianceBaselineView | null>(null);
  const loading = baselinesQuery.isLoading;
  const [busy, setBusy] = useState(false);
  const [confirmation, setConfirmation] = useState<
    | { kind: "apply"; baseline: ComplianceBaselineView }
    | { kind: "revert"; applicationId: string; baselineName: string }
    | null
  >(null);

  const latestApplicationId = history[0]?.id ?? null;

  const reload = async () => { await baselinesQuery.refetch(); };

  const handleApply = async (b: ComplianceBaselineView) => {
    setBusy(true);
    try {
      await applyComplianceBaseline(b.id);
      toastSuccess(`Applied ${b.name}`);
      setConfirmation(null);
      await reload();
    } catch (err) {
      const status = (
        err as { response?: { status?: number; data?: { error?: string } } }
      )?.response?.status;
      const msg = (err as { response?: { data?: { error?: string } } })
        ?.response?.data?.error;
      if (status === 409) {
        toastError(
          msg ??
            "Apply refused — guardrail triggered (audit retention downgrade?)",
        );
      } else {
        toastError("Apply failed");
      }
    } finally {
      setBusy(false);
    }
  };

  const handleRevert = async (id: string) => {
    setBusy(true);
    try {
      await revertComplianceBaselineApplication(id);
      toastSuccess("Reverted");
      setConfirmation(null);
      await reload();
    } catch (err) {
      const status = (err as { response?: { status?: number } })?.response
        ?.status;
      if (status === 409) {
        toastError(
          "Cannot revert — a newer application exists. Revert the latest first.",
        );
      } else {
        toastError("Revert failed");
      }
    } finally {
      setBusy(false);
    }
  };

  if (baselinesQuery.isError) return <QueryStates query={baselinesQuery}>{null}</QueryStates>;

  return (
    <SettingsAuthGate>
      <div className="space-y-6">
        <ResourceMasthead
          backTo="/dashboard/settings/compliance"
          backLabel="Compliance"
          title="Compliance baselines"
          description="One-click preset profiles for PCI-DSS, HIPAA, FedRAMP-Moderate, and SOC 2. Each baseline snapshots prior state on apply so a revert restores it. Applying a preset is not a certification or FIPS claim."
        />

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
                onViewDiff={setDiffBaseline}
                onApply={(baseline) =>
                  setConfirmation({ kind: "apply", baseline })
                }
                onRevert={(applicationId) =>
                  setConfirmation({
                    kind: "revert",
                    applicationId,
                    baselineName: history[0]?.baselineName ?? "latest baseline",
                  })
                }
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
            <p className="text-sm text-muted-foreground mt-2">
              No baseline has been applied yet.
            </p>
          ) : (
            <ul className="mt-2 text-sm divide-y border rounded-sm">
              {history.map((h) => (
                <li
                  key={h.id}
                  className="px-3 py-2 flex items-center justify-between gap-3"
                >
                  <span className="font-medium">{h.baselineName}</span>
                  <span className="text-muted-foreground">{h.status}</span>
                  <span className="text-xs text-muted-foreground">
                    {h.appliedAt}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </section>

        {diffBaseline ? <DiffDrawer baseline={diffBaseline} onClose={() => setDiffBaseline(null)} /> : null}
        <ConfirmDialog
          open={confirmation !== null}
          onClose={() => setConfirmation(null)}
          onConfirm={() => {
            if (confirmation?.kind === "apply") {
              void handleApply(confirmation.baseline);
            } else if (confirmation?.kind === "revert") {
              void handleRevert(confirmation.applicationId);
            }
          }}
          title={
            confirmation?.kind === "apply"
              ? "Apply compliance baseline"
              : "Revert baseline application"
          }
          description={
            confirmation?.kind === "apply"
              ? "The current platform settings will be snapshotted before the baseline is applied."
              : "This restores the snapshot captured before the latest baseline application."
          }
          confirmText={confirmation?.kind === "apply" ? "Apply" : "Revert"}
          loading={busy}
          impact={
            confirmation?.kind === "apply"
              ? {
                  scope: confirmation.baseline.name,
                  consequences: [
                    "Platform security and retention settings will change to match the baseline.",
                    "The previous settings will be retained as a reversible snapshot.",
                  ],
                  recovery: "Use Revert on the latest application.",
                }
              : confirmation?.kind === "revert"
                ? {
                    scope: confirmation.baselineName,
                    consequences: [
                      "The most recent baseline changes will be replaced by their saved prior values.",
                      "Any later application blocks this revert to prevent stale restoration.",
                    ],
                    recovery: "Apply the baseline again if needed.",
                  }
                : undefined
          }
        />
        {busy ? (
          <OverlayShell onClose={() => undefined} closeOnBackdrop={false}>
            <Loader2 className="w-6 h-6 animate-spin text-white" />
          </OverlayShell>
        ) : null}
      </div>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute(
  "/dashboard/settings/compliance/baselines/",
)({
  component: ComplianceBaselinesPage,
});
