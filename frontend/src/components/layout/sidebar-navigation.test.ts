import { describe, expect, it } from "vitest";
import { Box } from "lucide-react";

import {
  activeNavGroupLabel,
  defaultOpenNavGroupLabel,
  toggleOpenNavGroupLabel,
  type NavGroup,
} from "@/components/layout/sidebar-navigation";

const groups: NavGroup[] = [
  {
    label: "Default",
    defaultOpen: true,
    items: [{ label: "Home", href: "/dashboard", icon: Box, exact: true }],
  },
  {
    label: "Workloads",
    items: [{ label: "Pods", href: "/dashboard/pods", icon: Box }],
  },
];

describe("sidebar accordion group selection", () => {
  it("selects only the group containing the active route", () => {
    expect(activeNavGroupLabel(groups, "/dashboard/pods/ns/pod-a")).toBe(
      "Workloads",
    );
    expect(defaultOpenNavGroupLabel(groups, "/dashboard/pods/ns/pod-a")).toBe(
      "Workloads",
    );
  });

  it("uses the first default group when no route is active", () => {
    expect(defaultOpenNavGroupLabel(groups, "/outside-dashboard")).toBe(
      "Default",
    );
  });

  it("returns no group when neither an active nor default group exists", () => {
    const groupsWithoutDefault = groups.map((group) => ({
      ...group,
      defaultOpen: false,
    }));

    expect(
      defaultOpenNavGroupLabel(groupsWithoutDefault, "/outside-dashboard"),
    ).toBeNull();
  });

  it("replaces the open group and closes a group toggled twice", () => {
    expect(toggleOpenNavGroupLabel("Default", "Workloads")).toBe("Workloads");
    expect(toggleOpenNavGroupLabel("Workloads", "Workloads")).toBeNull();
  });
});
