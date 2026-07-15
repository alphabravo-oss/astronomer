import { createFileRoute } from '@tanstack/react-router';
/**
 * /dashboard/settings/auth/install/ — Dex install wizard.
 *
 * Three-step flow:
 *   1. Pick the target cluster (defaults to the first registered cluster
 *      since most setups run a single management cluster).
 *   2. Choose the issuer URL. We auto-suggest one based on the cluster's
 *      labels/annotations (`astronomer.io/dex-issuer-url`,
 *      `astronomer.io/external-url`) when present, and otherwise build one
 *      from the gateway hostname pattern.
 *   3. Review + save the bundled Dex settings. Dex is deployed only by the
 *      Astronomer management Helm chart; this page never installs the
 *      unrelated upstream chart through the remote tools catalog.
 */
import { useEffect, useMemo, useState } from 'react';
import { Link } from '@/lib/link';
import { useRouter } from '@/lib/navigation';
import { ArrowLeft, ArrowRight, Check, Loader2, Server, Globe } from 'lucide-react';
import { useClusters } from '@/lib/hooks';
import { useAppForm, useStore } from '@/lib/form';
import { useUpdateDexSettings } from '@/components/auth/hooks';
import { cn } from '@/lib/utils';
import type { Cluster } from '@/types';

type Step = 1 | 2 | 3;

function InstallDexPage() {
  const router = useRouter();
  const { data: clustersData, isLoading: clustersLoading } = useClusters({ pageSize: 100 });
  const clusters = useMemo(() => clustersData?.data ?? [], [clustersData]);

  const settingsMutation = useUpdateDexSettings();

  const [step, setStep] = useState<Step>(1);

  // Wizard state (cluster pick + issuer URL) lives on a TanStack form; the
  // step counter stays local UI state.
  const form = useAppForm({
    defaultValues: { clusterId: '', issuerUrl: '' },
    onSubmit: async ({ value }) => {
      const cluster = clusters.find((c) => c.id === value.clusterId);
      if (!cluster) return;
      try {
        // Persist the issuer + cluster identity for the in-band Dex Deployment
        // rendered by the management chart. The backend rejects remote catalog
        // installation for slug `dex` so these two deployment models cannot mix.
        await settingsMutation.mutateAsync({
          issuer_url: value.issuerUrl,
          cluster_id: cluster.id,
        });
        router.push('/dashboard/settings/auth/');
      } catch {
        /* mutation toasts on error */
      }
    },
  });
  const clusterId = useStore(form.store, (s) => s.values.clusterId);
  const issuerUrl = useStore(form.store, (s) => s.values.issuerUrl);

  const cluster = clusters.find((c) => c.id === clusterId);

  // Default cluster + issuer suggestion as soon as data lands.
  useEffect(() => {
    if (!clusterId && clusters.length > 0) {
      form.setFieldValue('clusterId', clusters[0].id);
    }
  }, [form, clusters, clusterId]);

  useEffect(() => {
    if (cluster && !issuerUrl) {
      form.setFieldValue('issuerUrl', suggestIssuerUrl(cluster));
    }
  }, [form, cluster, issuerUrl]);

  // Old per-step advance gate, ported 1:1 (cluster picked; issuer URL valid).
  const canAdvance =
    step === 1 ? !!clusterId : step === 2 ? isValidIssuerUrl(issuerUrl) : true;

  const installing = settingsMutation.isPending;

  return (
    <div className="max-w-3xl mx-auto space-y-6">
      <BackLink />

      <div>
        <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
          Auth · Install
        </p>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">Install Dex</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Dex is bundled with the Astronomer management chart. Enable <span className="font-mono">dex.enabled</span>
          in Helm first; this workflow only binds its issuer and target cluster. Kubernetes runtime identity stays chart-owned.
        </p>
      </div>

      <Stepper step={step} />

      <div className="rounded-xl border border-border bg-card p-6 space-y-5">
        {step === 1 && (
          <form.Field name="clusterId">
            {(field) => (
              <ClusterPicker
                clusters={clusters}
                loading={clustersLoading}
                value={field.state.value}
                onChange={field.handleChange}
              />
            )}
          </form.Field>
        )}
        {step === 2 && (
          <form.Field name="issuerUrl">
            {(field) => (
              <IssuerStep
                cluster={cluster}
                value={field.state.value}
                onChange={field.handleChange}
              />
            )}
          </form.Field>
        )}
        {step === 3 && cluster && (
          <ReviewStep cluster={cluster} issuerUrl={issuerUrl} />
        )}

        {/* Footer */}
        <div className="flex items-center justify-between pt-4 border-t border-border">
          <button
            type="button"
            onClick={() => setStep((s) => (s > 1 ? ((s - 1) as Step) : s))}
            disabled={step === 1}
            className="inline-flex items-center gap-2 h-9 px-3 rounded-lg text-sm
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors
              disabled:opacity-30 disabled:cursor-not-allowed"
          >
            <ArrowLeft className="h-3.5 w-3.5" />
            Back
          </button>
          {step < 3 ? (
            <button
              type="button"
              onClick={() => setStep((s) => ((s + 1) as Step))}
              disabled={!canAdvance}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
            >
              Continue
              <ArrowRight className="h-3.5 w-3.5" />
            </button>
          ) : (
            <button
              type="button"
              onClick={() => void form.handleSubmit()}
              disabled={installing}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
            >
              {installing && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              Save bundled Dex settings
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

// ============================================================
// Steps
// ============================================================

function BackLink() {
  return (
    <Link
      href="/dashboard/settings/auth"
      className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
    >
      <ArrowLeft className="h-3.5 w-3.5" />
      Back to Auth
    </Link>
  );
}

function Stepper({ step }: { step: Step }) {
  const steps = [
    { n: 1, label: 'Cluster' },
    { n: 2, label: 'Issuer' },
    { n: 3, label: 'Review' },
  ] as const;
  return (
    <div className="flex items-center gap-3">
      {steps.map((s, i) => {
        const active = step === s.n;
        const done = step > s.n;
        return (
          <div key={s.n} className="flex items-center gap-3">
            <div className="flex items-center gap-2">
              <span
                className={cn(
                  'flex items-center justify-center h-6 w-6 rounded-full text-xs font-medium',
                  active && 'bg-primary text-primary-foreground',
                  done && 'bg-status-success text-white',
                  !active && !done && 'bg-muted text-muted-foreground'
                )}
              >
                {done ? <Check className="h-3.5 w-3.5" /> : s.n}
              </span>
              <span
                className={cn(
                  'text-sm',
                  active ? 'text-foreground font-medium' : 'text-muted-foreground'
                )}
              >
                {s.label}
              </span>
            </div>
            {i < steps.length - 1 && <div className="w-8 h-px bg-border" />}
          </div>
        );
      })}
    </div>
  );
}

function ClusterPicker({
  clusters,
  loading,
  value,
  onChange,
}: {
  clusters: Cluster[];
  loading: boolean;
  value: string;
  onChange: (id: string) => void;
}) {
  if (loading) {
    return (
      <div className="flex items-center justify-center h-32">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (clusters.length === 0) {
    return (
      <div className="text-center py-10">
        <Server className="h-8 w-8 mx-auto text-muted-foreground" />
        <p className="text-sm text-foreground mt-3">No clusters registered yet.</p>
        <p className="text-xs text-muted-foreground mt-1">
          Register a cluster first — Dex needs somewhere to live.
        </p>
        <Link
          href="/dashboard/clusters/register"
          className="inline-flex items-center gap-2 h-8 px-3 mt-4 rounded-lg bg-primary text-primary-foreground text-xs font-medium hover:opacity-90 transition-opacity"
        >
          Register Cluster
        </Link>
      </div>
    );
  }
  return (
    <div className="space-y-3">
      <div>
        <p className="text-sm font-medium text-foreground">Choose a cluster</p>
        <p className="text-xs text-muted-foreground mt-0.5">
          Most setups deploy Dex on the management cluster.
        </p>
      </div>
      <div className="space-y-2">
        {clusters.map((c) => {
          const active = value === c.id;
          return (
            <button
              type="button"
              key={c.id}
              onClick={() => onChange(c.id)}
              className={cn(
                'w-full flex items-center justify-between px-4 py-3 rounded-lg border text-left transition-colors',
                active
                  ? 'border-primary bg-primary/5'
                  : 'border-border bg-background hover:bg-accent/30'
              )}
            >
              <div className="flex items-center gap-3 min-w-0">
                <Server className="h-4 w-4 text-muted-foreground flex-shrink-0" />
                <div className="min-w-0">
                  <p className="text-sm font-medium text-foreground truncate">
                    {c.displayName || c.name}
                  </p>
                  <p className="text-xs text-muted-foreground truncate">
                    {c.environment} · {c.kubernetesVersion || c.distribution}
                  </p>
                </div>
              </div>
              {active && <Check className="h-4 w-4 text-primary flex-shrink-0" />}
            </button>
          );
        })}
      </div>
    </div>
  );
}

function IssuerStep({
  cluster,
  value,
  onChange,
}: {
  cluster?: Cluster;
  value: string;
  onChange: (v: string) => void;
}) {
  const suggestions = useMemo(() => {
    if (!cluster) return [];
    const candidates = new Set<string>();
    candidates.add(suggestIssuerUrl(cluster));
    // A handful of common patterns operators tend to use.
    candidates.add(`https://dex.${cluster.name}.cluster.local`);
    if (typeof window !== 'undefined') {
      candidates.add(`${window.location.origin.replace(/^https?:\/\//, 'https://dex.')}`);
    }
    return Array.from(candidates).filter(Boolean).slice(0, 3);
  }, [cluster]);

  const valid = isValidIssuerUrl(value);

  return (
    <div className="space-y-3">
      <div>
        <p className="text-sm font-medium text-foreground">Issuer URL</p>
        <p className="text-xs text-muted-foreground mt-0.5">
          Public URL where Dex will serve OIDC discovery. This must match the URL the
          login flow redirects to.
        </p>
      </div>
      <div className="space-y-1.5">
        <div className="flex items-center gap-2 px-3 rounded-lg border border-border bg-background">
          <Globe className="h-4 w-4 text-muted-foreground" />
          <input
            type="text"
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder="https://dex.example.com"
            className="flex-1 h-10 bg-transparent text-sm placeholder:text-muted-foreground focus:outline-none"
          />
        </div>
        {!valid && value && (
          <p className="text-xs text-status-error">Issuer must be a valid https:// URL</p>
        )}
      </div>
      {suggestions.length > 0 && (
        <div className="space-y-1.5">
          <p className="text-xs text-muted-foreground">Suggestions</p>
          <div className="flex flex-wrap gap-2">
            {suggestions.map((s) => (
              <button
                type="button"
                key={s}
                onClick={() => onChange(s)}
                className="inline-flex items-center px-2.5 py-1 rounded-md border border-border text-xs
                  text-muted-foreground hover:text-foreground hover:bg-accent transition-colors font-mono"
              >
                {s}
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function ReviewStep({
  cluster,
  issuerUrl,
}: {
  cluster: Cluster;
  issuerUrl: string;
}) {
  return (
    <div className="space-y-4">
      <div>
        <p className="text-sm font-medium text-foreground">Review</p>
        <p className="text-xs text-muted-foreground mt-0.5">
          We&apos;ll bind the chart-managed Dex Deployment to its singleton settings row.
        </p>
      </div>
      <dl className="rounded-lg border border-border divide-y divide-border">
        <ReviewRow label="Tool" value="dex" mono />
        <ReviewRow label="Cluster" value={cluster.displayName || cluster.name} />
        <ReviewRow label="Environment" value={cluster.environment} />
        <ReviewRow label="Issuer URL" value={issuerUrl} mono />
        <ReviewRow label="Namespace" value="dex" mono />
        <ReviewRow label="Runtime Secret" value="astronomer-dex-runtime" mono />
      </dl>
    </div>
  );
}

function ReviewRow({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="flex items-center justify-between px-4 py-3 text-sm">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={cn('text-foreground', mono && 'font-mono text-xs')}>{value}</dd>
    </div>
  );
}

// ============================================================
// Helpers
// ============================================================

function suggestIssuerUrl(cluster: Cluster): string {
  // Operators frequently stamp this label/annotation onto management clusters
  // so the dex install can pick it up automatically.
  const labelHint =
    cluster.labels?.['astronomer.io/dex-issuer-url'] ??
    cluster.annotations?.['astronomer.io/dex-issuer-url'];
  if (labelHint) return labelHint;
  const externalUrl =
    cluster.labels?.['astronomer.io/external-url'] ??
    cluster.annotations?.['astronomer.io/external-url'];
  if (externalUrl) {
    return `${externalUrl.replace(/\/+$/, '')}/dex`;
  }
  if (typeof window !== 'undefined') {
    return `${window.location.origin}/dex`;
  }
  return `https://dex.${cluster.name}.example.com`;
}

function isValidIssuerUrl(s: string): boolean {
  if (!s) return false;
  try {
    const u = new URL(s);
    return u.protocol === 'https:' || u.protocol === 'http:';
  } catch {
    return false;
  }
}

export const Route = createFileRoute('/dashboard/settings/auth/install/')({
  component: InstallDexPage,
});
