import { describe, expect, it } from "vitest";
import type { CharlieFinding } from "@/lib/api/charlie";
import {
  findingLifecycleDecisions,
  findingWorkflowLabel,
  findingWorkflowGuidance,
} from "../finding-workflow";

function fixture(
  workflowState: CharlieFinding["workflowState"],
  availableDecisions: CharlieFinding["availableDecisions"],
): CharlieFinding {
  return {
    id: "finding-a",
    title: "Finding",
    severity: "medium",
    state: "open",
    affectedResource: { type: "installation", id: "local", requiredVerb: "read" },
    summary: "bounded",
    workflowState,
    availableDecisions,
  };
}

describe("Charlie finding workflow decisions", () => {
  it("renders only lifecycle decisions for manual remediation", () => {
    const finding = fixture("manual_remediation_required", [
      "acknowledge",
      "start_remediation",
      "dismiss",
    ]);
    expect(findingLifecycleDecisions(finding)).toEqual([
      "acknowledge",
      "start_remediation",
      "dismiss",
    ]);
    expect(findingWorkflowLabel(finding)).toBe("manual remediation required");
  });

  it("keeps exact approval decisions out of generic lifecycle controls", () => {
    const finding = fixture("approval_pending", []);
    expect(findingLifecycleDecisions(finding)).toEqual([]);
    expect(findingWorkflowLabel(finding)).toBe("approval pending");
    expect(findingWorkflowGuidance(finding)).toContain("separate approvals list");
  });

  it("makes blocked automation explicit and non-executing", () => {
    const finding = fixture("manual_remediation_required", [
      "start_remediation",
      "dismiss",
    ]);
    expect(findingWorkflowGuidance(finding)).toContain("No Charlie action is authorized");
  });

  it.each([
    "resolved",
    "rejected",
    "dismissed",
    "expired",
  ] as const)("exposes no decision for terminal %s", (state) => {
    expect(findingLifecycleDecisions(fixture(state, []))).toEqual([]);
  });
});
