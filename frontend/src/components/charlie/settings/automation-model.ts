import type {
  CharlieAutomationView,
  CharlieTriggerRule,
} from "@/lib/api/charlie-admin";

export const newAutomationRule = (): CharlieTriggerRule => ({
  id: "",
  name: "",
  enabled: false,
  sourceType: "event",
  severities: ["high", "critical"],
  scopes: [],
  cooldownSeconds: 1800,
  gracePeriodSeconds: 300,
  flapWindowSeconds: 900,
  flapCount: 3,
  estateThresholdPercent: 25,
  minimumAgentVersion: "",
  suppressed: false,
  maximumAttempts: 3,
  deadLetterEnabled: true,
  serviceIdentity: "system:charlie-automation",
  modeCeiling: "read_only",
});

export function actionPolicyValuesValid(
  policy: CharlieAutomationView["actionPolicies"][number],
) {
  return (
    policy.maxActionsPerIncident >= 1 &&
    policy.maxActionsPerIncident <= 100 &&
    policy.maxActionsPerWindow >= 1 &&
    policy.maxActionsPerWindow <= 100 &&
    policy.budgetWindowSeconds >= 60 &&
    policy.budgetWindowSeconds <= 86400 &&
    policy.cooldownSeconds >= 30 &&
    policy.cooldownSeconds <= 604800
  );
}
