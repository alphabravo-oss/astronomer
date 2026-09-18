import { describe, expect, it } from "vitest";

import {
  SETTINGS_NAVIGATION,
  settingsItemIsActive,
  visibleSettingsNavigation,
} from "./settings-navigation";

describe("settings navigation", () => {
  it("defines each destination exactly once", () => {
    const destinations = SETTINGS_NAVIGATION.flatMap((group) =>
      group.items.map((item) => item.href),
    );
    expect(new Set(destinations).size).toBe(destinations.length);
  });

  it("shows only Charlie to a delegated Charlie administrator", () => {
    const groups = visibleSettingsNavigation(SETTINGS_NAVIGATION, {
      isSuperuser: false,
      canManageCharlie: true,
      extensionsEnabled: true,
    });
    expect(
      groups.flatMap((group) => group.items.map((item) => item.title)),
    ).toEqual(["Charlie"]);
  });

  it("hides disabled extensions and unavailable Charlie from superusers", () => {
    const groups = visibleSettingsNavigation(SETTINGS_NAVIGATION, {
      isSuperuser: true,
      canManageCharlie: false,
      extensionsEnabled: false,
    });
    const titles = groups.flatMap((group) =>
      group.items.map((item) => item.title),
    );
    expect(titles).not.toContain("Charlie");
    expect(titles).not.toContain("Extensions");
    expect(titles).toContain("GitOps sources");
  });

  it("matches nested detail routes without matching sibling prefixes", () => {
    expect(
      settingsItemIsActive(
        "/dashboard/settings/webhooks/new",
        "/dashboard/settings/webhooks",
      ),
    ).toBe(true);
    expect(
      settingsItemIsActive(
        "/dashboard/settings/platforms",
        "/dashboard/settings/platform",
      ),
    ).toBe(false);
  });
});
