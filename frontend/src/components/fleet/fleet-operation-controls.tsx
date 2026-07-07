'use client';

import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Pause, Play, Ban, RotateCcw, Loader2 } from 'lucide-react';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { toastApiError, toastSuccess } from '@/lib/toast';
import {
  abortFleetOperation,
  pauseFleetOperation,
  resumeFleetOperation,
  retryFailedFleetOperation,
  type FleetOperation,
} from '@/lib/api/fleet-operations';
import { queryKeys } from '@/lib/hooks';

interface FleetOperationControlsProps {
  operation: FleetOperation;
  canUpdate: boolean;
}

type Action = 'pause' | 'resume' | 'abort' | 'retry';

const RUNNERS: Record<Action, (id: string) => Promise<FleetOperation>> = {
  pause: pauseFleetOperation,
  resume: resumeFleetOperation,
  abort: abortFleetOperation,
  retry: retryFailedFleetOperation,
};

export function FleetOperationControls({ operation, canUpdate }: FleetOperationControlsProps) {
  const queryClient = useQueryClient();
  const [confirmAbort, setConfirmAbort] = useState(false);

  const status = operation.status;
  // State machine (backend transitionStatus): pause from running, resume from
  // paused, abort from pending|running|paused. retry-failed whenever there are
  // failed targets. Anything else is disabled so we never offer a 409 action.
  const canPause = canUpdate && status === 'running';
  const canResume = canUpdate && status === 'paused';
  const canAbort = canUpdate && ['pending', 'running', 'paused'].includes(status);
  const canRetry =
    canUpdate && (operation.failed_clusters > 0 || status === 'failed' || status === 'aborted');

  const run = useMutation({
    mutationFn: (action: Action) => RUNNERS[action](operation.id),
    onSuccess: (op, action) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.fleetOperations.detail(operation.id) });
      queryClient.invalidateQueries({ queryKey: queryKeys.fleetOperations.all });
      toastSuccess(`Operation ${action === 'retry' ? 'retry queued' : op.status}`);
    },
    onError: (error: Error) => {
      // 409 invalid-transition is surfaced as a toast; the detail query keeps
      // polling so the buttons re-sync to the true status.
      toastApiError('Action failed', error);
      queryClient.invalidateQueries({ queryKey: queryKeys.fleetOperations.detail(operation.id) });
    },
  });

  const pending = run.isPending;

  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          onClick={() => run.mutate('pause')}
          disabled={!canPause || pending}
          className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-xs font-medium text-foreground hover:bg-accent disabled:opacity-40"
        >
          {pending && run.variables === 'pause' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Pause className="h-3.5 w-3.5" />}
          Pause
        </button>
        <button
          type="button"
          onClick={() => run.mutate('resume')}
          disabled={!canResume || pending}
          className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-xs font-medium text-foreground hover:bg-accent disabled:opacity-40"
        >
          {pending && run.variables === 'resume' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
          Resume
        </button>
        <button
          type="button"
          onClick={() => run.mutate('retry')}
          disabled={!canRetry || pending}
          className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-xs font-medium text-foreground hover:bg-accent disabled:opacity-40"
        >
          {pending && run.variables === 'retry' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RotateCcw className="h-3.5 w-3.5" />}
          Retry failed
        </button>
        <button
          type="button"
          onClick={() => setConfirmAbort(true)}
          disabled={!canAbort || pending}
          className="inline-flex h-8 items-center gap-1.5 rounded-md border border-status-error/40 bg-background px-3 text-xs font-medium text-status-error hover:bg-status-error/10 disabled:opacity-40"
        >
          <Ban className="h-3.5 w-3.5" />
          Abort
        </button>
      </div>

      <ConfirmDialog
        open={confirmAbort}
        onClose={() => setConfirmAbort(false)}
        onConfirm={() => {
          setConfirmAbort(false);
          run.mutate('abort');
        }}
        title="Abort fleet operation"
        description="In-flight targets finish their current work, but no new clusters are dispatched. This cannot be undone."
        confirmText="Abort operation"
        variant="destructive"
        loading={pending}
      />
    </>
  );
}
