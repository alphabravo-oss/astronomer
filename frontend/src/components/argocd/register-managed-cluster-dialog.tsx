'use client';

// Register one of our managed clusters into an ArgoCD instance.
//
// The bearer token is the only required credential — it's a ServiceAccount
// token minted inside the destination cluster (Astronomer doesn't store
// these; the operator pastes one here per registration).

import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { X, Loader2, Server } from 'lucide-react';
import { registerArgoManagedCluster } from '@/lib/api';
import type { Cluster } from '@/types';

interface RegisterManagedClusterDialogProps {
  instanceId: string;
  cluster: Cluster;
  onClose: () => void;
}

function parseLabels(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const eq = trimmed.indexOf('=');
    if (eq <= 0) continue;
    const k = trimmed.slice(0, eq).trim();
    const v = trimmed.slice(eq + 1).trim();
    if (k) out[k] = v;
  }
  return out;
}

export function RegisterManagedClusterDialog({
  instanceId,
  cluster,
  onClose,
}: RegisterManagedClusterDialogProps) {
  const queryClient = useQueryClient();
  const [bearerToken, setBearerToken] = useState('');
  const [insecure, setInsecure] = useState(false);
  const [labelsText, setLabelsText] = useState(
    `astronomer.io/environment=${cluster.environment}`,
  );

  const register = useMutation({
    mutationFn: () =>
      registerArgoManagedCluster(instanceId, cluster.id, {
        bearer_token: bearerToken,
        insecure,
        labels: parseLabels(labelsText),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['argocd', 'managed-clusters', instanceId] });
      toast.success(`${cluster.displayName} registered into ArgoCD`);
      onClose();
    },
    onError: (error: Error) => {
      toast.error(`Registration failed: ${error.message}`);
    },
  });

  const isLocalCluster = cluster.name === 'local';
  const canSubmit = (bearerToken.trim() !== '') || insecure;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-lg rounded-xl border border-border bg-popover shadow-2xl overflow-hidden">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border">
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
              <Server className="h-4 w-4 text-muted-foreground" />
            </div>
            <h3 className="text-lg font-semibold text-foreground">
              Register {cluster.displayName}
            </h3>
          </div>
          <button
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground transition-colors"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="p-6 space-y-4">
          <p className="text-xs text-muted-foreground">
            Stamps a Cluster Secret in the upstream ArgoCD instance so it can deploy
            applications to <span className="font-mono text-foreground">{cluster.name}</span>.
          </p>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Bearer Token</label>
            <input
              type="password"
              value={bearerToken}
              onChange={(e) => setBearerToken(e.target.value)}
              autoComplete="new-password"
              placeholder={isLocalCluster ? 'Optional for the local cluster' : 'ServiceAccount token from the destination cluster'}
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
            <p className="text-xs text-muted-foreground">
              {isLocalCluster ? (
                <>Astronomer mints and refreshes the local ArgoCD application-controller token automatically.</>
              ) : (
                <><code>kubectl -n argocd create token argocd-manager</code> on the target cluster.</>
              )}
            </p>
          </div>

          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={insecure}
              onChange={(e) => setInsecure(e.target.checked)}
              className="h-4 w-4 rounded border-border"
            />
            <span className="text-foreground">Skip TLS verification (insecure)</span>
          </label>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Extra Labels</label>
            <textarea
              value={labelsText}
              onChange={(e) => setLabelsText(e.target.value)}
              rows={3}
              placeholder="key=value (one per line)"
              className="w-full px-3 py-2 rounded-md border border-border bg-background text-xs font-mono
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
            <p className="text-xs text-muted-foreground">
              ApplicationSet cluster generators select on these labels. The astronomer.io/cluster-id
              and astronomer.io/cluster-name labels are added automatically.
            </p>
          </div>
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border bg-muted/30">
          <button
            onClick={onClose}
            disabled={register.isPending}
            className="inline-flex items-center h-8 px-3 rounded text-sm
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={() => register.mutate()}
            disabled={(!isLocalCluster && !canSubmit) || register.isPending}
            className="inline-flex items-center gap-1.5 h-8 px-4 rounded text-sm font-medium
              bg-primary text-primary-foreground hover:bg-primary/90 transition-colors
              disabled:opacity-50"
          >
            {register.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Register
          </button>
        </div>
      </div>
    </div>
  );
}
