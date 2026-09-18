import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  REGISTRATION_STEPS,
  WizardStepper,
} from "@/components/ui/wizard-stepper";

describe("WizardStepper", () => {
  it("exposes the current, completed, and upcoming steps", () => {
    render(<WizardStepper steps={REGISTRATION_STEPS} currentStep={2} />);

    expect(
      screen.getByRole("navigation", { name: "Registration progress" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Install agent").closest("li")).toHaveAttribute(
      "aria-current",
      "step",
    );
    expect(screen.getByText("Completed")).toBeInTheDocument();
    expect(screen.getByText("Upcoming")).toBeInTheDocument();
  });
});
