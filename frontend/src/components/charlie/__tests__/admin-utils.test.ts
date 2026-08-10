import { describe, expect, it } from "vitest";
import {
  adjacentTab,
  automationValidationIssues,
  canManageCharlie,
  completeDiagnostics,
  mergeCharlieSearch,
  normalizeCharlieAdminTab,
  parseOnboardingFile,
} from "../admin-utils";
import type { User } from "@/types";

describe("Charlie administration boundaries", () => {
  it("accepts only known deep-linked tabs", () => {
    expect(normalizeCharlieAdminTab("mode")).toBe("mode");
    expect(normalizeCharlieAdminTab("secrets")).toBe("connection");
  });
  it("implements the complete keyboard tab order", () => {
    const tabs = ["one", "two", "three"] as const;
    expect(adjacentTab(tabs, "one", "ArrowRight")).toBe("two");
    expect(adjacentTab(tabs, "one", "ArrowLeft")).toBe("three");
    expect(adjacentTab(tabs, "two", "Home")).toBe("one");
    expect(adjacentTab(tabs, "two", "End")).toBe("three");
    expect(adjacentTab(tabs, "two", "Enter")).toBeNull();
  });
  it("parses object packages without rendering their fields", () => {
    expect(parseOnboardingFile('{"package_id":"x"}')).toEqual({
      package_id: "x",
    });
    expect(() => parseOnboardingFile("[]")).toThrow(/object/);
  });
  it("shows every independent diagnostic even when Charlie omits one", () => {
    const checks = completeDiagnostics([
      { id: "local_config", label: "Local", state: "healthy", summary: "ok" },
    ]);
    expect(checks).toHaveLength(10);
    expect(checks.find((c) => c.id === "agent_standby")?.state).toBe("unknown");
  });
  it("preserves filters and context in deep links", () => {
    const current = new URLSearchParams("tab=findings&filter=open&context=cluster-a&finding=f-1");
    expect(mergeCharlieSearch(current, {tab:"approvals",approval:"a-1"})).toBe("tab=approvals&filter=open&context=cluster-a&finding=f-1&approval=a-1");
  });
  it("requires charlie:manage but remains available while runtime is disabled", () => {
    const admin = { id: "u", isSuperuser: true } as unknown as User;
    expect(canManageCharlie(admin)).toBe(true);
    expect(canManageCharlie({ id: "u" } as User)).toBe(false);
  });
  it("rejects hidden or invalid automation defaults before transport", () => {
    expect(automationValidationIssues({ rules: [{
      name: "", sourceType: "", severities: [], cooldownSeconds: 0,
      gracePeriodSeconds: 1, flapWindowSeconds: 1, flapCount: 1,
      fleetThresholdPercent: 101, maximumAttempts: 1, serviceIdentity: "", modeCeiling: "invalid",
    }] })).toEqual(expect.arrayContaining([
      "Rule 1 needs a name.",
      "Rule 1 needs a source type.",
      "Rule 1 needs a service identity.",
      "Rule 1 needs a valid mode ceiling.",
      "Rule 1 needs at least one severity.",
      "Rule 1 fleet threshold must be between 0 and 100.",
    ]));
  });
});
