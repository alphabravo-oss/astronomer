import { describe, expect, it } from "vitest";

import { dashboardContentLayout } from "./dashboard-content-layout";

describe("dashboardContentLayout", () => {
  it.each([
    ["/dashboard/clusters", ""],
    ["/dashboard/clusters/c1/pods", ""],
    ["/dashboard/clusters/c1/logging", ""],
    ["/dashboard/clusters/c1/deployments/ns/name", "tab=yaml"],
    ["/dashboard/clusters/c1/pods/ns/name", "view=logs"],
  ])("uses full width for dense operator surface %s?%s", (path, search) => {
    expect(dashboardContentLayout(path, search)).toBe("full-bleed");
  });

  it("keeps form and overview pages in the reading-width container", () => {
    expect(dashboardContentLayout("/dashboard/settings/platform")).toBe(
      "contained",
    );
    expect(dashboardContentLayout("/dashboard/clusters/c1")).toBe("contained");
  });
});
