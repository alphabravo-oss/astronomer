import { Trash2 } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import type {
  CharlieAutomationView,
  CharlieTriggerRule,
} from "@/lib/api/charlie-admin";
import { Field, NumberField, Section, button } from "./shared";

export function AutomationRuleSection({
  rule: r,
  index: i,
  ruleCount,
  update,
  onDelete,
}: {
  rule: CharlieTriggerRule;
  index: number;
  ruleCount: number;
  update: (
    index: number,
    patch: Partial<CharlieAutomationView["rules"][number]>,
  ) => void;
  onDelete: () => void;
}) {
  return (
    <Section
      key={r.id || `new-${i}`}
      title={r.name || "New trigger rule"}
      description={`${r.enabled ? "Enabled" : "Disabled"} · ${r.sourceType} · ${r.modeCeiling.replaceAll("_", " ")}`}
    >
      <label className="flex gap-2 text-sm">
        <input
          type="checkbox"
          checked={r.enabled}
          onChange={(e) => update(i, { enabled: e.target.checked })}
        />
        Enabled
      </label>
      <details
        className="rounded-lg border border-border bg-background p-3"
        open={r.enabled || !r.id || ruleCount <= 2}
      >
        <summary className="cursor-pointer text-sm font-medium">
          Edit trigger rule
        </summary>
        <div className="mt-3 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <Field
            label="Rule name"
            value={r.name}
            set={(v) => update(i, { name: v })}
          />
          <Field
            label="Source type"
            value={r.sourceType}
            set={(v) => update(i, { sourceType: v })}
          />
          <Field
            label="Severities (comma separated)"
            value={(r.severities ?? []).join(", ")}
            set={(v) =>
              update(i, {
                severities: v
                  .split(",")
                  .map((value) => value.trim().toLowerCase())
                  .filter(Boolean),
              })
            }
          />
          <Field
            label="Service identity"
            value={r.serviceIdentity}
            set={(v) => update(i, { serviceIdentity: v })}
          />
          <label className="space-y-1 text-sm">
            <span className="block font-medium">Mode ceiling</span>
            <select
              className="h-(--control-h) w-full rounded-md border border-input bg-background px-3 text-sm"
              value={r.modeCeiling}
              onChange={(event) =>
                update(i, {
                  modeCeiling: event.target
                    .value as CharlieTriggerRule["modeCeiling"],
                })
              }
            >
              <option value="read_only">Read only</option>
              <option value="approval">Approval required</option>
              <option value="auto">Autonomous</option>
            </select>
            <span className="block text-xs text-muted-foreground">
              This rule can never exceed the deployment mode. Autonomous also
              requires an eligible central allowlist and local action policy.
            </span>
          </label>
          <NumberField
            label="Cooldown seconds"
            value={r.cooldownSeconds}
            min={1}
            set={(v) => update(i, { cooldownSeconds: v })}
          />
          <NumberField
            label="Grace period seconds"
            value={r.gracePeriodSeconds}
            min={1}
            set={(v) => update(i, { gracePeriodSeconds: v })}
          />
          <NumberField
            label="Flap window seconds"
            value={r.flapWindowSeconds}
            min={1}
            set={(v) => update(i, { flapWindowSeconds: v })}
          />
          <NumberField
            label="Flap count"
            value={r.flapCount}
            min={1}
            set={(v) => update(i, { flapCount: v })}
          />
          <NumberField
            label="Cluster coverage threshold %"
            value={r.estateThresholdPercent}
            min={0}
            max={100}
            set={(v) => update(i, { estateThresholdPercent: v })}
          />
          <NumberField
            label="Maximum attempts"
            value={r.maximumAttempts}
            min={1}
            set={(v) => update(i, { maximumAttempts: v })}
          />
          <Field
            label="Minimum agent version"
            value={r.minimumAgentVersion ?? ""}
            set={(v) => update(i, { minimumAgentVersion: v })}
          />
          <Field
            label="Scopes (comma separated)"
            value={(r.scopes ?? []).join(", ")}
            set={(v) =>
              update(i, {
                scopes: v
                  .split(",")
                  .map((x) => x.trim())
                  .filter(Boolean),
              })
            }
          />
        </div>
      </details>
      <div className="flex flex-wrap gap-4">
        <label className="flex gap-2 text-sm">
          <input
            type="checkbox"
            checked={r.suppressed}
            onChange={(e) => update(i, { suppressed: e.target.checked })}
          />
          Suppressed
        </label>
        <label className="flex gap-2 text-sm">
          <input
            type="checkbox"
            checked={r.deadLetterEnabled}
            onChange={(e) => update(i, { deadLetterEnabled: e.target.checked })}
          />
          Dead letters
        </label>
        <ActionButton
          intent="bare"
          size="none"
          className={`${button} text-status-error`}
          onClick={onDelete}
        >
          <Trash2 className="h-4 w-4" />
          Delete rule
        </ActionButton>
      </div>
    </Section>
  );
}
