import type { CharlieMode } from "@/lib/api/charlie-admin";

export const modeHelp: Record<CharlieMode, string> = {
  disabled:
    "No new sessions, triggers, findings, claims, approvals, actions, or MCP calls. Health and audit remain available.",
  read_only:
    "Charlie can investigate and explain through authorized reads, but cannot propose executable approvals.",
  approval:
    "Includes Read only. Charlie may propose bounded actions; an eligible authorized user must approve each exact action.",
  auto: "Includes Read only and Approval required. Charlie may additionally execute only capabilities explicitly allowed by current product policy and disclosure.",
};
export const productModeLabel: Record<CharlieMode, string> = {
  disabled: "Disabled",
  read_only: "Read only",
  approval: "Approval required",
  auto: "Automation",
};
/** What operators should expect after a successful transition into each mode. */
export const modeAllowedSummary: Record<CharlieMode, string[]> = {
  disabled: [
    "No new Charlie sessions, triggers, findings, or MCP tool calls",
    "Health, configuration, and audit remain available",
  ],
  read_only: [
    "Chat, investigation, and authorized product reads",
    "No product writes — write requests stay guidance-only",
  ],
  approval: [
    "All read-only investigation capabilities",
    "Bounded writes only after an exact human approval of the proposed action",
  ],
  auto: [
    "All read-only investigation capabilities",
    "Human-approved writes still available",
    "Only centrally allowlisted, auto-eligible writes may run without a click",
    "Live RBAC, disclosure, and policy are rechecked on every write",
  ],
};

export type ModeTransitionPhase =
  "idle" | "applying" | "verifying" | "ready" | "failed";

export type ModeTransitionState = {
  phase: ModeTransitionPhase;
  target?: CharlieMode;
  from?: CharlieMode;
  message?: string;
  startedAt?: number;
};

/** Agent ceiling verified and live mode matches — safe for product work. */
export function charlieModeWorkReady(
  mode: {
    requested: CharlieMode;
    authoritative: CharlieMode;
    workloadCeilingReady: boolean;
    disablePending?: boolean;
    emergencyDisabled: boolean;
  },
  agent?: {
    desiredReplicas: number;
    readyReplicas: number;
    replicas?: Array<{ state: string }>;
  } | null,
): boolean {
  if (mode.requested !== mode.authoritative) return false;
  if (!mode.workloadCeilingReady) return false;
  if (mode.disablePending) return false;
  if (mode.emergencyDisabled && mode.authoritative !== "disabled") return false;
  if (agent) {
    if (
      agent.desiredReplicas > 0 &&
      agent.readyReplicas < agent.desiredReplicas
    ) {
      return false;
    }
    if (
      agent.replicas?.some(
        (replica) =>
          replica.state === "degraded" || replica.state === "unavailable",
      )
    ) {
      return false;
    }
  }
  return true;
}
