import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { InstalledChart } from "@/types";
import { InstalledTab } from "./-installed-tab";

vi.mock("@/lib/hooks/clusters", () => ({
  useCluster: () => ({ data: { displayName: "prod", name: "prod" } }),
}));

const row: InstalledChart = {
  id: "installed-1",
  clusterId: "cluster-1",
  chartVersionId: "version-2",
  releaseName: "kube-prometheus-stack",
  namespace: "monitoring",
  status: "installed",
  revision: 2,
  createdAt: "2026-09-01T00:00:00Z",
  updatedAt: "2026-09-01T00:00:00Z",
};

describe("catalog InstalledTab actions", () => {
  it("offers Upgrade, Rollback, and Uninstall from the row action menu", () => {
    render(
      <InstalledTab
        installed={[row]}
        loading={false}
        onUpgrade={vi.fn()}
        onRollback={vi.fn()}
        onUninstall={vi.fn()}
      />,
    );

    fireEvent.click(
      screen.getByRole("button", {
        name: `Actions for ${row.releaseName}`,
      }),
    );

    expect(screen.getByRole("menuitem", { name: "Upgrade" })).toBeInTheDocument();
    expect(
      screen.getByRole("menuitem", { name: "Rollback" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("menuitem", { name: "Uninstall" }),
    ).toBeInTheDocument();
  });

  it("calls onUpgrade with the row when Upgrade is clicked", () => {
    const onUpgrade = vi.fn();
    render(
      <InstalledTab
        installed={[row]}
        loading={false}
        onUpgrade={onUpgrade}
        onRollback={vi.fn()}
        onUninstall={vi.fn()}
      />,
    );

    fireEvent.click(
      screen.getByRole("button", {
        name: `Actions for ${row.releaseName}`,
      }),
    );
    fireEvent.click(screen.getByRole("menuitem", { name: "Upgrade" }));

    expect(onUpgrade).toHaveBeenCalledWith(row);
  });

  it("disables Rollback at revision 1 with a reason", () => {
    render(
      <InstalledTab
        installed={[{ ...row, revision: 1 }]}
        loading={false}
        onUpgrade={vi.fn()}
        onRollback={vi.fn()}
        onUninstall={vi.fn()}
      />,
    );

    fireEvent.click(
      screen.getByRole("button", {
        name: `Actions for ${row.releaseName}`,
      }),
    );

    const rollbackItem = screen.getByRole("menuitem", { name: "Rollback" });
    expect(rollbackItem).toBeDisabled();
    expect(rollbackItem).toHaveAttribute("title", "No previous revision");
  });
});
