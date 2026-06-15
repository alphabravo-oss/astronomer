'use client';

/**
 * /dashboard/settings/platform — branding, banners, feature flags, token
 * TTL, and telemetry. Each section maps to a stable dotted-key prefix on
 * the backend (`branding.*`, `banners.*`, `features.*`, ...); we hydrate a
 * flat key/value snapshot from `GET /admin/settings/`, mirror it into a
 * grouped form-state struct, and on save diff against the original to only
 * push keys that actually changed.
 */
import { useMemo, useState, useEffect } from 'react';
import Link from 'next/link';
import {
  ArrowLeft,
  Loader2,
  Save,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { toastInfo } from '@/lib/toast';
import {
  usePlatformSettings,
  useSavePlatformSettings,
} from '@/components/settings/hooks';
import type { PlatformSettingsGrouped } from '@/lib/api/settings';

// Defaults match the backend's seed values; we fall back to them when a key
// is absent from the snapshot so the form has stable initial state.
const DEFAULTS: PlatformSettingsGrouped = {
  branding: {
    logoUrl: '',
    productName: 'Astronomer',
    primaryColor: '#3b82f6',
    supportUrl: '',
    copyright: '',
  },
  banners: {
    loginBannerText: '',
    globalBannerText: '',
    globalBannerColor: 'info',
  },
  features: {
    catalog: true,
    projects: true,
    monitoring: true,
    argocd: true,
    security: true,
    backups: true,
  },
  tokens: {
    defaultTtlSeconds: 86400,
    maxTtlSeconds: 2592000,
  },
  telemetry: {
    enabled: false,
    endpoint: '',
  },
  registration: {
    tlsMode: 'public_ca',
    caBundle: '',
  },
};

const FLAT_KEYS: Record<string, (g: PlatformSettingsGrouped) => unknown> = {
  'branding.logo_url': (g) => g.branding.logoUrl,
  'branding.product_name': (g) => g.branding.productName,
  'branding.primary_color': (g) => g.branding.primaryColor,
  'branding.support_url': (g) => g.branding.supportUrl,
  'branding.copyright': (g) => g.branding.copyright,
  'banners.login_banner_text': (g) => g.banners.loginBannerText,
  'banners.global_banner_text': (g) => g.banners.globalBannerText,
  'banners.global_banner_color': (g) => g.banners.globalBannerColor,
  'features.catalog': (g) => g.features.catalog,
  'features.projects': (g) => g.features.projects,
  'features.monitoring': (g) => g.features.monitoring,
  'features.argocd': (g) => g.features.argocd,
  'features.security': (g) => g.features.security,
  'features.backups': (g) => g.features.backups,
  'tokens.default_ttl_seconds': (g) => g.tokens.defaultTtlSeconds,
  'tokens.max_ttl_seconds': (g) => g.tokens.maxTtlSeconds,
  'telemetry.enabled': (g) => g.telemetry.enabled,
  'telemetry.endpoint': (g) => g.telemetry.endpoint,
  'registration.tls_mode': (g) => g.registration.tlsMode,
  'registration.ca_bundle': (g) => g.registration.caBundle,
};

function hydrate(flat: Array<{ key: string; value: unknown }>): PlatformSettingsGrouped {
  const map = new Map(flat.map((s) => [s.key, s.value]));
  const get = <T,>(key: string, fallback: T): T => {
    const v = map.get(key);
    return v === undefined || v === null ? fallback : (v as T);
  };
  return {
    branding: {
      logoUrl: get('branding.logo_url', DEFAULTS.branding.logoUrl),
      productName: get('branding.product_name', DEFAULTS.branding.productName),
      primaryColor: get('branding.primary_color', DEFAULTS.branding.primaryColor),
      supportUrl: get('branding.support_url', DEFAULTS.branding.supportUrl),
      copyright: get('branding.copyright', DEFAULTS.branding.copyright),
    },
    banners: {
      loginBannerText: get('banners.login_banner_text', DEFAULTS.banners.loginBannerText),
      globalBannerText: get('banners.global_banner_text', DEFAULTS.banners.globalBannerText),
      globalBannerColor: get('banners.global_banner_color', DEFAULTS.banners.globalBannerColor),
    },
    features: {
      catalog: get('features.catalog', DEFAULTS.features.catalog),
      projects: get('features.projects', DEFAULTS.features.projects),
      monitoring: get('features.monitoring', DEFAULTS.features.monitoring),
      argocd: get('features.argocd', DEFAULTS.features.argocd),
      security: get('features.security', DEFAULTS.features.security),
      backups: get('features.backups', DEFAULTS.features.backups),
    },
    tokens: {
      defaultTtlSeconds: get('tokens.default_ttl_seconds', DEFAULTS.tokens.defaultTtlSeconds),
      maxTtlSeconds: get('tokens.max_ttl_seconds', DEFAULTS.tokens.maxTtlSeconds),
    },
    telemetry: {
      enabled: get('telemetry.enabled', DEFAULTS.telemetry.enabled),
      endpoint: get('telemetry.endpoint', DEFAULTS.telemetry.endpoint),
    },
    registration: {
      tlsMode: get('registration.tls_mode', DEFAULTS.registration.tlsMode) as PlatformSettingsGrouped['registration']['tlsMode'],
      caBundle: get('registration.ca_bundle', DEFAULTS.registration.caBundle),
    },
  };
}

function diffKeys(a: PlatformSettingsGrouped, b: PlatformSettingsGrouped): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [flat, getter] of Object.entries(FLAT_KEYS)) {
    const left = getter(a);
    const right = getter(b);
    if (left !== right) out[flat] = right;
  }
  return out;
}

function Toggle({
  value,
  onChange,
  label,
  hint,
}: {
  value: boolean;
  onChange: (v: boolean) => void;
  label: string;
  hint?: string;
}) {
  return (
    <div className="flex items-center justify-between p-3 rounded-lg border border-border">
      <div>
        <p className="text-sm font-medium text-foreground">{label}</p>
        {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
      </div>
      <button
        type="button"
        onClick={() => onChange(!value)}
        className={cn(
          'relative inline-flex h-6 w-11 items-center rounded-full transition-colors',
          value ? 'bg-status-success' : 'bg-muted',
        )}
      >
        <span
          className={cn(
            'inline-block h-4 w-4 transform rounded-full bg-white transition-transform',
            value ? 'translate-x-6' : 'translate-x-1',
          )}
        />
      </button>
    </div>
  );
}

function TextField({
  label,
  value,
  onChange,
  placeholder,
  type = 'text',
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  type?: string;
}) {
  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground">{label}</label>
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
      />
    </div>
  );
}

function NumberField({
  label,
  value,
  onChange,
  min,
}: {
  label: string;
  value: number;
  onChange: (v: number) => void;
  min?: number;
}) {
  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground">{label}</label>
      <input
        type="number"
        value={value}
        min={min}
        onChange={(e) => onChange(Number(e.target.value))}
        className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
      />
    </div>
  );
}

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
        {description && <p className="text-xs text-muted-foreground mt-0.5">{description}</p>}
      </div>
      <div className="space-y-4">{children}</div>
    </section>
  );
}

function BannerPreview({ text, color }: { text: string; color: PlatformSettingsGrouped['banners']['globalBannerColor'] }) {
  if (!text) {
    return <p className="text-xs text-muted-foreground italic">No banner — leave blank to hide.</p>;
  }
  const palette: Record<typeof color, string> = {
    info: 'bg-blue-500/10 border-blue-500/30 text-blue-600 dark:text-blue-400',
    success: 'bg-emerald-500/10 border-emerald-500/30 text-emerald-600 dark:text-emerald-400',
    warning: 'bg-amber-500/10 border-amber-500/30 text-amber-600 dark:text-amber-400',
    error: 'bg-rose-500/10 border-rose-500/30 text-rose-600 dark:text-rose-400',
  };
  return (
    <div className={cn('rounded-lg border px-3 py-2 text-xs whitespace-pre-wrap', palette[color])}>
      {text}
    </div>
  );
}

function PlatformSettingsForm() {
  const { data: flat, isLoading } = usePlatformSettings();
  const save = useSavePlatformSettings();

  const initial = useMemo<PlatformSettingsGrouped>(() => hydrate(flat ?? []), [flat]);
  const [form, setForm] = useState<PlatformSettingsGrouped>(initial);

  // Re-hydrate the form whenever the snapshot lands. We do this once on load
  // and again after a save invalidates the query, which keeps the form in
  // sync with whatever the backend ultimately stored.
  useEffect(() => {
    setForm(initial);
  }, [initial]);

  const dirty = useMemo(() => diffKeys(initial, form), [initial, form]);
  const hasChanges = Object.keys(dirty).length > 0;

  const handleSave = async () => {
    if (!hasChanges) {
      toastInfo('Nothing to save');
      return;
    }
    try {
      await save.mutateAsync(dirty);
    } catch {
      // Toast handled by mutation.
    }
  };

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-48">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <Section title="Branding" description="Logo, product name, colors. Applied across the dashboard chrome.">
        <TextField label="Product name" value={form.branding.productName} onChange={(v) => setForm({ ...form, branding: { ...form.branding, productName: v } })} />
        <TextField label="Logo URL" value={form.branding.logoUrl} onChange={(v) => setForm({ ...form, branding: { ...form.branding, logoUrl: v } })} placeholder="https://..." />
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Primary color</label>
          <div className="flex items-center gap-3">
            <input
              type="text"
              value={form.branding.primaryColor}
              onChange={(e) => setForm({ ...form, branding: { ...form.branding, primaryColor: e.target.value } })}
              placeholder="#3b82f6"
              className="flex-1 h-10 px-3 rounded-lg border border-border bg-background text-sm font-mono focus:outline-none focus:ring-2 focus:ring-ring"
            />
            <div
              className="w-10 h-10 rounded-lg border border-border"
              style={{ backgroundColor: form.branding.primaryColor }}
              title={form.branding.primaryColor}
            />
          </div>
          <p className="text-xs text-muted-foreground">Hex string (e.g. <span className="font-mono">#3b82f6</span>).</p>
        </div>
        <TextField label="Support URL" value={form.branding.supportUrl} onChange={(v) => setForm({ ...form, branding: { ...form.branding, supportUrl: v } })} placeholder="https://help.example.com" />
        <TextField label="Copyright" value={form.branding.copyright} onChange={(v) => setForm({ ...form, branding: { ...form.branding, copyright: v } })} placeholder="© 2026 Example Corp." />
      </Section>

      <Section title="Banners" description="Optional banner text shown on the login screen and inside the dashboard.">
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Login banner</label>
          <textarea
            value={form.banners.loginBannerText}
            onChange={(e) => setForm({ ...form, banners: { ...form.banners, loginBannerText: e.target.value } })}
            placeholder="Authorized access only — your session is recorded."
            rows={3}
            className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
          />
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Global banner</label>
          <textarea
            value={form.banners.globalBannerText}
            onChange={(e) => setForm({ ...form, banners: { ...form.banners, globalBannerText: e.target.value } })}
            placeholder="Maintenance window 18:00 UTC tonight."
            rows={3}
            className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
          />
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Global banner color</label>
          <select
            value={form.banners.globalBannerColor}
            onChange={(e) =>
              setForm({ ...form, banners: { ...form.banners, globalBannerColor: e.target.value as PlatformSettingsGrouped['banners']['globalBannerColor'] } })
            }
            className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
          >
            <option value="info">Info (blue)</option>
            <option value="success">Success (green)</option>
            <option value="warning">Warning (amber)</option>
            <option value="error">Error (red)</option>
          </select>
        </div>
        <div className="space-y-1.5">
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Preview</p>
          <BannerPreview text={form.banners.globalBannerText} color={form.banners.globalBannerColor} />
        </div>
      </Section>

      <Section title="Feature flags" description="Hide entire dashboard areas from the sidebar. Server-side authorisation still applies regardless.">
        <Toggle value={form.features.catalog} onChange={(v) => setForm({ ...form, features: { ...form.features, catalog: v } })} label="Catalog" />
        <Toggle value={form.features.projects} onChange={(v) => setForm({ ...form, features: { ...form.features, projects: v } })} label="Projects" />
        <Toggle value={form.features.monitoring} onChange={(v) => setForm({ ...form, features: { ...form.features, monitoring: v } })} label="Monitoring" />
        <Toggle value={form.features.argocd} onChange={(v) => setForm({ ...form, features: { ...form.features, argocd: v } })} label="ArgoCD" />
        <Toggle value={form.features.security} onChange={(v) => setForm({ ...form, features: { ...form.features, security: v } })} label="Security" />
        <Toggle value={form.features.backups} onChange={(v) => setForm({ ...form, features: { ...form.features, backups: v } })} label="Backups" />
      </Section>

      <Section title="Token TTL" description="Defaults applied to newly minted API tokens.">
        <NumberField label="Default TTL (seconds)" value={form.tokens.defaultTtlSeconds} onChange={(v) => setForm({ ...form, tokens: { ...form.tokens, defaultTtlSeconds: v } })} min={60} />
        <NumberField label="Maximum TTL (seconds)" value={form.tokens.maxTtlSeconds} onChange={(v) => setForm({ ...form, tokens: { ...form.tokens, maxTtlSeconds: v } })} min={60} />
        <p className="text-xs text-muted-foreground">
          {form.tokens.defaultTtlSeconds >= 86400
            ? `Default ≈ ${Math.round(form.tokens.defaultTtlSeconds / 86400)} day(s)`
            : `Default ≈ ${Math.round(form.tokens.defaultTtlSeconds / 3600)} hour(s)`}
          {' · '}
          {form.tokens.maxTtlSeconds >= 86400
            ? `Max ≈ ${Math.round(form.tokens.maxTtlSeconds / 86400)} day(s)`
            : `Max ≈ ${Math.round(form.tokens.maxTtlSeconds / 3600)} hour(s)`}
        </p>
      </Section>

      <Section title="Telemetry" description="Anonymous usage signals. Opt-in only.">
        <Toggle
          value={form.telemetry.enabled}
          onChange={(v) => setForm({ ...form, telemetry: { ...form.telemetry, enabled: v } })}
          label="Enable telemetry"
          hint="Sends platform version + cluster count to the endpoint below."
        />
        <TextField
          label="Endpoint URL"
          value={form.telemetry.endpoint}
          onChange={(v) => setForm({ ...form, telemetry: { ...form.telemetry, endpoint: v } })}
          placeholder="https://telemetry.example.com/v1/ingest"
        />
      </Section>

      <Section
        title="Cluster registration TLS"
        description="Controls which curl variant the cluster-registration wizard shows by default and whether the public /api/v1/register/ca.crt endpoint serves a CA bundle."
      >
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">TLS posture</label>
          <div className="grid grid-cols-1 md:grid-cols-3 gap-2">
            {(
              [
                { v: 'public_ca', label: 'Public CA', hint: 'Server certificate signed by a publicly-trusted CA. curl works with no flags.' },
                { v: 'private_ca', label: 'Private CA', hint: 'Server cert signed by an internal CA. Paste the PEM below; agents fetch & --cacert.' },
                { v: 'insecure', label: 'Skip verify', hint: 'Escape hatch — agents are told to use curl --insecure. Not recommended.' },
              ] as const
            ).map((opt) => {
              const active = form.registration.tlsMode === opt.v;
              return (
                <button
                  key={opt.v}
                  type="button"
                  onClick={() =>
                    setForm({
                      ...form,
                      registration: { ...form.registration, tlsMode: opt.v },
                    })
                  }
                  className={cn(
                    'text-left p-3 rounded-lg border transition-colors',
                    active ? 'border-primary bg-primary/5' : 'border-border hover:bg-accent',
                  )}
                >
                  <p className="text-sm font-medium text-foreground">{opt.label}</p>
                  <p className="text-xs text-muted-foreground mt-1">{opt.hint}</p>
                </button>
              );
            })}
          </div>
        </div>
        {form.registration.tlsMode === 'private_ca' && (
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">CA bundle (PEM)</label>
            <textarea
              value={form.registration.caBundle}
              onChange={(e) =>
                setForm({
                  ...form,
                  registration: { ...form.registration, caBundle: e.target.value },
                })
              }
              rows={10}
              placeholder="-----BEGIN CERTIFICATE-----&#10;...&#10;-----END CERTIFICATE-----"
              className="w-full px-3 py-2 rounded-lg border border-border bg-background text-xs font-mono placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
            />
            <p className="text-xs text-muted-foreground">
              Served via <code className="font-mono">GET /api/v1/register/ca.crt</code>. Concatenate any intermediate certs.
            </p>
          </div>
        )}
      </Section>

      <div className="flex items-center justify-between sticky bottom-4 z-10 rounded-xl border border-border bg-popover/80 backdrop-blur p-3 shadow-sm">
        <p className="text-xs text-muted-foreground">
          {hasChanges
            ? `${Object.keys(dirty).length} unsaved change${Object.keys(dirty).length === 1 ? '' : 's'}`
            : 'No changes'}
        </p>
        <button
          type="button"
          onClick={handleSave}
          disabled={!hasChanges || save.isPending}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
        >
          {save.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}
          Save changes
        </button>
      </div>
    </div>
  );
}

export default function PlatformSettingsPage() {
  return (
    <SettingsAuthGate>
      <div className="max-w-3xl mx-auto space-y-6">
        <Link
          href="/dashboard/settings"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Settings
        </Link>
        <div>
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Settings · Platform</p>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">Platform settings</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Branding, banners, feature flags, token TTL, telemetry. Changes apply across the dashboard.
          </p>
        </div>
        <PlatformSettingsForm />
      </div>
    </SettingsAuthGate>
  );
}
