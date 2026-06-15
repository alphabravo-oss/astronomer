'use client';

// Register one of our managed clusters into an ArgoCD instance.
//
// The bearer token is the only required credential — it's a ServiceAccount
// token minted inside the destination cluster (Astronomer doesn't store
// these; the operator pastes one here per registration).

import { useMemo, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import { ModalShell } from '@/components/ui/modal-shell';
import { Loader2, Server } from 'lucide-react';
import { registerArgoManagedCluster } from '@/lib/api';
import { queryKeys } from '@/lib/hooks';
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

function reservedArgoLabelKey(key: string): boolean {
  const trimmed = key.trim();
  return (
    trimmed === 'astronomer.io' ||
    trimmed === 'argocd.argoproj.io' ||
    trimmed.startsWith('astronomer.io/') ||
    trimmed.startsWith('argocd.argoproj.io/')
  );
}

function firstReservedLabel(labels: Record<string, string>): string | null {
  return Object.keys(labels).find(reservedArgoLabelKey) ?? null;
}

export function RegisterManagedClusterDialog({
  instanceId,
  cluster,
  onClose,
}: RegisterManagedClusterDialogProps) {
  const queryClient = useQueryClient();
  const [bearerToken, setBearerToken] = useState('');
  const [insecure, setInsecure] = useState(false);
  const [labelsText, setLabelsText] = useState('');
  const parsedLabels = useMemo(() => parseLabels(labelsText), [labelsText]);
  const reservedLabel = useMemo(() => firstReservedLabel(parsedLabels), [parsedLabels]);

  const register = useMutation({
    mutationFn: () => {
      if (reservedLabel) {
        throw new Error(`Reserved label: ${reservedLabel}`);
      }
      return registerArgoManagedCluster(instanceId, cluster.id, {
        bearer_token: bearerToken,
        insecure,
        labels: parsedLabels,
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.managedClusters(instanceId) });
      toastSuccess(`${cluster.displayName} registered into ArgoCD`);
      onClose();
    },
    onError: (error: Error) => {
      toastApiError('Registration failed', error);
    },
  });

  const isLocalCluster = cluster.name === 'local';
  const hasRegistrationCredential = bearerToken.trim() !== '' || insecure;
  const canSubmit = !reservedLabel && (isLocalCluster || hasRegistrationCredential);

  return (
    <ModalShell
      title={`Register ${cluster.displayName}`}
      onClose={onClose}
      panelClassName="max-w-lg bg-popover overflow-hidden"
      footerClassName="bg-muted/30"
      titleIcon={(
        <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
          <Server className="h-4 w-4 text-muted-foreground" />
        </div>
      )}
      footer={(
        <div className="flex items-center justify-end gap-2">
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
            disabled={!canSubmit || register.isPending}
            className="inline-flex items-center gap-1.5 h-8 px-4 rounded text-sm font-medium
              bg-primary text-primary-foreground hover:bg-primary/90 transition-colors
              disabled:opacity-50"
          >
            {register.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Register
          </button>
        </div>
      )}
    >
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
              Use non-reserved labels. Astronomer and Argo CD labels are added automatically.
            </p>
            {reservedLabel && (
              <p className="text-xs text-status-error">
                Reserved label: <code>{reservedLabel}</code>
              </p>
            )}
          </div>
    </ModalShell>
  );
}
