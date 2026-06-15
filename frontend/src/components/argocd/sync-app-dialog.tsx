'use client';

// Sync dialog: triggers POST /argocd/applications/{id}/sync/ with optional
// revision / prune / dry-run fields. The backend enqueues an Operation row
// and returns it; the parent page polls operations to convergence.

import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import { ModalShell } from '@/components/ui/modal-shell';
import { Loader2, RefreshCw } from 'lucide-react';
import { syncArgoApplicationById } from '@/lib/api';
import { queryKeys } from '@/lib/hooks';
import type { ArgoOperation } from '@/types';

interface SyncAppDialogProps {
  appId: string;
  appName: string;
  defaultRevision?: string;
  onClose: () => void;
  onSubmitted?: (op: ArgoOperation) => void;
}

export function SyncAppDialog({
  appId,
  appName,
  defaultRevision,
  onClose,
  onSubmitted,
}: SyncAppDialogProps) {
  const queryClient = useQueryClient();
  const [revision, setRevision] = useState(defaultRevision ?? '');
  const [prune, setPrune] = useState(false);
  const [dryRun, setDryRun] = useState(false);
  const [syncWindowOverride, setSyncWindowOverride] = useState(false);
  const [reason, setReason] = useState('');
  const reasonRequired = syncWindowOverride && !reason.trim();

  const sync = useMutation({
    mutationFn: () =>
      syncArgoApplicationById(appId, {
        revision: revision || undefined,
        prune,
        dryRun,
        syncWindowOverride,
        reason: reason.trim() || undefined,
      }),
    onSuccess: (op) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.operations });
      toastSuccess(`Sync queued for ${appName}`);
      onSubmitted?.(op);
      onClose();
    },
    onError: (error: Error) => {
      toastApiError('Sync failed', error);
    },
  });

  return (
    <ModalShell
      title="Sync Application"
      onClose={onClose}
      size="sm"
      panelClassName="max-w-md bg-popover overflow-hidden"
      footerClassName="bg-muted/30"
      titleIcon={(
        <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
          <RefreshCw className="h-4 w-4 text-muted-foreground" />
        </div>
      )}
      footer={(
        <div className="flex items-center justify-end gap-2">
          <button
            onClick={onClose}
            disabled={sync.isPending}
            className="inline-flex items-center h-8 px-3 rounded text-sm
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={() => sync.mutate()}
            disabled={sync.isPending || reasonRequired}
            className="inline-flex items-center gap-1.5 h-8 px-4 rounded text-sm font-medium
              bg-primary text-primary-foreground hover:bg-primary/90 transition-colors
              disabled:opacity-50"
          >
            {sync.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {dryRun ? 'Preview Sync' : 'Sync'}
          </button>
        </div>
      )}
    >
          <div className="text-sm text-muted-foreground">
            Sync <span className="font-mono text-foreground">{appName}</span> against its
            target revision. Optional fields override the defaults defined on the
            Application.
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Revision (optional)</label>
            <input
              type="text"
              value={revision}
              onChange={(e) => setRevision(e.target.value)}
              placeholder="HEAD or 7c9f2a1"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <label className="flex items-start gap-3 text-sm">
            <input
              type="checkbox"
              checked={prune}
              onChange={(e) => setPrune(e.target.checked)}
              className="mt-0.5 h-4 w-4 rounded border-border"
            />
            <div>
              <div className="text-foreground font-medium">Prune resources</div>
              <div className="text-xs text-muted-foreground">
                Delete resources no longer present in the target manifests.
              </div>
            </div>
          </label>

          <label className="flex items-start gap-3 text-sm">
            <input
              type="checkbox"
              checked={dryRun}
              onChange={(e) => setDryRun(e.target.checked)}
              className="mt-0.5 h-4 w-4 rounded border-border"
            />
            <div>
              <div className="text-foreground font-medium">Dry run</div>
              <div className="text-xs text-muted-foreground">
                Preview the diff; do not apply changes.
              </div>
            </div>
          </label>

          <label className="flex items-start gap-3 text-sm">
            <input
              type="checkbox"
              checked={syncWindowOverride}
              onChange={(e) => setSyncWindowOverride(e.target.checked)}
              className="mt-0.5 h-4 w-4 rounded border-border"
            />
            <div>
              <div className="text-foreground font-medium">Sync-window override</div>
              <div className="text-xs text-muted-foreground">
                Record this sync as an emergency or approved manual window override.
              </div>
            </div>
          </label>

          {syncWindowOverride && (
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Override reason</label>
              <textarea
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                rows={3}
                maxLength={500}
                placeholder="Change ticket or incident reason"
                className="w-full px-3 py-2 rounded-md border border-border bg-background text-sm
                  focus:outline-none focus:ring-1 focus:ring-ring"
              />
              <div className="flex justify-end text-xs text-muted-foreground tabular-nums">
                {reason.trim().length}/500
              </div>
            </div>
          )}
    </ModalShell>
  );
}
