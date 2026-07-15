import { createFileRoute } from '@tanstack/react-router';
/**
 * /dashboard/settings/auth/settings/ — singleton Dex settings.
 *
 * Lives under the auth/ sub-tree to keep all Dex UI co-located. Three sections:
 *   1. Issuer + cluster — edited as plain text fields. Changing the issuer
 *      requires re-applying for the change to land in the rendered ConfigMap.
 *   2. Public clients — list editor for Dex's `staticClients` array. Each
 *      client carries an id, optional secret, and redirect URIs. The `public`
 *      flag toggles between confidential and public OIDC client behaviour.
 *   3. Token expiry — three sliders/inputs that round-trip the Dex `expiry`
 *      block. We store the raw map verbatim so any future Dex fields land
 *      without a code change here.
 */
import { useEffect } from 'react';
import { Link } from '@/lib/link';
import { useAppForm, useStore } from '@/lib/form';
import { ArrowLeft, Loader2, Plus, Trash2 } from 'lucide-react';
import { useClusters } from '@/lib/hooks';
import { useDexSettings, useUpdateDexSettings, useApplyDexConfig } from '@/components/auth/hooks';
import type { DexPublicClient } from '@/types';
import { cn } from '@/lib/utils';

function DexSettingsPage() {
  const { data: settings, isLoading } = useDexSettings();
  const { data: clustersData } = useClusters({ pageSize: 100 });
  const clusters = clustersData?.data ?? [];

  const updateMutation = useUpdateDexSettings();
  const applyMutation = useApplyDexConfig();

  // Track form state separately from the query so unsaved edits don't snap
  // back when the cache refetches (we only rebase when the snapshot lands).
  const form = useAppForm({
    defaultValues: {
      issuer: '',
      clusterId: '',
      namespace: 'dex',
      releaseName: 'dex',
      configmapName: 'astronomer-dex-config',
      publicClients: [] as DexPublicClient[],
      idTokenExpiry: '24h',
      refreshTokenExpiry: '2160h',
      refreshIdle: '',
    },
    onSubmit: async ({ value }) => {
      const expiry: Record<string, unknown> = {};
      if (value.idTokenExpiry) expiry.idTokens = value.idTokenExpiry;
      if (value.refreshTokenExpiry || value.refreshIdle) {
        const rt: Record<string, unknown> = {};
        if (value.refreshTokenExpiry) rt.absoluteLifetime = value.refreshTokenExpiry;
        if (value.refreshIdle) rt.validIfNotUsedFor = value.refreshIdle;
        expiry.refreshTokens = rt;
      }
      await updateMutation.mutateAsync({
        issuer_url: value.issuer.trim(),
        cluster_id: value.clusterId || undefined,
        namespace: value.namespace,
        release_name: value.releaseName,
        configmap_name: value.configmapName,
        public_clients: value.publicClients,
        expiry,
        extra: (settings?.extra as Record<string, unknown>) ?? {},
      });
    },
  });
  // Old disabled gate (`!issuer.trim()`), recomputed from form state.
  const issuer = useStore(form.store, (s) => s.values.issuer);

  useEffect(() => {
    if (!settings) return;
    const expiry = (settings.expiry || {}) as Record<string, unknown>;
    let idTokenExpiry = '24h';
    let refreshTokenExpiry = '2160h';
    let refreshIdle = '';
    if (typeof expiry.idTokens === 'string') idTokenExpiry = expiry.idTokens;
    if (typeof expiry.refreshTokens === 'object' && expiry.refreshTokens !== null) {
      const rt = expiry.refreshTokens as Record<string, unknown>;
      if (typeof rt.absoluteLifetime === 'string') refreshTokenExpiry = rt.absoluteLifetime;
      if (typeof rt.validIfNotUsedFor === 'string') refreshIdle = rt.validIfNotUsedFor;
    } else if (typeof expiry.refreshTokens === 'string') {
      refreshTokenExpiry = expiry.refreshTokens;
    }
    form.reset({
      issuer: settings.issuerUrl,
      clusterId: settings.clusterId || '',
      namespace: settings.namespace || 'dex',
      releaseName: settings.releaseName || 'dex',
      configmapName: settings.configmapName || 'astronomer-dex-config',
      publicClients: Array.isArray(settings.publicClients) ? settings.publicClients : [],
      idTokenExpiry,
      refreshTokenExpiry,
      refreshIdle,
    });
  }, [form, settings]);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-48">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  return (
    <div className="max-w-3xl mx-auto space-y-6">
      <Link
        href="/dashboard/settings/auth"
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to Auth
      </Link>

      <div className="flex items-start justify-between gap-4">
        <div>
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            Auth · Dex Settings
          </p>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">
            Dex Top-level Settings
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            Issuer URL, public clients, token expiry. Changes are written to the rendered
            ConfigMap on Apply.
          </p>
        </div>
        <button
          type="button"
          onClick={() => applyMutation.mutate()}
          disabled={applyMutation.isPending}
          className="inline-flex items-center gap-2 h-9 px-3 rounded-lg border border-border text-sm
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
        >
          {applyMutation.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
          Apply to Dex
        </button>
      </div>

      {/* Section: Identity */}
      <Section title="Identity" description="Where Dex lives and what it calls itself.">
        <form.AppField name="issuer">
          {(field) => (
            <field.TextField
              label="Issuer URL"
              required
              helper="Must match the URL the OIDC RP redirects to."
              placeholder="https://dex.example.com"
            />
          )}
        </form.AppField>
        <form.AppField name="clusterId">
          {(field) => (
            <field.SelectField label="Target cluster" helper="Where the ConfigMap is written on Apply.">
              <option value="">— None —</option>
              {clusters.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.displayName || c.name}
                </option>
              ))}
            </field.SelectField>
          )}
        </form.AppField>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <form.AppField name="namespace">
            {(field) => <field.TextField label="Namespace" />}
          </form.AppField>
          <form.AppField name="releaseName">
            {(field) => <field.TextField label="Release name" />}
          </form.AppField>
          <form.AppField name="configmapName">
            {(field) => <field.TextField label="ConfigMap name" />}
          </form.AppField>
        </div>
      </Section>

      {/* Section: Public clients */}
      <Section
        title="Static / public clients"
        description="OIDC clients Dex will accept. The `astronomer` row is added automatically when you register Dex as SSO."
      >
        <form.Field name="publicClients">
          {(field) => (
            <div className="space-y-3">
              {field.state.value.length === 0 ? (
                <p className="text-xs text-muted-foreground">
                  No clients configured. Add one to allow OIDC RPs (Astronomer, Argo CD, etc.) to
                  authenticate.
                </p>
              ) : (
                field.state.value.map((client, i) => (
                  <PublicClientEditor
                    key={i}
                    value={client}
                    onChange={(next) => {
                      field.handleChange(field.state.value.map((c, idx) => (idx === i ? next : c)));
                    }}
                    onRemove={() =>
                      field.handleChange(field.state.value.filter((_, idx) => idx !== i))
                    }
                  />
                ))
              )}
              <button
                type="button"
                onClick={() =>
                  field.handleChange([
                    ...field.state.value,
                    { id: '', name: '', redirectURIs: [], public: false },
                  ])
                }
                className="inline-flex items-center gap-2 h-9 px-3 rounded-lg border border-dashed border-border text-sm
                  text-muted-foreground hover:text-foreground hover:border-foreground/30 transition-colors"
              >
                <Plus className="h-3.5 w-3.5" />
                Add client
              </button>
            </div>
          )}
        </form.Field>
      </Section>

      {/* Section: Token expiry */}
      <Section title="Token expiry" description="Forwarded into Dex's `expiry` block as-is.">
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <form.AppField name="idTokenExpiry">
            {(field) => <field.TextField label="ID token" helper="e.g. 24h" placeholder="24h" />}
          </form.AppField>
          <form.AppField name="refreshTokenExpiry">
            {(field) => (
              <field.TextField label="Refresh token (absolute)" helper="e.g. 2160h" placeholder="2160h" />
            )}
          </form.AppField>
          <form.AppField name="refreshIdle">
            {(field) => (
              <field.TextField label="Refresh idle timeout" helper="Optional; e.g. 168h" placeholder="168h" />
            )}
          </form.AppField>
        </div>
      </Section>

      <div className="flex items-center justify-end gap-2">
        <button
          type="button"
          onClick={() => void form.handleSubmit()}
          disabled={updateMutation.isPending || !issuer.trim()}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
            text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
        >
          {updateMutation.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
          Save settings
        </button>
      </div>
    </div>
  );
}

// ============================================================
// Helpers
// ============================================================

const inputCls =
  'w-full h-10 px-3 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring';

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
    <div className="rounded-xl border border-border bg-card p-5 space-y-4">
      <div>
        <h2 className="text-base font-semibold text-foreground">{title}</h2>
        {description && <p className="text-xs text-muted-foreground mt-1">{description}</p>}
      </div>
      <div className="space-y-4">{children}</div>
    </div>
  );
}

function FieldRow({
  label,
  helper,
  required,
  children,
}: {
  label: string;
  helper?: string;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground">
        {label}
        {required && <span className="text-status-error ml-0.5">*</span>}
      </label>
      {children}
      {helper && <p className="text-2xs text-muted-foreground">{helper}</p>}
    </div>
  );
}

function PublicClientEditor({
  value,
  onChange,
  onRemove,
}: {
  value: DexPublicClient;
  onChange: (next: DexPublicClient) => void;
  onRemove: () => void;
}) {
  const redirects = (value.redirectURIs ?? []).join(', ');
  return (
    <div className="rounded-lg border border-border bg-background p-3 space-y-3">
      <div className="flex items-start justify-between gap-2">
        <p className="text-xs font-medium text-foreground">
          {value.id ? value.id : 'New client'}{' '}
          {value.public && (
            <span className="ml-1 text-2xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground">
              public
            </span>
          )}
        </p>
        <button
          type="button"
          onClick={onRemove}
          className="text-muted-foreground hover:text-status-error transition-colors"
          title="Remove client"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <FieldRow label="Client ID" required>
          <input
            type="text"
            value={value.id}
            onChange={(e) => onChange({ ...value, id: e.target.value })}
            placeholder="astronomer"
            className={inputCls}
          />
        </FieldRow>
        <FieldRow label="Display name">
          <input
            type="text"
            value={value.name ?? ''}
            onChange={(e) => onChange({ ...value, name: e.target.value })}
            placeholder="Astronomer"
            className={inputCls}
          />
        </FieldRow>
      </div>
      <FieldRow label="Redirect URIs" helper="Comma-separated">
        <input
          type="text"
          value={redirects}
          onChange={(e) =>
            onChange({
              ...value,
              redirectURIs: e.target.value
                .split(',')
                .map((s) => s.trim())
                .filter(Boolean),
            })
          }
          placeholder="https://app.example.com/auth/callback"
          className={inputCls}
        />
      </FieldRow>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <FieldRow
          label="Client secret"
          helper={value.public ? 'Not used for public clients' : 'Required for confidential clients'}
        >
          <input
            type="password"
            value={value.secret ?? ''}
            onChange={(e) => onChange({ ...value, secret: e.target.value })}
            placeholder={value.public ? '—' : '••••••••'}
            disabled={!!value.public}
            className={cn(inputCls, value.public && 'opacity-50 cursor-not-allowed')}
          />
        </FieldRow>
        <FieldRow label="Public client?">
          <label className="inline-flex items-center gap-2 mt-1.5 cursor-pointer">
            <button
              type="button"
              onClick={() => onChange({ ...value, public: !value.public })}
              className={cn(
                'relative inline-flex h-6 w-11 items-center rounded-full transition-colors',
                value.public ? 'bg-status-success' : 'bg-muted'
              )}
            >
              <span
                className={cn(
                  'inline-block h-4 w-4 transform rounded-full bg-white transition-transform',
                  value.public ? 'translate-x-6' : 'translate-x-1'
                )}
              />
            </button>
            <span className="text-xs text-muted-foreground">
              {value.public ? 'Yes — no client secret' : 'No — confidential'}
            </span>
          </label>
        </FieldRow>
      </div>
    </div>
  );
}

export const Route = createFileRoute('/dashboard/settings/auth/settings/')({
  component: DexSettingsPage,
});
