'use client';

// Sync dialog: triggers POST /argocd/applications/{id}/sync/ with optional
// revision / prune / dry-run fields. The backend enqueues an Operation row
// and returns it; the parent page polls operations to convergence.

import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { X, Loader2, RefreshCw } from 'lucide-react';
import { syncArgoApplicationById } from '@/lib/api';
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

  const sync = useMutation({
    mutationFn: () => syncArgoApplicationById(appId, { revision: revision || undefined, prune, dryRun }),
    onSuccess: (op) => {
      queryClient.invalidateQueries({ queryKey: ['argocd', 'operations'] });
      toast.success(`Sync queued for ${appName}`);
      onSubmitted?.(op);
      onClose();
    },
    onError: (error: Error) => {
      toast.error(`Sync failed: ${error.message}`);
    },
  });

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-md rounded-xl border border-border bg-popover shadow-2xl overflow-hidden">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border">
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
              <RefreshCw className="h-4 w-4 text-muted-foreground" />
            </div>
            <h3 className="text-lg font-semibold text-foreground">Sync Application</h3>
          </div>
          <button
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground transition-colors"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="p-6 space-y-4">
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
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border bg-muted/30">
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
            disabled={sync.isPending}
            className="inline-flex items-center gap-1.5 h-8 px-4 rounded text-sm font-medium
              bg-primary text-primary-foreground hover:bg-primary/90 transition-colors
              disabled:opacity-50"
          >
            {sync.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {dryRun ? 'Preview Sync' : 'Sync'}
          </button>
        </div>
      </div>
    </div>
  );
}
