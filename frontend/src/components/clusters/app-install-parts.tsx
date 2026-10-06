import { Loader2, Info } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import { BARE_BUTTON } from "@/lib/bare-button";

// Charts that ship CRDs by default — the operator should know that
// uninstall will not remove the CRDs unless they take extra steps.
// Surfaced on install too so the operator picks a stable namespace
// from the start.
export const HAS_CRDS = new Set([
  "kube-prometheus-stack",
  "cert-manager",
  "trivy-operator",
  "istio-base",
  "gatekeeper",
  "opa-gatekeeper",
]);

export function AppInstallFooter({
  onClose,
  pending,
  onSubmit,
  submittable,
  reason,
  upgrade,
}: {
  onClose: () => void;
  pending: boolean;
  onSubmit: () => void;
  submittable: boolean;
  reason?: string;
  upgrade: boolean;
}) {
  return (
    <div className="flex items-center justify-end gap-2">
      <ActionButton
        {...BARE_BUTTON}
        onClick={onClose}
        className="px-3 py-1.5 text-sm rounded-md border border-border bg-background hover:bg-muted inline-block font-normal"
        disabled={pending}
      >
        Cancel
      </ActionButton>
      <ActionButton
        {...BARE_BUTTON}
        disabledReason={reason}
        onClick={onSubmit}
        disabled={!submittable}
        className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-md bg-primary text-primary-foreground hover:opacity-90 disabled:opacity-50 font-normal"
      >
        {pending ? (
          <>
            <Loader2 className="h-3.5 w-3.5 animate-spin" />{" "}
            {upgrade ? "Upgrading" : "Installing"}…
          </>
        ) : (
          <>{upgrade ? "Upgrade" : "Install"}</>
        )}
      </ActionButton>
    </div>
  );
}

export function ChartInstallationNotes({
  slowInstall,
  hasCRDs,
  isUpgrade,
  chartName,
}: {
  slowInstall: boolean;
  hasCRDs: boolean;
  isUpgrade: boolean;
  chartName: string;
}) {
  return (
    <>
      {" "}
      {(slowInstall || hasCRDs) && (
        <div className="rounded-md border border-status-warning/30 bg-status-warning/5 px-3 py-2 text-xs flex items-start gap-2">
          <Info className="h-4 w-4 text-status-warning mt-0.5 shrink-0" />
          <div className="space-y-0.5 text-foreground">
            {slowInstall && (
              <div>
                First install of{" "}
                <span className="font-medium">{chartName}</span> typically takes
                3–10 minutes — sub-charts and CRDs land before the workloads
                come up.
              </div>
            )}
            {hasCRDs && !isUpgrade && (
              <div>
                This chart ships CRDs. The CRDs will <em>not</em> be removed
                automatically on uninstall (helm leaves them to protect data) —
                pick a stable namespace from the start.
              </div>
            )}
          </div>
        </div>
      )}
    </>
  );
}
