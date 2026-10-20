import { render, screen } from "@testing-library/react";
import type { ComponentProps } from "react";
import { AppsModals } from "./-apps-modals";

vi.mock("@/components/clusters/app-uninstall-modal", () => ({
  AppUninstallModal: () => <div role="dialog">Confirm uninstall</div>,
}));
vi.mock("../../../catalog/-add-repository-modal", () => ({
  AddRepositoryModal: () => null,
}));
vi.mock("./-delete-failed-modal", () => ({ DeleteFailedModal: () => null }));

const allowed = {
  allowed: true,
  permission: "catalog:manage",
  scope: { type: "global" as const },
  scopeLabel: "global",
  reason: "",
  grantedBy: [],
  requestAccessHint: "",
};
const props: ComponentProps<typeof AppsModals> = {
  modal: { kind: "none" },
  clusterId: "cluster-1",
  onCloseModal: vi.fn(),
  catalogDeleteDecision: allowed,
  uninstallPending: false,
  onConfirmUninstall: vi.fn(),
  showRepoModal: false,
  onCloseRepoModal: vi.fn(),
  showDeleteFailed: false,
  onCloseDeleteFailed: vi.fn(),
  deleteFailedCount: 0,
  deleteFailedPending: false,
  onConfirmDeleteFailed: vi.fn(),
};
it("uses the modal layer only for confirmation actions", () => {
  const view = render(<AppsModals {...props} />);
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  view.rerender(
    <AppsModals
      {...props}
      modal={{
        kind: "uninstall",
        installedChartId: "release",
        releaseName: "loki",
        chartName: "loki",
        namespace: "logs",
      }}
    />,
  );
  expect(screen.getByRole("dialog")).toHaveTextContent("Confirm uninstall");
});
