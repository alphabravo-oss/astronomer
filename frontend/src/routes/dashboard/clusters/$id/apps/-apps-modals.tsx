import { AddRepositoryModal } from "../../../catalog/-add-repository-modal";
import {
  AppInstallModal,
  AppUninstallModal,
} from "@/components/clusters/app-install-modal";
import type { PermissionDecision } from "@/lib/permissions";
import { DeleteFailedModal } from "./-delete-failed-modal";
import type { ModalState } from "./-modal-state";

/**
 * The apps page's modal layer: install/upgrade, uninstall, add-repository,
 * and bulk delete-failed. Extracted so the page body stays focused on
 * data-fetching and tab dispatch.
 */
export function AppsModals({
  modal,
  onCloseModal,
  projectId,
  clusterId,
  catalogCreateDecision,
  catalogUpdateDecision,
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
  projectId: string;
  clusterId: string;
  catalogCreateDecision: PermissionDecision;
  catalogUpdateDecision: PermissionDecision;
  catalogDeleteDecision: PermissionDecision;
  uninstallPending: boolean;
  onConfirmUninstall: (installedChartId: string) => void;
  showRepoModal: boolean;
  onCloseRepoModal: () => void;
  showDeleteFailed: boolean;
  onCloseDeleteFailed: () => void;
  deleteFailedCount: number;
  deleteFailedPending: boolean;
  onConfirmDeleteFailed: () => void;
}) {
  return (
    <>
      {modal.kind === "install" && (
        <AppInstallModal
          projectId={projectId}
          clusterId={clusterId}
          mode={{
            kind: "install",
            chartId: modal.chartId,
            chartName: modal.chartName,
          }}
          submitDecision={catalogCreateDecision}
          onClose={onCloseModal}
        />
      )}
      {modal.kind === "upgrade" && (
        <AppInstallModal
          projectId={projectId}
          clusterId={clusterId}
          mode={{
            kind: "upgrade",
            installedChartId: modal.installedChartId,
            chartId: modal.chartId,
            chartName: modal.chartName,
            currentVersionId: modal.currentVersionId,
            currentValues: modal.currentValues,
            releaseName: modal.releaseName,
            namespace: modal.namespace,
          }}
          submitDecision={catalogUpdateDecision}
          onClose={onCloseModal}
        />
      )}
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
