import type { CharlieFinding } from "@/lib/api/charlie";

export type FindingLifecycleDecision =
  | "acknowledge"
  | "start_remediation"
  | "request_verification"
  | "dismiss"
  | "resolve";

const lifecycle = new Set<string>([
  "acknowledge",
  "start_remediation",
  "request_verification",
  "dismiss",
  "resolve",
]);

export function findingLifecycleDecisions(
  finding: CharlieFinding,
): FindingLifecycleDecision[] {
  return finding.availableDecisions.filter(
    (decision): decision is FindingLifecycleDecision => lifecycle.has(decision),
  );
}

export function findingWorkflowLabel(finding: CharlieFinding): string {
  return finding.workflowState.replaceAll("_", " ");
}

export function findingDecisionLabel(decision: FindingLifecycleDecision): string {
  return decision.replaceAll("_", " ");
}

export function findingWorkflowGuidance(finding: CharlieFinding): string {
  switch (finding.workflowState) {
    case "approval_pending":
      return "This action requires the linked exact human approval. Acknowledging the finding or changing mode does not approve or retry it.";
    case "manual_remediation_required":
      return finding.proposedAction?.mode === "auto"
        ? "Automation did not run. Follow the bounded manual checks or start manual remediation; changing mode does not retry the action."
        : "Follow the bounded manual checks or start manual remediation. No Charlie action is authorized by this finding.";
    case "remediation_in_progress":
      return "Complete the manual steps, then request product-owned verification. This does not authorize Charlie execution.";
    case "verification_pending":
      return "Resolve only after the current product state passes the listed verification; otherwise return to remediation.";
    default:
      return `This workflow is ${finding.workflowState.replaceAll("_", " ")}; no further decision is available.`;
  }
}
