import { render, screen } from "@testing-library/react";
import type { ComponentProps } from "react";
import { AppsModals } from "./-apps-modals";

vi.mock("@/components/clusters/app-install-modal", () => ({
  AppInstallModal: ({ projectId }: { projectId: string }) => (
    <div role="dialog">Install in {projectId}</div>
  ),
  AppUninstallModal: () => null,
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
  modal: { kind: "install", chartId: "chart", chartName: "Chart" },
  projectId: "project-225",
  clusterId: "cluster-1",
  onCloseModal: vi.fn(),
  catalogCreateDecision: allowed,
  catalogUpdateDecision: allowed,
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
it.each(["install", "upgrade"] as const)(
  "hides an open %s dialog when the project scope becomes unavailable",
  (kind) => {
    const modal: typeof props.modal =
      kind === "install"
        ? props.modal
        : {
            kind,
            chartId: "chart",
            chartName: "Chart",
            installedChartId: "release",
            currentVersionId: "v1",
            currentValues: "",
            releaseName: "app",
            namespace: "default",
          };
    const view = render(<AppsModals {...props} modal={modal} />);
    expect(screen.getByRole("dialog")).toHaveTextContent("project-225");
    view.rerender(<AppsModals {...props} modal={modal} projectId="" />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  },
);
