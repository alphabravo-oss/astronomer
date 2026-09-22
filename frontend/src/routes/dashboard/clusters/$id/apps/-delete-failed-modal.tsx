import { Trash2 } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import { ModalShell } from "@/components/ui/modal-shell";
import { permissionDeniedReason, toastPermissionDenied } from "@/lib/permission-hooks";
import type { PermissionDecision } from "@/lib/permissions";

// Confirmation dialog for the bulk "delete failed installs" action. Plain
// modal (not the AppUninstallModal) because there's no per-row context to
// surface; we're nuking every failed_* row on this cluster.
export function DeleteFailedModal({
  count,
  pending,
  onClose,
  onConfirm,
  confirmDecision,
}: {
  count: number;
  pending: boolean;
  onClose: () => void;
  onConfirm: () => void;
  confirmDecision: PermissionDecision;
}) {
  const blockedReason = !confirmDecision.allowed
    ? permissionDeniedReason(confirmDecision)
    : undefined;

  return (
    <ModalShell
      title="Delete failed installs"
      onClose={onClose}
      size="sm"
      titleIcon={<Trash2 className="h-4 w-4 text-status-error" />}
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton type="button" onClick={onClose} disabled={pending}>
            Cancel
          </ActionButton>
          <ActionButton
            type="button"
            intent="destructive"
            onClick={() => {
              if (!confirmDecision.allowed) {
                toastPermissionDenied(confirmDecision);
                return;
              }
              onConfirm();
            }}
            disabled={pending || !confirmDecision.allowed || count === 0}
            disabledReason={blockedReason}
            loading={pending}
            icon={<Trash2 className="h-3.5 w-3.5" />}
          >
            Delete {count} row{count === 1 ? "" : "s"}
          </ActionButton>
        </>
      }
    >
      <p className="text-sm text-muted-foreground">
        Hard-delete {count} <code className="font-mono">installed_charts</code>{" "}
        row{count === 1 ? "" : "s"} in{" "}
        <code className="font-mono">failed_install</code> /{" "}
        <code className="font-mono">failed_uninstall</code> on this cluster.
      </p>
      <p className="text-xs text-muted-foreground">
        No helm release uninstall is attempted — by definition these rows never
        deployed (or already failed to uninstall). If you suspect a stale
        release exists in-cluster, run{" "}
        <code className="font-mono">helm uninstall</code> via the kubectl shell
        first.
      </p>
    </ModalShell>
  );
}
