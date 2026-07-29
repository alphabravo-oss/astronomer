'use client';

/**
 * The in-flight / failed / gave-up surface for one monitoring-stack operation.
 *
 * This is the part of the feature that is not a form. install / upgrade /
 * replace / uninstall enqueue work and return a receipt; the Helm apply,
 * readiness wait, health probe and smoke query happen afterwards and take tens
 * of seconds to minutes. Every state `useMonitoringOperationTracker` can report
 * has a rendering here, because the ones without a rendering are exactly the
 * ones that make the product look broken:
 *
 *   active            — spinner, verb, attempt number, elapsed, stage log.
 *   stalled           — still running well past the slowest legitimate path.
 *   stopped tracking  — the client ceiling. Says the operation MAY STILL BE
 *                       RUNNING, because it is; offers Refresh and Dismiss, not
 *                       a lie. The panel's controls also unlock here
 *                       (tracker.isBusy goes false), because enqueueing over a
 *                       wedged operation — which the backend supersedes — is
 *                       the only escape an operator has.
 *   awaiting retry    — failed, but inside the window where the backend's own
 *                       retry policy may requeue it. Error shown, Retry hidden
 *                       (it would race the reconciler).
 *   failed / settled  — the reconciler's error VERBATIM, plus Retry.
 *   superseded        — a newer operation took over. Retryable, not the user's
 *                       fault, so it is not worded as an error.
 *   completed         — success, dismissible.
 */
import { AlertTriangle, CheckCircle2, Clock, Loader2, RotateCcw, X } from 'lucide-react';

import { ActionButton } from '@/components/ui/action-button';
import {
  OperationTimeline,
  type OperationTimelineStep,
} from '@/components/ui/operation-timeline';
import { cn } from '@/lib/utils';
import type { MonitoringOperationTracking } from '@/components/monitoring/hooks';

function formatElapsed(ms: number): string {
  const total = Math.max(0, Math.round(ms / 1000));
  const minutes = Math.floor(total / 60);
  const seconds = total % 60;
  return minutes > 0 ? `${minutes}m ${seconds}s` : `${seconds}s`;
}

function verbLabel(operationType: string | undefined): string {
  switch (operationType) {
    case 'install':
      return 'Install';
    case 'upgrade':
      return 'Upgrade';
    case 'replace':
      return 'Replace';
    case 'uninstall':
      return 'Uninstall';
    default:
      return 'Operation';
  }
}

function stepStatusForLevel(level: string): OperationTimelineStep['status'] {
  const normalized = level.toLowerCase();
  if (normalized === 'error' || normalized === 'fatal') return 'failed';
  return 'success';
}

export interface StackOperationPanelProps {
  tracker: MonitoringOperationTracking;
  /**
   * Retry re-enqueues the row, which the backend gates on monitoring:update at
   * the operation's own scope — so the button is absent, not disabled, for a
   * caller who only holds monitoring:read.
   */
  canRetry: boolean;
  className?: string;
}

export function StackOperationPanel({ tracker, canRetry, className }: StackOperationPanelProps) {
  const op = tracker.operation;
  if (!op) return null;

  const verb = verbLabel(op.operationType);
  const steps: OperationTimelineStep[] = tracker.events.map((event) => ({
    id: event.id,
    label: event.message,
    status: stepStatusForLevel(event.level),
    detail: `${event.stage} · ${new Date(event.createdAt).toLocaleTimeString()}`,
  }));

  if (tracker.isActive && !tracker.hasStoppedTracking) {
    steps.push({
      id: 'in-flight',
      label: op.status === 'pending' ? 'Queued for the reconciler' : 'Reconciler working…',
      status: 'running',
    });
  }

  // Superseded is terminal and retryable but is NOT an operator error: a newer
  // operation for the same target took over. Word it that way.
  const superseded = op.status === 'superseded';

  return (
    <OperationTimeline
      className={className}
      header={
        <div className="flex min-w-0 items-center gap-2">
          <HeaderIcon tracker={tracker} />
          <span className="truncate text-sm font-medium text-foreground">
            {verb}
            {tracker.isActive ? ' in progress' : superseded ? ' superseded' : ''}
            {tracker.isSuccess ? ' complete' : ''}
            {tracker.isFailure && !superseded ? ' failed' : ''}
          </span>
          {op.attemptCount > 1 && (
            <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
              attempt {op.attemptCount}
            </span>
          )}
        </div>
      }
      headerMeta={
        <span className="tabular-nums" title={`Operation ${op.id}`}>
          {formatElapsed(tracker.elapsedMs)}
        </span>
      }
      steps={steps}
      emptyLabel={
        tracker.isActive
          ? 'Waiting for the reconciler to pick this up…'
          : 'This operation recorded no stage events.'
      }
      footer={
        <div className="space-y-3 border-t border-border px-4 py-3">
          {tracker.hasStoppedTracking && (
            <Notice tone="warning" icon={Clock}>
              Stopped following this operation after 30 minutes.{' '}
              <strong className="font-medium">It may still be running on the server</strong> —
              nothing has been cancelled. Refresh to re-read its state, or dismiss it and queue
              another: <strong className="font-medium">a new operation supersedes this one</strong>,
              which is the way out of a wedged reconciler. Retry is not offered because the backend
              only requeues a row that already reached a terminal state.
            </Notice>
          )}

          {!tracker.hasStoppedTracking && tracker.isStalled && (
            <Notice tone="warning" icon={Clock}>
              Still running after {formatElapsed(tracker.elapsedMs)}. The slowest normal path (a
              replace, with readiness and smoke checks) finishes inside about five minutes — check
              the cluster agent if this does not settle.
            </Notice>
          )}

          {tracker.isAwaitingAutoRetry && (
            <Notice tone="warning" icon={AlertTriangle}>
              Attempt {op.attemptCount} failed. The reconciler may requeue it automatically — its
              retry policy runs within a few seconds.
            </Notice>
          )}

          {tracker.errorMessage && (
            <div>
              <div className="mb-1 text-xs font-medium text-muted-foreground">
                {superseded ? 'Reason' : 'Reconciler error'}
              </div>
              {/* Verbatim: this is the real Helm / readiness / smoke-check text
                  and it is the only thing that tells an operator what to fix. */}
              <pre className="max-h-40 overflow-auto whitespace-pre-wrap rounded border border-border bg-background px-3 py-2 font-mono text-xs text-status-error">
                {tracker.errorMessage}
              </pre>
            </div>
          )}

          <div className="flex flex-wrap items-center gap-2">
            {tracker.isSettled && tracker.canRetry && canRetry && (
              <ActionButton
                size="sm"
                intent="primary"
                icon={<RotateCcw className="h-3.5 w-3.5" />}
                loading={tracker.isRetrying}
                loadingLabel="Retrying"
                onClick={tracker.retry}
              >
                Retry
              </ActionButton>
            )}
            {(tracker.hasStoppedTracking || tracker.isStalled || tracker.isTerminal) && (
              <ActionButton size="sm" onClick={tracker.refresh}>
                Refresh
              </ActionButton>
            )}
            {/* Dismiss is also offered past the ceiling, where the row is NOT
                terminal: it is the only affordance that clears a wedged
                operation off the panel, and the tracker suppresses re-adopting
                the id it dismissed. */}
            {(tracker.isTerminal || tracker.hasStoppedTracking) && (
              <ActionButton
                size="sm"
                intent="ghost"
                icon={<X className="h-3.5 w-3.5" />}
                onClick={tracker.clear}
              >
                Dismiss
              </ActionButton>
            )}
          </div>
        </div>
      }
    />
  );
}

function HeaderIcon({ tracker }: { tracker: MonitoringOperationTracking }) {
  if (tracker.hasStoppedTracking) return <Clock className="h-4 w-4 shrink-0 text-status-warning" />;
  if (tracker.isActive) return <Loader2 className="h-4 w-4 shrink-0 animate-spin text-primary" />;
  if (tracker.isSuccess) return <CheckCircle2 className="h-4 w-4 shrink-0 text-status-success" />;
  if (tracker.isFailure) return <AlertTriangle className="h-4 w-4 shrink-0 text-status-error" />;
  return <Clock className="h-4 w-4 shrink-0 text-muted-foreground" />;
}

function Notice({
  tone,
  icon: Icon,
  children,
}: {
  tone: 'warning' | 'info';
  icon: React.ElementType;
  children: React.ReactNode;
}) {
  return (
    <div
      className={cn(
        'flex items-start gap-2 rounded-md border px-3 py-2 text-xs',
        tone === 'warning'
          ? 'border-status-warning/30 bg-status-warning/10 text-status-warning'
          : 'border-border bg-muted/40 text-muted-foreground',
      )}
    >
      <Icon className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      <span className="min-w-0">{children}</span>
    </div>
  );
}
