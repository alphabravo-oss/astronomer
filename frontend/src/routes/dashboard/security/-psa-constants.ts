import type { PodSecurityLevel } from "@/types";

export const psaLevels: PodSecurityLevel[] = [
  "privileged",
  "baseline",
  "restricted",
];

export const psaLevelColors: Record<PodSecurityLevel | "unknown", string> = {
  unknown: "bg-muted text-muted-foreground",
  privileged: "bg-status-error/10 text-status-error",
  baseline: "bg-status-warning/10 text-status-warning",
  restricted: "bg-status-success/10 text-status-success",
};

// On-page reference copy for the three Pod Security Standards and the three
// admission modes. Kept here next to psaLevelColors so the explainer and the
// table badges stay in lockstep.
export const psaLevelDefs: { level: PodSecurityLevel; summary: string }[] = [
  {
    level: "privileged",
    summary:
      "Unrestricted — no policy applied. For trusted/system namespaces or to opt out of PSA.",
  },
  {
    level: "baseline",
    summary:
      "Minimally restrictive — blocks known privilege escalations while staying broadly compatible.",
  },
  {
    level: "restricted",
    summary:
      "Heavily restricted — follows current pod-hardening best practices. Recommended for production.",
  },
];

export const psaModeDefs: { mode: string; summary: string }[] = [
  {
    mode: "enforce",
    summary: "Rejects pods that violate the standard at admission time.",
  },
  {
    mode: "audit",
    summary: "Allows the pod but records a violation in the audit log.",
  },
  {
    mode: "warn",
    summary: "Allows the pod but returns a user-facing warning to the client.",
  },
];
