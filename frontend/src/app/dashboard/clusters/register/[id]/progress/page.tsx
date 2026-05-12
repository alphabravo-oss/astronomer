'use client';

// Wizard page 3 — live progress timeline. Subscribes to the wizard's
// per-cluster SSE stream and renders one row per step. When the
// cluster reaches `ready`, the "Take me to the cluster →" CTA appears.

import { useCallback, useEffect, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import { toast } from 'sonner';
import { Check, Circle, Loader2, X, Server } from 'lucide-react';
import {
  getRegistrationStatus,
  retryRegistrationStep,
  type RegistrationStatus,
  type RegistrationStep,
} from '@/lib/api';
import { useLiveEvents } from '@/lib/live-events';

export default function ProgressStepPage() {
  const router = useRouter();
  const params = useParams();
  const clusterId = String(params?.id ?? '');

  const [status, setStatus] = useState<RegistrationStatus | null>(null);
  const [retrying, setRetrying] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    if (!clusterId) return;
    try {
      const s = await getRegistrationStatus(clusterId);
      setStatus(s);
    } catch (e) {
      // 404 just means the cluster row was deleted while we were
      // watching. Bounce to the list instead of pinning a broken UI.
      const msg = e instanceof Error ? e.message : '';
      if (msg.includes('404') || msg.toLowerCase().includes('not_found')) {
        router.push('/dashboard/clusters');
      }
    }
  }, [clusterId, router]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Subscribe to wizard-scoped events. The bus fires for ALL clusters;
  // filter here.
  const live = useLiveEvents();
  useEffect(() => {
    const off1 = live.subscribe('cluster.registration.step', (payload) => {
      const data = payload as { cluster_id?: string };
      if (data?.cluster_id === clusterId) refresh();
    });
    const off2 = live.subscribe('cluster.registration.phase', (payload) => {
      const data = payload as { cluster_id?: string };
      if (data?.cluster_id === clusterId) refresh();
    });
    return () => {
      off1();
      off2();
    };
  }, [live, clusterId, refresh]);

  const onRetry = async (step: RegistrationStep) => {
    setRetrying(step.id);
    try {
      const s = await retryRegistrationStep(clusterId, step.id);
      setStatus(s);
      toast.success('Retry queued');
    } catch (e) {
      const msg = e instanceof Error ? e.message : 'Unknown error';
      toast.error(`Retry failed: ${msg}`);
    } finally {
      setRetrying(null);
    }
  };

  const phase = status?.phase;
  const isReady = phase === 'ready';
  const isFailed = phase === 'failed';

  return (
    <div className="max-w-3xl mx-auto p-6">
      <div className="mb-6 flex items-center gap-3">
        <div className="w-10 h-10 rounded-lg bg-muted flex items-center justify-center">
          <Server className="h-5 w-5 text-muted-foreground" />
        </div>
        <div>
          <h1 className="text-2xl font-semibold text-foreground">Provisioning</h1>
          <p className="text-sm text-muted-foreground">Step 3 of 3 — Watch your cluster come online</p>
        </div>
      </div>

      <div className="rounded-lg border border-border bg-card">
        <div className="px-4 py-3 border-b border-border flex items-center justify-between">
          <PhaseBadge phase={phase} />
          {status?.started_at && (
            <span className="text-xs text-muted-foreground">
              Started {new Date(status.started_at).toLocaleString()}
            </span>
          )}
        </div>

        <ul className="divide-y divide-border">
          {(status?.steps ?? []).map((step) => (
            <li key={step.id} className="px-4 py-3 flex items-start gap-3">
              <StepIcon status={step.status} />
              <div className="flex-1 min-w-0">
                <div className="text-sm font-medium text-foreground">{step.label}</div>
                {step.detail && Object.keys(step.detail).length > 0 && (
                  <div className="text-xs text-muted-foreground mt-0.5 truncate">
                    {Object.entries(step.detail)
                      .map(([k, v]) => `${k}: ${String(v)}`)
                      .join(' • ')}
                  </div>
                )}
                {step.error_message && (
                  <div className="text-xs text-status-danger mt-1">{step.error_message}</div>
                )}
                {step.status === 'running' && step.progress_pct > 0 && (
                  <div className="mt-1 h-1.5 w-full bg-muted rounded overflow-hidden">
                    <div className="h-full bg-primary" style={{ width: `${step.progress_pct}%` }} />
                  </div>
                )}
              </div>
              {step.status === 'failed' && (
                <button
                  onClick={() => onRetry(step)}
                  disabled={retrying === step.id}
                  className="text-xs text-primary hover:underline disabled:opacity-50"
                >
                  {retrying === step.id ? 'Retrying...' : 'Retry'}
                </button>
              )}
            </li>
          ))}
          {!status?.steps?.length && (
            <li className="px-4 py-6 text-sm text-muted-foreground text-center">
              {status ? 'Waiting for first step...' : 'Loading...'}
            </li>
          )}
        </ul>
      </div>

      {isReady && (
        <div className="mt-6 flex items-center justify-between p-4 rounded-lg border border-status-success/30 bg-status-success/5">
          <div className="flex items-center gap-2 text-sm font-medium text-status-success">
            <Check className="h-4 w-4" />
            Cluster is ready
          </div>
          <button
            onClick={() => router.push(`/dashboard/clusters/${clusterId}`)}
            className="h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90"
          >
            Take me to the cluster →
          </button>
        </div>
      )}
      {isFailed && (
        <div className="mt-6 p-4 rounded-lg border border-status-danger/30 bg-status-danger/5">
          <div className="text-sm font-medium text-status-danger">Registration failed</div>
          <p className="mt-1 text-xs text-muted-foreground">
            Use the Retry buttons above to re-run a failing step, or talk to your platform team if the
            issue persists.
          </p>
        </div>
      )}
    </div>
  );
}

function StepIcon({ status }: { status: RegistrationStep['status'] }) {
  switch (status) {
    case 'success':
      return <Check className="h-4 w-4 text-status-success mt-0.5" />;
    case 'running':
      return <Loader2 className="h-4 w-4 text-primary animate-spin mt-0.5" />;
    case 'failed':
      return <X className="h-4 w-4 text-status-danger mt-0.5" />;
    case 'skipped':
      return <Circle className="h-4 w-4 text-muted-foreground/40 mt-0.5" />;
    default:
      return <Circle className="h-4 w-4 text-muted-foreground/40 mt-0.5" />;
  }
}

function PhaseBadge({ phase }: { phase: RegistrationStatus['phase'] | undefined }) {
  if (!phase) return <span className="text-xs text-muted-foreground">Loading...</span>;
  const colour =
    phase === 'ready' ? 'text-status-success' :
    phase === 'failed' ? 'text-status-danger' :
    'text-muted-foreground';
  return <span className={`text-xs font-medium uppercase tracking-wide ${colour}`}>Phase: {phase}</span>;
}
