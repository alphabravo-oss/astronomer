import { createFileRoute } from "@tanstack/react-router";
/**
 * /dashboard/settings/platform — branding, banners, feature flags, token
 * TTL, and telemetry. Each section maps to a stable dotted-key prefix on
 * the backend (`branding.*`, `banner.*`, `feature.*`, ...); we hydrate a
 * flat key/value snapshot from `GET /admin/settings/`, mirror it into a
 * grouped form-state struct, and on save diff against the original to only
 * push keys that actually changed.
 */
import { useMemo, useEffect, useState } from "react";
import { Link } from "@/lib/link";
import { ArrowLeft, Loader2, Pencil, Save } from "lucide-react";
import { cn } from "@/lib/utils";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { ModalShell } from "@/components/ui/modal-shell";
import { PageHeader, PageShell } from "@/components/ui/page";
import { Textarea } from "@/components/ui/textarea";
import { KeyStatusPanel } from "@/components/settings/key-status-panel";
import { toastInfo } from "@/lib/toast";
import { useAppForm } from "@/lib/form";
import {
  usePlatformSettings,
  useSavePlatformSettings,
} from "@/components/settings/hooks";
import type { PlatformSettingsGrouped } from "@/lib/api/platform-settings";
import {
  diffPlatformSettings,
  hydratePlatformSettings,
} from "@/lib/platform-settings-model";

// Banner textareas are text-sm / row-sized, unlike the kit's mono default —
// merged over the kit textarea class (twMerge, later wins).
const bannerTextareaClassName = "min-h-0 text-sm font-sans";

function Section({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-xl border border-border bg-card p-6 space-y-4">
      <div>
        <h2 className="text-base font-semibold text-foreground">{title}</h2>
        {description && (
          <p className="text-xs text-muted-foreground mt-0.5">{description}</p>
        )}
      </div>
      <div className="space-y-4">{children}</div>
    </section>
  );
}

function BannerPreview({
  text,
  color,
}: {
  text: string;
  color: PlatformSettingsGrouped["banners"]["globalBannerColor"];
}) {
  if (!text) {
    return (
      <p className="text-xs text-muted-foreground italic">
        No banner — leave blank to hide.
      </p>
    );
  }
  const palette: Record<typeof color, string> = {
    info: "bg-status-info/10 border-status-info/30 text-status-info",
    warning:
      "bg-status-warning/10 border-status-warning/30 text-status-warning",
    critical: "bg-status-error/10 border-status-error/30 text-status-error",
  };
  return (
    <div
      className={cn(
        "rounded-lg border px-3 py-2 text-xs whitespace-pre-wrap",
        palette[color],
      )}
    >
      {text}
    </div>
  );
}

function PlatformSettingsForm({ onSaved }: { onSaved?: () => void }) {
  const { data: flat, isLoading } = usePlatformSettings();
  const save = useSavePlatformSettings();

  const initial = useMemo<PlatformSettingsGrouped>(
    () => hydratePlatformSettings(flat ?? []),
    [flat],
  );

  const form = useAppForm({
    defaultValues: initial,
    onSubmit: async ({ value }) => {
      // Same pre-save gate as before: diff against the snapshot and only push
      // keys that actually changed.
      const dirty = diffPlatformSettings(initial, value);
      if (Object.keys(dirty).length === 0) {
        toastInfo("Nothing to save");
        return;
      }
      try {
        await save.mutateAsync(dirty);
        onSaved?.();
      } catch {
        // Toast handled by mutation.
      }
    },
  });

  // Re-hydrate the form whenever the snapshot lands. We do this once on load
  // and again after a save invalidates the query, which keeps the form in
  // sync with whatever the backend ultimately stored.
  useEffect(() => {
    form.reset(initial);
  }, [form, initial]);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-48">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <Section
        title="Branding"
        description="Logo, product name, colors. Applied across the dashboard chrome."
      >
        <form.AppField name="branding.productName">
          {(field) => <field.TextField label="Product name" />}
        </form.AppField>
        <form.AppField name="branding.logoUrl">
          {(field) => (
            <field.TextField label="Logo URL" placeholder="https://..." />
          )}
        </form.AppField>
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="field-5257f27b-245"
          >
            Primary color
          </label>
          <form.Field name="branding.primaryColor">
            {(field) => (
              <div className="flex items-center gap-3">
                <Input
                  id="field-5257f27b-245"
                  type="text"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="#3b82f6"
                  className="flex-1 font-mono"
                />
                <div
                  className="w-10 h-10 rounded-lg border border-border"
                  style={{ backgroundColor: field.state.value }}
                  title={field.state.value}
                />
              </div>
            )}
          </form.Field>
          <p className="text-xs text-muted-foreground">
            Hex string (e.g. <span className="font-mono">#3b82f6</span>).
          </p>
        </div>
        <form.AppField name="branding.supportUrl">
          {(field) => (
            <field.TextField
              label="Support URL"
              placeholder="https://help.example.com"
            />
          )}
        </form.AppField>
        <form.AppField name="branding.copyright">
          {(field) => (
            <field.TextField
              label="Copyright"
              placeholder="© 2026 Example Corp."
            />
          )}
        </form.AppField>
      </Section>

      <Section
        title="Banners"
        description="Optional banner text shown on the login screen and inside the dashboard."
      >
        <form.AppField name="banners.loginBannerText">
          {(field) => (
            <field.TextareaField
              label="Login banner"
              placeholder="Authorized access only — your session is recorded."
              rows={3}
              className={bannerTextareaClassName}
            />
          )}
        </form.AppField>
        <form.AppField name="banners.globalBannerText">
          {(field) => (
            <field.TextareaField
              label="Global banner"
              placeholder="Maintenance window 18:00 UTC tonight."
              rows={3}
              className={bannerTextareaClassName}
            />
          )}
        </form.AppField>
        <form.AppField name="banners.globalBannerColor">
          {(field) => (
            <field.SelectField label="Global banner color">
              <option value="info">Info (blue)</option>
              <option value="warning">Warning (amber)</option>
              <option value="critical">Critical (red)</option>
            </field.SelectField>
          )}
        </form.AppField>
        <div className="space-y-1.5">
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            Preview
          </p>
          <form.Subscribe selector={(s) => s.values.banners}>
            {(banners) => (
              <BannerPreview
                text={banners.globalBannerText}
                color={
                  banners.globalBannerColor as PlatformSettingsGrouped["banners"]["globalBannerColor"]
                }
              />
            )}
          </form.Subscribe>
        </div>
      </Section>

      <Section
        title="Feature flags"
        description="Hide entire dashboard areas from the sidebar. Server-side authorisation still applies regardless."
      >
        <form.AppField name="features.catalog">
          {(field) => <field.SwitchField label="Catalog" />}
        </form.AppField>
        <form.AppField name="features.projects">
          {(field) => <field.SwitchField label="Projects" />}
        </form.AppField>
        <form.AppField name="features.monitoring">
          {(field) => <field.SwitchField label="Monitoring" />}
        </form.AppField>
        <form.AppField name="features.security">
          {(field) => <field.SwitchField label="Security" />}
        </form.AppField>
        <form.AppField name="features.backups">
          {(field) => <field.SwitchField label="Backups" />}
        </form.AppField>
        <form.AppField name="features.extensions">
          {(field) => (
            <field.SwitchField
              label="Extensions"
              helper="UI extension registry. Leave off until the marketplace is ready."
            />
          )}
        </form.AppField>
      </Section>

      <Section
        title="Token TTL"
        description="Defaults applied to newly minted API tokens."
      >
        <form.AppField name="tokens.defaultTtlMinutes">
          {(field) => (
            <field.NumberField label="Default TTL (minutes)" min={0} />
          )}
        </form.AppField>
        <form.AppField name="tokens.maxTtlMinutes">
          {(field) => (
            <field.NumberField label="Maximum TTL (minutes)" min={1} />
          )}
        </form.AppField>
        <form.Subscribe selector={(s) => s.values.tokens}>
          {(tokens) => (
            <p className="text-xs text-muted-foreground">
              {tokens.defaultTtlMinutes === 0
                ? "Default: no expiry"
                : `Default: ${humanTtlMinutes(tokens.defaultTtlMinutes)}`}
              {" · "}
              Max: {humanTtlMinutes(tokens.maxTtlMinutes)}
            </p>
          )}
        </form.Subscribe>
      </Section>

      <Section
        title="Browser session"
        description="JWT access-token lifetime for interactive logins (password, SSO, TOTP). Absolute TTL at mint/refresh — not an idle timeout."
      >
        <form.AppField name="session.timeoutMinutes">
          {(field) => (
            <field.NumberField
              label="Access token lifetime (minutes)"
              min={5}
            />
          )}
        </form.AppField>
        <form.Subscribe selector={(s) => s.values.session.timeoutMinutes}>
          {(timeoutMinutes) => (
            <p className="text-xs text-muted-foreground">
              Absolute JWT <span className="font-mono">exp</span> applied on
              every mint and refresh (setting key{" "}
              <span className="font-mono">session.timeout_minutes</span>).
              Activity does not slide the access token; use refresh to obtain a
              new one under this same cap. Compliance baselines may pin this
              value (e.g. 15–20 minutes).
              {timeoutMinutes >= 60
                ? ` Current ≈ ${Math.round(timeoutMinutes / 60)} hour(s).`
                : ` Current = ${timeoutMinutes} minute(s).`}
            </p>
          )}
        </form.Subscribe>
      </Section>

      <Section
        title="Telemetry"
        description="Anonymous usage signals. Opt-in only."
      >
        <form.AppField name="telemetry.enabled">
          {(field) => (
            <field.SwitchField
              label="Enable telemetry"
              helper="Sends platform version + cluster count to the endpoint below."
            />
          )}
        </form.AppField>
        <form.AppField name="telemetry.endpoint">
          {(field) => (
            <field.TextField
              label="Endpoint URL"
              placeholder="https://telemetry.example.com/v1/ingest"
            />
          )}
        </form.AppField>
      </Section>

      <Section
        title="Cluster registration TLS"
        description="Controls which curl variant the cluster-registration wizard shows by default and whether the public /api/v1/register/ca.crt endpoint serves a CA bundle."
      >
        <form.Field name="registration.tlsMode">
          {(field) => (
            <>
              <div className="space-y-1.5">
                <span
                  id="tls-posture-label"
                  className="text-sm font-medium text-foreground"
                >
                  TLS posture
                </span>
                <div
                  role="radiogroup"
                  aria-labelledby="tls-posture-label"
                  className="grid grid-cols-1 md:grid-cols-3 gap-2"
                >
                  {(
                    [
                      {
                        v: "public_ca",
                        label: "Public CA",
                        hint: "Server certificate signed by a publicly-trusted CA. curl works with no flags.",
                      },
                      {
                        v: "private_ca",
                        label: "Private CA",
                        hint: "Server cert signed by an internal CA. Paste the PEM below; agents fetch & --cacert.",
                      },
                      {
                        v: "insecure",
                        label: "Skip verify",
                        hint: "Escape hatch — agents are told to use curl --insecure. Not recommended.",
                      },
                    ] as const
                  ).map((opt) => {
                    const active = field.state.value === opt.v;
                    return (
                      <button
                        key={opt.v}
                        type="button"
                        onClick={() => field.handleChange(opt.v)}
                        className={cn(
                          "text-left p-3 rounded-lg border transition-colors",
                          active
                            ? "border-primary bg-primary/5"
                            : "border-border hover:bg-accent",
                        )}
                      >
                        <p className="text-sm font-medium text-foreground">
                          {opt.label}
                        </p>
                        <p className="text-xs text-muted-foreground mt-1">
                          {opt.hint}
                        </p>
                      </button>
                    );
                  })}
                </div>
              </div>
              {field.state.value === "private_ca" && (
                <div className="space-y-1.5">
                  <label
                    className="text-sm font-medium text-foreground"
                    htmlFor="field-5257f27b-432"
                  >
                    CA bundle (PEM)
                  </label>
                  <form.Field name="registration.caBundle">
                    {(caField) => (
                      <Textarea
                        id="field-5257f27b-432"
                        value={caField.state.value}
                        onChange={(e) => caField.handleChange(e.target.value)}
                        onBlur={caField.handleBlur}
                        rows={10}
                        placeholder="-----BEGIN CERTIFICATE-----&#10;...&#10;-----END CERTIFICATE-----"
                      />
                    )}
                  </form.Field>
                  <p className="text-xs text-muted-foreground">
                    Served via{" "}
                    <code className="font-mono">
                      GET /api/v1/register/ca.crt
                    </code>
                    . Concatenate any intermediate certs.
                  </p>
                </div>
              )}
            </>
          )}
        </form.Field>
      </Section>

      <form.Subscribe selector={(s) => s.values}>
        {(values) => {
          const dirty = diffPlatformSettings(initial, values);
          const hasChanges = Object.keys(dirty).length > 0;
          return (
            <div className="flex items-center justify-between sticky bottom-4 z-10 rounded-xl border border-border bg-popover/80 backdrop-blur p-3 shadow-sm">
              <p className="text-xs text-muted-foreground">
                {hasChanges
                  ? `${Object.keys(dirty).length} unsaved change${Object.keys(dirty).length === 1 ? "" : "s"}`
                  : "No changes"}
              </p>
              <ActionButton
                type="button"
                intent="primary"
                onClick={() => void form.handleSubmit()}
                disabled={!hasChanges || save.isPending}
                loading={save.isPending}
                icon={<Save className="h-3.5 w-3.5" />}
              >
                Save changes
              </ActionButton>
            </div>
          );
        }}
      </form.Subscribe>
    </div>
  );
}

function humanTtlMinutes(minutes: number): string {
  return minutes >= 1440
    ? `${Math.round(minutes / 1440)}d`
    : minutes >= 60
      ? `${Math.round(minutes / 60)}h`
      : `${minutes}m`;
}

function PlatformSummary({ onEdit }: { onEdit: () => void }) {
  const { data: flat, isLoading } = usePlatformSettings();
  const g = useMemo<PlatformSettingsGrouped>(
    () => hydratePlatformSettings(flat ?? []),
    [flat],
  );
  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-32 rounded-xl border border-border bg-card">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  const features = Object.entries(g.features);
  const enabled = features.filter(([, on]) => on).length;
  return (
    <div className="rounded-xl border border-border bg-card p-6 space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-base font-semibold text-foreground">
            Current configuration
          </h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Branding, banners, feature flags, TTLs, telemetry.
          </p>
        </div>
        <ActionButton
          icon={<Pencil className="h-3.5 w-3.5" />}
          onClick={onEdit}
        >
          Edit settings
        </ActionButton>
      </div>
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-x-8 divide-y divide-border/60 sm:divide-y-0">
        <div className="flex items-center justify-between gap-4 py-1.5">
          <span className="text-xs text-muted-foreground">Product name</span>
          <span className="inline-flex items-center gap-2 text-sm text-foreground">
            <span
              className="h-3.5 w-3.5 rounded border border-border"
              style={{ backgroundColor: g.branding.primaryColor }}
            />
            {g.branding.productName}
          </span>
        </div>
        <div className="flex items-center justify-between gap-4 py-1.5">
          <span className="text-xs text-muted-foreground">Feature flags</span>
          <span className="text-sm text-foreground">
            {enabled}/{features.length} enabled
          </span>
        </div>
        <div className="flex items-center justify-between gap-4 py-1.5">
          <span className="text-xs text-muted-foreground">Token TTL</span>
          <span className="text-sm text-foreground font-mono">
            {g.tokens.defaultTtlMinutes === 0
              ? "no expiry"
              : humanTtlMinutes(g.tokens.defaultTtlMinutes)}{" "}
            · max {humanTtlMinutes(g.tokens.maxTtlMinutes)}
          </span>
        </div>
        <div className="flex items-center justify-between gap-4 py-1.5">
          <span className="text-xs text-muted-foreground">
            Session lifetime
          </span>
          <span className="text-sm text-foreground font-mono">
            {g.session.timeoutMinutes}m
          </span>
        </div>
        <div className="flex items-center justify-between gap-4 py-1.5">
          <span className="text-xs text-muted-foreground">Telemetry</span>
          <span className="text-sm text-foreground">
            {g.telemetry.enabled ? "Enabled" : "Disabled"}
          </span>
        </div>
        <div className="flex items-center justify-between gap-4 py-1.5">
          <span className="text-xs text-muted-foreground">
            Registration TLS
          </span>
          <span className="text-sm text-foreground font-mono">
            {g.registration.tlsMode}
          </span>
        </div>
      </div>
    </div>
  );
}

function PlatformSettingsPage() {
  const [editing, setEditing] = useState(false);
  return (
    <SettingsAuthGate>
      <PageShell>
        <Link
          href="/dashboard/settings"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Settings
        </Link>
        <PageHeader
          eyebrow="Settings · Platform"
          title="Platform settings"
          description="Branding, banners, feature flags, token TTL, telemetry. Changes apply across the dashboard."
        />
        <PlatformSummary onEdit={() => setEditing(true)} />
        <KeyStatusPanel />
      </PageShell>
      {editing && (
        <ModalShell
          title="Platform settings"
          subtitle="Branding, banners, feature flags, TTLs, telemetry."
          size="xl"
          onClose={() => setEditing(false)}
        >
          <PlatformSettingsForm onSaved={() => setEditing(false)} />
        </ModalShell>
      )}
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute("/dashboard/settings/platform/")({
  component: PlatformSettingsPage,
});
