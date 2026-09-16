/** Human-facing product mode badge for chat drawer + hub. */
export const productModeCopy = {
  disabled: {
    key: "disabled" as const,
    label: "Disabled",
    short: "Off",
    ceiling:
      "No Charlie sessions, triggers, approvals, or actions are allowed.",
    badgeClass: "border-status-error/40 bg-status-error/10 text-status-error",
  },
  read_only: {
    key: "read_only" as const,
    label: "Read only",
    short: "Read only",
    ceiling:
      "Investigation and findings only. Charlie cannot change cluster state; write requests become guidance.",
    badgeClass: "border-status-info/40 bg-status-info/10 text-status-info",
  },
  approval: {
    key: "approval" as const,
    label: "Approval mode",
    short: "Approval",
    ceiling:
      "Includes read-only. Every exact write is proposed for human review — use the approval card or Approvals tab to confirm.",
    badgeClass:
      "border-status-warning/40 bg-status-warning/10 text-status-warning",
  },
  auto: {
    key: "auto" as const,
    label: "Autonomous",
    short: "Auto",
    ceiling:
      "Includes read-only and approval. Only explicitly allowlisted safe writes may run without a click; other writes still need approval.",
    badgeClass:
      "border-status-success/40 bg-status-success/10 text-status-success",
  },
} as const;

export function productModePresentation(mode: string | undefined) {
  if (mode && mode in productModeCopy) {
    return productModeCopy[mode as keyof typeof productModeCopy];
  }
  return {
    key: "unknown" as const,
    label: "Mode unknown",
    short: "Unknown",
    ceiling:
      "Hard ceiling is unavailable; Charlie must not assume write authority.",
    badgeClass: "border-border bg-muted text-muted-foreground",
  };
}

export type CharlieModePresentation = ReturnType<
  typeof productModePresentation
>;
