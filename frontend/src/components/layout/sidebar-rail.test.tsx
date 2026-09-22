import { fireEvent, render, screen } from "@testing-library/react";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...(await importOriginal<typeof import("@tanstack/react-router")>()),
    Link: RouterLinkStub,
  };
});

import { getClusterNavGroups } from "@/components/layout/sidebar-navigation";
import { CollapsedNavItems, SidebarRailGroup } from "./sidebar-rail";

describe("collapsed cluster rail", () => {
  const groups = getClusterNavGroups("c-1", {
    isLocal: false,
    veleroInstalled: true,
    grafanaAvailable: true,
  });

  it("renders one control per group, not one per item", () => {
    render(
      <div>
        {groups.map((group) => (
          <SidebarRailGroup
            key={group.label}
            group={group}
            pathname="/dashboard/clusters/c-1"
          />
        ))}
      </div>,
    );

    const totalItems = groups.reduce((sum, g) => sum + g.items.length, 0);
    const controls = screen.getAllByRole("button");
    expect(controls).toHaveLength(groups.length);
    expect(controls.length).toBeLessThanOrEqual(10);
    expect(controls.length).toBeLessThan(totalItems);
  });

  it("shows a group's items only after activating its control", () => {
    const cluster = groups.find((g) => g.label === "Cluster")!;
    render(
      <SidebarRailGroup group={cluster} pathname="/dashboard/clusters/c-1" />,
    );

    expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Cluster" }));

    const menu = screen.getByRole("navigation");
    for (const item of cluster.items) {
      expect(
        screen.getByRole("link", { name: new RegExp(item.label) }),
      ).toBeInTheDocument();
    }
    expect(menu).toBeInTheDocument();
    expect(menu.parentElement).toBe(document.body);
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Cluster" })).toHaveFocus();
  });
});

describe("CollapsedNavItems (header-less groups)", () => {
  it("renders each item as its own icon link", () => {
    const groups = getClusterNavGroups("c-1");
    const cluster = groups.find((g) => g.label === "Cluster")!;
    render(
      <CollapsedNavItems
        items={cluster.items.slice(0, 2)}
        pathname="/dashboard/clusters/c-1"
      />,
    );
    expect(screen.getAllByRole("link")).toHaveLength(2);
  });
});
