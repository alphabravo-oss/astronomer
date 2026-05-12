'use client';

// Sprint 23 — shared registration-timeline component.
//
// Used by both:
//   - the wizard's progress page (/clusters/register/[id]/progress/)
//   - the cluster-detail Provisioning tab (/clusters/[id]/provisioning/)
//
// Subscribes to the wizard SSE stream + polls /clusters/{id}/registration/
// status/ as a fallback. Renders each step row with status icon, label,
// detail, optional progress bar, and a Retry button on failed rows.

import { useCallback, useEffect, useState } from 'react';
import { toast } from 'sonner';
import { Check, Circle, Loader2, X } from 'lucide-react';
import {
  getRegistrationStatus,
  retryRegistrationStep,
  type RegistrationStatus,
  type RegistrationStep,
} from '@/lib/api';
import { useLiveEvents } from '@/lib/live-events';

interface Props {
  clusterId: string;
  /** Pass `true` when embedded inside the cluster-detail tab so the
   *  component renders without the wizard's "Step 3 of 3" header chrome.
   */
  embedded?: boolean;
  /** Optional callback invoked when the registration completes (status
   *  transitions to `ready`). Lets the host page swap chrome or
   *  redirect.
   */
  onReady?: () => void;
}

export function RegistrationTimeline({ clusterId, embedded = false, onReady }: Props) {
  const [status, setStatus] = useState<RegistrationStatus | null>(null);
  const [retrying, setRetrying] = useState<string | null>(null);
  const [notFound, setNotFound] = useState(false);

  const refresh = useCallback(async () => {
    if (!clusterId) return;
    try {
      const s = await getRegistrationStatus(clusterId);
      setStatus(s);
      setNotFound(false);
    } catch (e) {
      const msg = e instanceof Error ? e.message : '';
      if (msg.includes('404') || msg.toLowerCase().includes('not_found')) {
        setNotFound(true);
      }
    }
  }, [clusterId]);

  useEffect(() => {
    refresh();
  }, [refresh]);

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

  // Fire onReady once when we transition into ready phase.
  useEffect(() => {
    if (status?.phase === 'ready') onReady?.();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [status?.phase]);

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

  if (notFound) {
    return (
      <div className="text-sm text-muted-foreground py-4">
        No registration record for this cluster — it likely predates the wizard or has already been
        cleaned up.
      </div>
    );
  }

  return (
    <div className="rounded-lg border border-border bg-card">
      <div className="px-4 py-3 border-b border-border flex items-center justify-between">
        <PhaseBadge phase={status?.phase} />
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

      {!embedded && status?.phase === 'failed' && (
        <div className="px-4 py-3 border-t border-border bg-status-danger/5">
          <p className="text-xs text-muted-foreground">
            Use the Retry buttons above to re-run a failing step, or talk to your platform team if
            the issue persists.
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

export function PhaseBadge({ phase }: { phase: RegistrationStatus['phase'] | undefined }) {
  if (!phase) return <span className="text-xs text-muted-foreground">Loading...</span>;
  const colour =
    phase === 'ready' ? 'text-status-success' :
    phase === 'failed' ? 'text-status-danger' :
    phase === 'provisioning' ? 'text-primary' :
    'text-muted-foreground';
  const label =
    phase === 'awaiting_agent' ? 'awaiting agent' :
    phase;
  return <span className={`text-xs font-medium uppercase tracking-wide ${colour}`}>Phase: {label}</span>;
}
