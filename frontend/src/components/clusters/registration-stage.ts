import type { RegistrationPhase } from "@/lib/api/cluster-registration";

const progressPhases = new Set<RegistrationPhase>([
  "connected",
  "provisioning",
  "ready",
  "failed",
]);

export function registrationWizardStep(
  phase: RegistrationPhase | undefined,
  progressRequested: boolean,
): 2 | 3 {
  return progressRequested || (phase != null && progressPhases.has(phase))
    ? 3
    : 2;
}
