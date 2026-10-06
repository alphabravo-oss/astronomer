import { useState } from "react";
import { toastWarning } from "@/lib/toast";
import { Loader2, AlertTriangle } from "lucide-react";
import { ModalShell } from "@/components/ui/modal-shell";
import { permissionDeniedReason } from "@/lib/permission-hooks";
import type { PermissionDecision } from "@/lib/permissions";
import { HAS_CRDS } from "./app-install-parts";
import { BareButton } from "@/components/form/bare-button";

// ---------------------------------------------------------------------
// Uninstall confirmation
// ---------------------------------------------------------------------

export interface AppUninstallModalProps {
  clusterId: string;
  installedChartId: string;
  releaseName: string;
  chartName: string;
  namespace: string;
  onClose: () => void;
  onConfirm: () => Promise<void> | void;
  pending?: boolean;
  confirmDecision?: PermissionDecision;
}

export function AppUninstallModal({
  releaseName,
  chartName,
  namespace,
  onClose,
  onConfirm,
  pending,
  confirmDecision,
}: AppUninstallModalProps) {
  const [typed, setTyped] = useState("");
  const confirmBlockedReason =
    confirmDecision && !confirmDecision.allowed
      ? permissionDeniedReason(confirmDecision)
      : undefined;
  const confirmable =
    typed === releaseName && !pending && !confirmBlockedReason;
  const crdsWillSurvive = HAS_CRDS.has(chartName);
  const handleConfirm = () => {
    if (confirmBlockedReason) {
      toastWarning(confirmBlockedReason);
      return;
    }
    onConfirm();
  };

  return (
    <ModalShell
      title="Uninstall release"
      onClose={onClose}
      size="sm"
      panelClassName="bg-popover"
      bodyClassName="p-5 space-y-3 text-sm"
      footerClassName="bg-muted/30"
      titleIcon={<AlertTriangle className="h-5 w-5 text-status-error" />}
      footer={
        <div className="flex items-center justify-end gap-2">
          <BareButton
            onClick={onClose}
            className="px-3 py-1.5 text-sm rounded-md border border-border bg-background hover:bg-muted inline-block font-normal"
            disabled={pending}
          >
            Cancel
          </BareButton>
          <BareButton
            disabledReason={confirmBlockedReason}
            onClick={handleConfirm}
            disabled={!confirmable}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-md bg-status-error text-background hover:bg-status-error disabled:opacity-50 font-normal"
          >
            {pending ? (
              <>
                <Loader2 className="h-3.5 w-3.5 animate-spin" /> Uninstalling…
              </>
            ) : (
              <>Uninstall</>
            )}
          </BareButton>
        </div>
      }
    >
      <p>
        This will run{" "}
        <code className="font-mono text-xs">
          helm uninstall {releaseName} -n {namespace}
        </code>{" "}
        on the cluster. Workload pods + Services + ConfigMaps owned by the
        release will be deleted.
      </p>
      {crdsWillSurvive && (
        <div className="rounded-md border border-status-warning/40 bg-status-warning/5 px-3 py-2 text-xs">
          <div className="font-medium text-status-warning flex items-center gap-1.5">
            <AlertTriangle className="h-3.5 w-3.5" /> CRDs will not be removed
          </div>
          <p className="text-muted-foreground mt-1">
            <span className="font-mono">{chartName}</span> ships CRDs. Helm
            leaves them in place on uninstall to protect data; remove manually
            with <code className="font-mono">kubectl delete crd …</code> if you
            need a clean re-install.
          </p>
        </div>
      )}
      <div className="space-y-1.5">
        <label className="text-xs font-medium text-muted-foreground">
          Type{" "}
          <code className="font-mono text-xs bg-muted px-1 rounded-sm">
            {releaseName}
          </code>{" "}
          to confirm
        </label>
        <input
          type="text"
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          className="w-full h-(--control-h) px-3 rounded-md border border-border bg-background text-sm font-mono focus:outline-hidden focus:ring-1 focus:ring-ring"
          data-initial-focus
        />
      </div>
    </ModalShell>
  );
}
