'use client';

// Modal: register a new ArgoCD instance against a cluster.
//
// Fields:
//   - cluster (selected via the existing useClusters hook)
//   - display name
//   - api url (https://...)
//   - auth token (treated as a credential — type="password" and never echoed)
//   - verify SSL toggle
//
// Posts to /argocd/instances/. The auth_token is sent in plaintext; the
// backend Fernet-encrypts it before persisting.

import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import { ModalShell } from '@/components/ui/modal-shell';
import { Loader2, GitBranch } from 'lucide-react';
import api from '@/lib/api';
import { queryKeys, useClusters } from '@/lib/hooks';

interface RegisterInstanceModalProps {
  onClose: () => void;
}

export function RegisterInstanceModal({ onClose }: RegisterInstanceModalProps) {
  const queryClient = useQueryClient();
  const { data: clustersPage } = useClusters({ pageSize: 100 });
  const clusters = clustersPage?.data ?? [];

  const [form, setForm] = useState({
    name: '',
    clusterId: '',
    apiUrl: '',
    authToken: '',
    verifySsl: true,
  });

  const create = useMutation({
    mutationFn: async () => {
      const res = await api.post('/argocd/instances', {
        name: form.name,
        cluster_id: form.clusterId,
        api_url: form.apiUrl,
        auth_token: form.authToken,
        verify_ssl: form.verifySsl,
      });
      return res.data;
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.instances() });
      toastSuccess('ArgoCD instance registered');
      onClose();
    },
    onError: (error: Error) => {
      toastApiError('Failed to register instance', error);
    },
  });

  const canSubmit =
    form.name.trim() && form.clusterId && form.apiUrl.trim() && form.authToken.trim();

  return (
    <ModalShell
      title="Register ArgoCD Instance"
      onClose={onClose}
      panelClassName="max-w-lg bg-popover overflow-hidden"
      footerClassName="bg-muted/30"
      titleIcon={(
        <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
          <GitBranch className="h-4 w-4 text-muted-foreground" />
        </div>
      )}
      footer={(
        <div className="flex items-center justify-end gap-2">
          <button
            onClick={onClose}
            disabled={create.isPending}
            className="inline-flex items-center h-8 px-3 rounded text-sm
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={() => create.mutate()}
            disabled={!canSubmit || create.isPending}
            className="inline-flex items-center gap-1.5 h-8 px-4 rounded text-sm font-medium
              bg-primary text-primary-foreground hover:bg-primary/90 transition-colors
              disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {create.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Register
          </button>
        </div>
      )}
    >
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Display Name</label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="prod-argocd"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Cluster</label>
            <select
              value={form.clusterId}
              onChange={(e) => setForm({ ...form, clusterId: e.target.value })}
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option value="">Select a cluster…</option>
              {clusters.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.displayName} ({c.name})
                </option>
              ))}
            </select>
            <p className="text-xs text-muted-foreground">
              The cluster the ArgoCD control plane runs on.
            </p>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">API URL</label>
            <input
              type="url"
              value={form.apiUrl}
              onChange={(e) => setForm({ ...form, apiUrl: e.target.value })}
              placeholder="https://argocd.example.com"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Auth Token</label>
            <input
              type="password"
              value={form.authToken}
              onChange={(e) => setForm({ ...form, authToken: e.target.value })}
              autoComplete="new-password"
              placeholder="••••••••••••"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
            <p className="text-xs text-muted-foreground">
              ArgoCD project token (created in ArgoCD UI). Stored encrypted at rest.
            </p>
          </div>

          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={form.verifySsl}
              onChange={(e) => setForm({ ...form, verifySsl: e.target.checked })}
              className="h-4 w-4 rounded border-border"
            />
            <span className="text-foreground">Verify SSL certificate</span>
          </label>
    </ModalShell>
  );
}
