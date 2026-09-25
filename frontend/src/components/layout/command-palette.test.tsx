import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { CommandPalette } from "@/components/layout/command-palette";
import { CommandPaletteDialog } from "@/components/layout/command-palette-dialog";
import { globalNavGroups } from "@/components/layout/sidebar-navigation";
import { useUIStore, useAuthStore } from "@/lib/store";

vi.stubGlobal(
  "ResizeObserver",
  class {
    observe() {
      return undefined;
    }
    unobserve() {
      return undefined;
    }
    disconnect() {
      return undefined;
    }
  },
);

// jsdom doesn't implement scrollIntoView; cmdk calls it when the selected
// item changes as the search filters the list.
Element.prototype.scrollIntoView = vi.fn();

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
  useLocation: ({ select }: { select: (value: unknown) => unknown }) =>
    select({ pathname: "/dashboard", searchStr: "" }),
}));

vi.mock("@/lib/hooks/clusters", () => ({
  useClusters: () => ({ data: { data: [] } }),
  useFeatureFlags: () => ({
    data: {
      "feature.monitoring": true,
      "feature.charlie": true,
      "feature.security": true,
      "feature.projects": true,
      "feature.catalog": true,
      "feature.extensions": true,
    },
  }),
  useCharlieActivated: () => ({ activated: true }),
}));

vi.mock("@/lib/hooks/projects", () => ({
  useProjects: () => ({ data: { data: [] } }),
}));

vi.mock("./use-sidebar-navigation", () => ({
  useSidebarNavigation: () => ({ navGroups: [] }),
}));

const superuser = {
  id: "u-1",
  username: "admin",
  displayName: "Admin",
  email: "admin@example.com",
  isSuperuser: true,
} as never;

beforeEach(() => {
  useUIStore.setState({ commandPaletteOpen: true });
  useAuthStore.setState({ user: superuser });
});

describe("CommandPalette", () => {
  it("surfaces every global nav destination by label search", () => {
    render(<CommandPaletteDialog />);
    const input = screen.getByPlaceholderText(
      "Search clusters, pages, actions...",
    );
    for (const group of globalNavGroups) {
      for (const item of group.items) {
        fireEvent.change(input, { target: { value: item.label } });
        expect(
          screen.getAllByText(item.label).length,
          `expected "${item.label}" to be reachable by search`,
        ).toBeGreaterThan(0);
      }
    }
  });

  it("opens with the keyboard and clears search after closing", async () => {
    useUIStore.setState({ commandPaletteOpen: false });
    render(<CommandPalette />);
    expect(
      screen.queryByPlaceholderText("Search clusters, pages, actions..."),
    ).toBeNull();
    fireEvent.keyDown(document, { key: "k", ctrlKey: true });
    const input = await screen.findByPlaceholderText(
      "Search clusters, pages, actions...",
    );
    fireEvent.change(input, { target: { value: "clusters" } });
    fireEvent.keyDown(document, { key: "Escape" });
    expect(
      screen.queryByPlaceholderText("Search clusters, pages, actions..."),
    ).toBeNull();
    fireEvent.keyDown(document, { key: "k", metaKey: true });
    expect(
      await screen.findByPlaceholderText("Search clusters, pages, actions..."),
    ).toHaveValue("");
  });
});
