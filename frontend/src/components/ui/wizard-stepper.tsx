import { Check } from "lucide-react";
import { cn } from "@/lib/utils";

export interface WizardStep {
  id: string;
  label: string;
}

interface WizardStepperProps {
  steps: readonly WizardStep[];
  currentStep: number;
  className?: string;
}

export function WizardStepper({
  steps,
  currentStep,
  className,
}: WizardStepperProps) {
  return (
    <nav aria-label="Registration progress" className={className}>
      <ol className="grid gap-2 sm:grid-flow-col sm:grid-cols-none">
        {steps.map((step, index) => {
          const number = index + 1;
          const complete = number < currentStep;
          const current = number === currentStep;

          return (
            <li
              key={step.id}
              aria-current={current ? "step" : undefined}
              className={cn(
                "flex min-w-0 items-center gap-2 rounded-lg border px-3 py-2 text-sm",
                current
                  ? "border-primary bg-primary/10 text-foreground"
                  : complete
                    ? "border-status-success/30 bg-status-success/5 text-foreground"
                    : "border-border bg-card text-muted-foreground",
              )}
            >
              <span
                aria-hidden="true"
                className={cn(
                  "flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-xs font-semibold",
                  current
                    ? "bg-primary text-primary-foreground"
                    : complete
                      ? "bg-status-success text-white"
                      : "bg-muted text-muted-foreground",
                )}
              >
                {complete ? <Check className="h-3.5 w-3.5" /> : number}
              </span>
              <span className="truncate font-medium">{step.label}</span>
              <span className="sr-only">
                {current ? "Current step" : complete ? "Completed" : "Upcoming"}
              </span>
            </li>
          );
        })}
      </ol>
    </nav>
  );
}

export const REGISTRATION_STEPS = [
  { id: "details", label: "Cluster details" },
  { id: "connect", label: "Install agent" },
  { id: "progress", label: "Adoption progress" },
] as const satisfies readonly WizardStep[];
