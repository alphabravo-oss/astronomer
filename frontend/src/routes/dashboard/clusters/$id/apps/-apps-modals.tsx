import { AddRepositoryModal } from "../../../catalog/-add-repository-modal";
import { AppUninstallModal } from "@/components/clusters/app-uninstall-modal";
import type { PermissionDecision } from "@/lib/permissions";
import { DeleteFailedModal } from "./-delete-failed-modal";
import type { ModalState } from "./-modal-state";

/**
 * The apps page's modal layer: uninstall, add-repository,
 * and bulk delete-failed. Extracted so the page body stays focused on
 * data-fetching and tab dispatch.
 */
export function AppsModals({
  modal,
  onCloseModal,
  clusterId,
  catalogDeleteDecision,
  uninstallPending,
  onConfirmUninstall,
  showRepoModal,
  onCloseRepoModal,
  showDeleteFailed,
  onCloseDeleteFailed,
  deleteFailedCount,
  deleteFailedPending,
  onConfirmDeleteFailed,
}: {
  modal: ModalState;
  onCloseModal: () => void;
  clusterId: string;
  catalogDeleteDecision: PermissionDecision;
  uninstallPending: boolean;
  onConfirmUninstall: (installedChartId: string) => void;
  showRepoModal: boolean;
  onCloseRepoModal: () => void;
  showDeleteFailed: boolean;
  onCloseDeleteFailed: () => void;
  deleteFailedCount: number | undefined;
  deleteFailedPending: boolean;
  onConfirmDeleteFailed: () => void;
}) {
  return (
    <>
      {modal.kind === "uninstall" && (
        <AppUninstallModal
          clusterId={clusterId}
          installedChartId={modal.installedChartId}
          releaseName={modal.releaseName}
          chartName={modal.chartName}
          namespace={modal.namespace}
          pending={uninstallPending}
          confirmDecision={catalogDeleteDecision}
          onClose={onCloseModal}
          onConfirm={() => onConfirmUninstall(modal.installedChartId)}
        />
      )}
      {showRepoModal && <AddRepositoryModal onClose={onCloseRepoModal} />}
      {showDeleteFailed && (
        <DeleteFailedModal
          count={deleteFailedCount}
          pending={deleteFailedPending}
          confirmDecision={catalogDeleteDecision}
          onClose={onCloseDeleteFailed}
          onConfirm={onConfirmDeleteFailed}
        />
      )}
    </>
  );
}
