import { withForm } from "@/lib/form";
import { PLATFORM_SETTINGS_DEFAULTS } from "@/lib/platform-settings-model";

export const GovernanceFields = withForm({
  defaultValues: PLATFORM_SETTINGS_DEFAULTS,
  render: function GovernanceFields({ form }) {
    return (
      <section className="rounded-xl border border-border bg-card p-6 space-y-4">
        <div>
          <h2 className="text-base font-semibold text-foreground">
            Account retention and read audit
          </h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Changes are audited and apply without restarting the platform.
          </p>
        </div>
        <form.AppField name="governance.inactiveRetentionDays">
          {(field) => (
            <field.NumberField
              label="Deactivate inactive accounts after (days)"
              min={1}
              max={3650}
            />
          )}
        </form.AppField>
        <p className="text-xs text-muted-foreground">
          The daily sweep deactivates human accounts based on last successful
          login (or creation), revokes their tokens and sessions, and records an
          audit event. Superusers and service accounts are excluded.
        </p>
        <form.AppField name="governance.readAuditTier">
          {(field) => (
            <field.SelectField label="Read-audit coverage">
              <option value="standard">Standard — configured policies</option>
              <option value="diagnostic">
                Diagnostic — policies + 10% of all reads
              </option>
              <option value="incident">
                Incident — all authenticated reads
              </option>
            </field.SelectField>
          )}
        </form.AppField>
        <p className="text-xs text-muted-foreground">
          Higher tiers add coverage without weakening existing policies. Request
          and response bodies are never recorded. Incident mode increases audit
          volume; return to Standard after investigation. Changes reach all
          replicas within 30 seconds.
        </p>
      </section>
    );
  },
});
