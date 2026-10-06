import { Save } from "lucide-react";
import { StatusBadge } from "@/components/ui/status-badge";
import { ActionButton } from "@/components/ui/action-button";
import type {
  CharlieActionPolicyInput,
  CharlieAutomationView,
} from "@/lib/api/charlie-admin";
import { actionPolicyValuesValid } from "./automation-model";
import { Meta, NumberField, Section } from "./shared";

type ActionPolicy = CharlieAutomationView["actionPolicies"][number];

export function AutomationActionPolicies({
  policies,
  savedPolicies,
  policyError,
  saving,
  updatePolicy,
  onSave,
}: {
  policies: ActionPolicy[];
  savedPolicies: ActionPolicy[] | undefined;
  policyError: string;
  saving: boolean;
  updatePolicy: (index: number, patch: Partial<ActionPolicy>) => void;
  onSave: (input: CharlieActionPolicyInput) => void;
}) {
  return (
    <Section
      title="Automatic-action policies"
      description="The exact intersection of Charlie's central capability allowlist and Astronomer's local budgets and safety controls. This view never includes action arguments or authority references."
    >
      {policies.length ? (
        <div className="space-y-3">
          {policies.map((policy, policyIndex) => {
            const mayEnable =
              policy.centralState === "verified" &&
              policy.centralAllowlisted &&
              policy.autoEligible;
            const original = savedPolicies?.find(
              (candidate) => candidate.capability === policy.capability,
            );
            const changed =
              !!original &&
              (
                [
                  "enabled",
                  "maxActionsPerIncident",
                  "maxActionsPerWindow",
                  "budgetWindowSeconds",
                  "cooldownSeconds",
                ] as const
              ).some((field) => original[field] !== policy[field]);
            const valuesValid = actionPolicyValuesValid(policy);
            return (
              <article
                key={policy.capability}
                className="rounded-lg border p-4"
              >
                <div className="flex flex-wrap items-start justify-between gap-2">
                  <div>
                    <h3 className="font-mono text-sm font-medium">
                      {policy.capability}
                    </h3>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {policy.effect}
                    </p>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <StatusBadge
                      status={policy.enabled ? "healthy" : "disabled"}
                      label={policy.enabled ? "Enabled" : "Disabled"}
                    />
                    <StatusBadge
                      status={
                        policy.centralState === "verified"
                          ? "healthy"
                          : "unavailable"
                      }
                      label={`Central: ${policy.centralState}`}
                    />
                    <StatusBadge
                      status={policy.circuitState}
                      label={`Circuit: ${policy.circuitState}`}
                    />
                  </div>
                </div>
                <dl className="mt-3 grid gap-3 text-xs sm:grid-cols-2 lg:grid-cols-4">
                  <Meta label="Risk" value={policy.risk} />
                  <Meta
                    label="Auto eligible"
                    value={policy.autoEligible ? "Yes" : "No"}
                  />
                  <Meta
                    label="Central allowlisted"
                    value={policy.centralAllowlisted ? "Yes" : "No"}
                  />
                  <Meta label="Revision" value={policy.revision} />
                </dl>
                <div className="mt-3 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                  <NumberField
                    label="Max actions per incident"
                    value={policy.maxActionsPerIncident}
                    min={1}
                    max={100}
                    set={(value) =>
                      updatePolicy(policyIndex, {
                        maxActionsPerIncident: value,
                      })
                    }
                  />
                  <NumberField
                    label="Max actions per window"
                    value={policy.maxActionsPerWindow}
                    min={1}
                    max={100}
                    set={(value) =>
                      updatePolicy(policyIndex, {
                        maxActionsPerWindow: value,
                      })
                    }
                  />
                  <NumberField
                    label="Budget window seconds"
                    value={policy.budgetWindowSeconds}
                    min={60}
                    max={86400}
                    set={(value) =>
                      updatePolicy(policyIndex, {
                        budgetWindowSeconds: value,
                      })
                    }
                  />
                  <NumberField
                    label="Cooldown seconds"
                    value={policy.cooldownSeconds}
                    min={30}
                    max={604800}
                    set={(value) =>
                      updatePolicy(policyIndex, { cooldownSeconds: value })
                    }
                  />
                </div>
                <label className="mt-3 flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    aria-label={`Enable ${policy.capability}`}
                    checked={policy.enabled}
                    disabled={!policy.enabled && !mayEnable}
                    onChange={(event) =>
                      updatePolicy(policyIndex, {
                        enabled: event.target.checked,
                      })
                    }
                  />
                  Enable bounded automatic action
                </label>
                {!mayEnable && !policy.enabled && (
                  <p className="mt-2 text-xs text-status-warning">
                    Enabling is unavailable until Charlie central is verified,
                    centrally allowlists this capability, and marks it auto
                    eligible.
                  </p>
                )}
                <p className="mt-3 text-xs">
                  <span className="font-medium">Scope:</span>{" "}
                  {policy.scopeSummary}
                </p>
                <p className="mt-2 text-xs">
                  <span className="font-medium">Verification:</span>{" "}
                  {policy.verification}
                </p>
                <div className="mt-2 text-xs">
                  <span className="font-medium">Preconditions:</span>{" "}
                  {policy.preconditions.length
                    ? policy.preconditions.join("; ")
                    : "None published"}
                </div>
                <ActionButton
                  intent="primary"
                  className="mt-3"
                  disabled={!changed || !valuesValid || saving}
                  onClick={() => onSave(policy)}
                >
                  <Save className="h-4 w-4" />
                  Save action policy
                </ActionButton>
              </article>
            );
          })}
          {policyError && (
            <p
              role="alert"
              className="rounded-lg border border-status-error/30 p-3 text-sm text-status-error"
            >
              {policyError}
            </p>
          )}
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">
          No bounded automatic-action policy was published. Astronomer will not
          imply automatic authority.
        </p>
      )}
    </Section>
  );
}
