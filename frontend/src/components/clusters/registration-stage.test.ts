import { describe, expect, it } from "vitest";
import { registrationWizardStep } from "./registration-stage";

describe("registrationWizardStep", () => {
  it.each([undefined, "created", "awaiting_agent"] as const)(
    "keeps %s on the install step",
    (phase) => expect(registrationWizardStep(phase, false)).toBe(2),
  );

  it.each(["connected", "provisioning", "ready", "failed"] as const)(
    "restores %s directly into inline progress",
    (phase) => expect(registrationWizardStep(phase, false)).toBe(3),
  );

  it("advances immediately after an explicit confirmation", () => {
    expect(registrationWizardStep("awaiting_agent", true)).toBe(3);
  });
});
