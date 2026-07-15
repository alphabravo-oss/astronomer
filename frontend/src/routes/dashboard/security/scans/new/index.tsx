import { createFileRoute } from '@tanstack/react-router';
import { useEffect, useMemo, useState } from 'react';
import { useRouter } from '@/lib/navigation';
import { Link } from '@/lib/link';
import { useClusters } from '@/lib/hooks';
import { useCISProfiles, useCreateCISScan } from '@/lib/hooks';
import { CIS_NOT_INSTALLED_HINT } from '@/components/security/cis-scans-tab';
import { distributionDisplayName, cn } from '@/lib/utils';
import {
  ArrowLeft,
  ArrowRight,
  ChevronRight,
  CheckCircle2,
  Loader2,
  ShieldAlert,
  ShieldCheck,
  AlertTriangle,
} from 'lucide-react';

/**
 * Phase B5 — CIS scan wizard.
 *
 * Three-step linear flow:
 *   1. Pick a cluster.
 *   2. Pick a profile (default-selected from the operator's recommendation
 *      for the cluster's distribution).
 *   3. Review + run.
 *
 * Profile picker uses the live `/security/profiles/?cluster_id=…` endpoint
 * which falls back to a static list when the cis-operator isn't installed.
 * In that case we surface a hint banner so the user can decide whether to
 * deploy it before scanning.
 */
type WizardStep = 1 | 2 | 3;

const STEPS: { n: WizardStep; label: string }[] = [
  { n: 1, label: 'Cluster' },
  { n: 2, label: 'Profile' },
  { n: 3, label: 'Review' },
];

function NewScanWizardPage() {
  const router = useRouter();
  const [step, setStep] = useState<WizardStep>(1);
  const [clusterId, setClusterId] = useState<string>('');
  const [profile, setProfile] = useState<string>('');

  const { data: clustersPage, isLoading: clustersLoading } = useClusters({ pageSize: 200 });
  const { data: profilesData, isLoading: profilesLoading } = useCISProfiles(clusterId || undefined);
  const createScan = useCreateCISScan();

  const cluster = useMemo(
    () => clustersPage?.data.find((c) => c.id === clusterId),
    [clustersPage, clusterId],
  );

  // Pre-select the recommended profile whenever the cluster (or its profile
  // list) changes — but never overwrite an explicit user choice.
  const recommendedName = useMemo(() => {
    if (!cluster) return undefined;
    return defaultProfileForDistribution(cluster.distribution || '');
  }, [cluster]);

  useEffect(() => {
    if (!profilesData) return;
    const items = profilesData.items ?? [];
    if (items.length === 0) return;
    if (profile && items.some((p) => p.name === profile)) return;
    const recommended = items.find((p) => p.name === recommendedName);
    setProfile((recommended ?? items[0]).name);
  }, [profilesData, recommendedName, profile]);

  const canAdvance = step === 1 ? !!clusterId : step === 2 ? !!profile : true;

  async function handleSubmit() {
    if (!clusterId) return;
    const scan = await createScan.mutateAsync({
      cluster_id: clusterId,
      profile: profile || undefined,
    });
    router.push(`/dashboard/security/scans/${scan.id}`);
  }

  return (
    <div className="space-y-6 max-w-3xl">
      {/* Breadcrumb */}
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Link href="/dashboard/security" className="hover:text-foreground transition-colors">
          Security
        </Link>
        <ChevronRight className="h-3.5 w-3.5" />
        <span className="text-foreground">New CIS Scan</span>
      </div>

      <div>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight">Run CIS Scan</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Run a CIS Kubernetes Benchmark via the cis-operator on a registered cluster.
        </p>
      </div>

      {/* Step indicator */}
      <ol className="flex items-center gap-2">
        {STEPS.map((s, i) => {
          const active = step === s.n;
          const complete = step > s.n;
          return (
            <li key={s.n} className="flex items-center gap-2">
              <div
                className={cn(
                  'flex items-center gap-2 px-3 py-1.5 rounded-full text-xs font-medium transition-colors',
                  active && 'bg-primary text-primary-foreground',
                  complete && 'bg-status-success/10 text-status-success',
                  !active && !complete && 'bg-muted text-muted-foreground',
                )}
              >
                {complete ? (
                  <CheckCircle2 className="h-3.5 w-3.5" />
                ) : (
                  <span className="tabular-nums">{s.n}</span>
                )}
                {s.label}
              </div>
              {i < STEPS.length - 1 && <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />}
            </li>
          );
        })}
      </ol>

      <div className="rounded-xl border border-border bg-card p-6">
        {step === 1 && (
          <div className="space-y-4">
            <div>
              <h2 className="text-sm font-medium text-foreground">Select cluster</h2>
              <p className="text-xs text-muted-foreground mt-1">
                The scan runs on the cis-operator deployed to this cluster's tunnel-managed agent.
              </p>
            </div>
            {clustersLoading ? (
              <div className="space-y-2">
                {Array.from({ length: 3 }).map((_, i) => (
                  <div key={i} className="h-12 rounded-md bg-muted animate-pulse" />
                ))}
              </div>
            ) : (
              <div className="space-y-1.5">
                {(clustersPage?.data ?? []).map((c) => (
                  <button
                    key={c.id}
                    type="button"
                    onClick={() => setClusterId(c.id)}
                    className={cn(
                      'w-full flex items-center justify-between rounded-md border px-4 py-3 text-left transition-colors',
                      clusterId === c.id
                        ? 'border-primary bg-primary/5'
                        : 'border-border hover:bg-accent',
                    )}
                  >
                    <div className="min-w-0">
                      <p className="text-sm font-medium text-foreground truncate">
                        {c.displayName || c.name}
                      </p>
                      <p className="text-xs text-muted-foreground mt-0.5 truncate">
                        {distributionDisplayName(c.distribution)} · {c.environment} · {c.status}
                      </p>
                    </div>
                    {clusterId === c.id && <CheckCircle2 className="h-4 w-4 text-primary" />}
                  </button>
                ))}
                {(clustersPage?.data ?? []).length === 0 && (
                  <p className="text-sm text-muted-foreground py-8 text-center">
                    No clusters registered. Register a cluster first.
                  </p>
                )}
              </div>
            )}
          </div>
        )}

        {step === 2 && (
          <div className="space-y-4">
            <div>
              <h2 className="text-sm font-medium text-foreground">Select profile</h2>
              <p className="text-xs text-muted-foreground mt-1">
                Profiles ship preinstalled with cis-operator. The recommended one is highlighted.
              </p>
            </div>

            {profilesData?.source === 'fallback' && (
              <div className="rounded-md border border-status-warning/30 bg-status-warning/5 p-3 flex items-start gap-2.5">
                <AlertTriangle className="h-4 w-4 text-status-warning flex-shrink-0 mt-0.5" />
                <p className="text-xs text-status-warning">{CIS_NOT_INSTALLED_HINT}</p>
              </div>
            )}

            {profilesLoading ? (
              <div className="space-y-2">
                {Array.from({ length: 3 }).map((_, i) => (
                  <div key={i} className="h-12 rounded-md bg-muted animate-pulse" />
                ))}
              </div>
            ) : (
              <div className="space-y-1.5">
                {(profilesData?.items ?? []).map((p) => {
                  const recommended = p.name === recommendedName;
                  return (
                    <button
                      key={p.name}
                      type="button"
                      onClick={() => setProfile(p.name)}
                      className={cn(
                        'w-full flex items-center justify-between rounded-md border px-4 py-3 text-left transition-colors',
                        profile === p.name
                          ? 'border-primary bg-primary/5'
                          : 'border-border hover:bg-accent',
                      )}
                    >
                      <div className="flex items-center gap-2 min-w-0">
                        <ShieldCheck className="h-4 w-4 text-muted-foreground flex-shrink-0" />
                        <div className="min-w-0">
                          <p className="text-sm font-mono text-foreground truncate">{p.name}</p>
                          {p.benchmarkVersion && (
                            <p className="text-xs text-muted-foreground mt-0.5 truncate">
                              Benchmark: {p.benchmarkVersion}
                            </p>
                          )}
                        </div>
                        {recommended && (
                          <span className="text-2xs px-1.5 py-0.5 rounded bg-primary/10 text-primary font-medium">
                            Recommended
                          </span>
                        )}
                      </div>
                      {profile === p.name && <CheckCircle2 className="h-4 w-4 text-primary" />}
                    </button>
                  );
                })}
              </div>
            )}
          </div>
        )}

        {step === 3 && (
          <div className="space-y-4">
            <div>
              <h2 className="text-sm font-medium text-foreground">Review &amp; run</h2>
              <p className="text-xs text-muted-foreground mt-1">
                Confirm the scan parameters. The scan will be polled in the background; you'll be
                redirected to its detail page.
              </p>
            </div>

            <dl className="rounded-md border border-border divide-y divide-border">
              <ReviewRow label="Cluster" value={cluster?.displayName || cluster?.name || '—'} />
              <ReviewRow
                label="Distribution"
                value={cluster ? distributionDisplayName(cluster.distribution) : '—'}
              />
              <ReviewRow label="Profile" value={profile || '—'} mono />
            </dl>

            {createScan.isError && (
              <div className="rounded-md border border-status-error/30 bg-status-error/5 p-3 flex items-start gap-2.5">
                <ShieldAlert className="h-4 w-4 text-status-error flex-shrink-0 mt-0.5" />
                <p className="text-xs text-status-error">
                  {(createScan.error as Error)?.message ?? 'Failed to start scan.'}
                </p>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Footer */}
      <div className="flex items-center justify-between">
        <button
          type="button"
          onClick={() => (step > 1 ? setStep((s) => (s - 1) as WizardStep) : router.push('/dashboard/security'))}
          disabled={createScan.isPending}
          className="inline-flex items-center gap-1.5 h-9 px-4 rounded-lg border border-border text-sm font-medium
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
        >
          <ArrowLeft className="h-4 w-4" />
          {step > 1 ? 'Back' : 'Cancel'}
        </button>

        {step < 3 ? (
          <button
            type="button"
            onClick={() => setStep((s) => (s + 1) as WizardStep)}
            disabled={!canAdvance}
            className="inline-flex items-center gap-1.5 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            Next
            <ArrowRight className="h-4 w-4" />
          </button>
        ) : (
          <button
            type="button"
            onClick={handleSubmit}
            disabled={createScan.isPending || !clusterId}
            className="inline-flex items-center gap-1.5 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {createScan.isPending ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <ShieldCheck className="h-4 w-4" />
            )}
            Run Scan
          </button>
        )}
      </div>
    </div>
  );
}

function ReviewRow({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-center justify-between px-4 py-3">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className={cn('text-sm text-foreground', mono && 'font-mono')}>{value}</dd>
    </div>
  );
}

/**
 * Mirrors `defaultCISProfileForDistribution` on the backend so the wizard's
 * "Recommended" highlight matches what the server will pick when no profile
 * is supplied. Kept in sync manually — the list is short and stable.
 */
function defaultProfileForDistribution(distribution: string): string {
  switch (distribution.toLowerCase().trim()) {
    case 'rke':
    case 'rke1':
      return 'rke-cis-1.8-permissive';
    case 'rke2':
      return 'rke2-cis-1.8-permissive';
    case 'k3s':
      return 'k3s-cis-1.8-permissive';
    case 'eks':
      return 'eks-cis-1.5';
    case 'aks':
      return 'aks-cis-1.0';
    case 'gke':
      return 'gke-cis-1.5';
    default:
      return 'cis-1.8';
  }
}

export const Route = createFileRoute('/dashboard/security/scans/new/')({
  component: NewScanWizardPage,
});
