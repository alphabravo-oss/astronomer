'use client';

// Sprint 23 - shared registration-timeline component.
//
// Used by both:
//   - the wizard's progress page (/clusters/register/[id]/progress/)
//   - the cluster-detail Adoption tab (/clusters/[id]/adoption/)
//
// Subscribes to the wizard SSE stream + polls /clusters/{id}/registration/
// status/ as a fallback. Renders each step row with status icon, label,
// detail, optional progress bar, and a Retry button on failed rows.

import { useCallback, useEffect, useState } from 'react';
import { toastError, toastSuccess } from '@/lib/toast';
import {
  getRegistrationStatus,
  retryRegistrationStep,
  type RegistrationStatus,
  type RegistrationStep,
} from '@/lib/api';
import { useLiveEvents } from '@/lib/live/hooks';
import { useStore } from '@tanstack/react-store';
import { liveStatus } from '@/lib/live/status-store';
import { ActionButton } from '@/components/ui/action-button';
import { OperationTimeline, type OperationTimelineStepStatus } from '@/components/ui/operation-timeline';

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
    // Live envelopes are camelized centrally (lib/live/envelope.ts).
    const off1 = live.subscribe('cluster.registration.step', (payload) => {
      const data = (payload as { data?: { clusterId?: string } }).data;
      if (data?.clusterId === clusterId) refresh();
    });
    const off2 = live.subscribe('cluster.registration.phase', (payload) => {
      const data = (payload as { data?: { clusterId?: string } }).data;
      if (data?.clusterId === clusterId) refresh();
    });
    return () => {
      off1();
      off2();
    };
  }, [live, clusterId, refresh]);

  // Stream-status-conditional polling fallback (P4.5): SSE is the primary
  // channel — while the stream is open the registration.step/phase events
  // drive refresh and the poll stays off. It only runs when the stream is
  // down (proxy timeout, tab throttling) and stops at a terminal phase.
  const streamStatus = useStore(liveStatus);
  useEffect(() => {
    if (notFound || status?.phase === 'ready' || status?.phase === 'failed') return;
    if (streamStatus === 'open') return;
    const interval = setInterval(refresh, 5000);
    return () => clearInterval(interval);
  }, [refresh, notFound, status?.phase, streamStatus]);

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
      toastSuccess('Retry queued');
    } catch (e) {
      const msg = e instanceof Error ? e.message : 'Unknown error';
      toastError(`Retry failed: ${msg}`);
    } finally {
      setRetrying(null);
    }
  };

  if (notFound) {
    return (
      <div className="text-sm text-muted-foreground py-4">
        No registration record for this cluster - it likely predates the wizard or has already been
        cleaned up.
      </div>
    );
  }

  const steps = (status?.steps ?? []).map((step) => ({
    id: step.id,
    label: step.label,
    status: timelineStatus(step.status),
    detail: step.detail && Object.keys(step.detail).length > 0
      ? Object.entries(step.detail)
        .map(([k, v]) => `${k}: ${String(v)}`)
        .join(' • ')
      : undefined,
    error: step.error_message,
    progressPct: step.progress_pct,
    action: step.status === 'failed' ? (
      <ActionButton
        intent="ghost"
        size="sm"
        onClick={() => onRetry(step)}
        loading={retrying === step.id}
        loadingLabel="Retrying..."
      >
        Retry
      </ActionButton>
    ) : undefined,
  }));

  return (
    <OperationTimeline
      header={<PhaseBadge phase={status?.phase} />}
      headerMeta={status?.started_at ? `Started ${new Date(status.started_at).toLocaleString()}` : undefined}
      steps={steps}
      emptyLabel={status ? 'Waiting for first step...' : 'Loading...'}
      footer={!embedded && status?.phase === 'failed' ? (
        <div className="px-4 py-3 border-t border-border bg-status-danger/5">
          <p className="text-xs text-muted-foreground">
            Use the Retry buttons above to re-run a failing step, or talk to your platform team if
            the issue persists.
          </p>
        </div>
      ) : undefined}
    />
  );
}

function timelineStatus(status: RegistrationStep['status']): OperationTimelineStepStatus {
  switch (status) {
    case 'success':
      return 'success';
    case 'running':
      return 'running';
    case 'failed':
      return 'failed';
    case 'skipped':
      return 'skipped';
    default:
      return 'pending';
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
    phase === 'provisioning' ? 'applying baseline' :
    phase;
  return <span className={`text-xs font-medium uppercase tracking-wide ${colour}`}>Phase: {label}</span>;
}
