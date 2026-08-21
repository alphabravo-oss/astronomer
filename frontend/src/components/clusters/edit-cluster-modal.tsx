'use client';

import { useState } from 'react';
import { ModalShell } from '@/components/ui/modal-shell';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { useUpdateCluster } from '@/lib/hooks';
import type { Cluster, ClusterEnvironment } from '@/types';
import { Pencil, AlertTriangle } from 'lucide-react';

interface EditClusterModalProps {
  cluster: Cluster;
  onClose: () => void;
}

export function EditClusterModal({ cluster, onClose }: EditClusterModalProps) {
  const updateCluster = useUpdateCluster();
  const [form, setForm] = useState({
    displayName: cluster.displayName,
    environment: cluster.environment as ClusterEnvironment,
    description: cluster.description || '',
    directAccessEnabled: !!cluster.directAccessEnabled,
  });

  const handleSubmit = async () => {
    try {
      await updateCluster.mutateAsync({
        id: cluster.id,
        data: {
          displayName: form.displayName,
          environment: form.environment,
          description: form.description || undefined,
          directAccessEnabled: form.directAccessEnabled,
        },
      });
      onClose();
    } catch {
      // Error handled by mutation
    }
  };

  return (
    <ModalShell
      title="Edit Cluster"
      onClose={onClose}
      panelClassName="max-w-lg bg-popover overflow-hidden"
      titleIcon={(
        <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
          <Pencil className="h-4 w-4 text-muted-foreground" />
        </div>
      )}
      footerClassName="flex items-center justify-end gap-2 bg-muted/30"
      footer={(
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={handleSubmit}
            disabled={!form.displayName || updateCluster.isPending}
            loading={updateCluster.isPending}
          >
            Save Changes
          </ActionButton>
        </>
      )}
    >
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Cluster Name</label>
            <Input
              type="text"
              value={cluster.name}
              disabled
              className="bg-muted/50 text-muted-foreground"
            />
            <p className="text-xs text-muted-foreground">Cluster name cannot be changed after creation.</p>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Display Name</label>
            <Input
              type="text"
              value={form.displayName}
              onChange={(e) => setForm((f) => ({ ...f, displayName: e.target.value }))}
              placeholder="My Production Cluster"
              autoFocus
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Environment</label>
            <Select
              value={form.environment}
              onChange={(e) => setForm((f) => ({ ...f, environment: e.target.value as ClusterEnvironment }))}
            >
              <option value="production">Production</option>
              <option value="staging">Staging</option>
              <option value="development">Development</option>
              <option value="testing">Testing</option>
            </Select>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">
              Description <span className="text-muted-foreground font-normal">(optional)</span>
            </label>
            <Textarea
              value={form.description}
              onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
              placeholder="Brief description..."
              rows={2}
              className="min-h-0 resize-none"
            />
          </div>

          <label className="flex items-start gap-2 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={form.directAccessEnabled}
              onChange={(e) => setForm((f) => ({ ...f, directAccessEnabled: e.target.checked }))}
              className="mt-0.5 h-4 w-4 rounded border-border text-primary focus:ring-ring"
            />
            <span className="text-xs flex-1">
              <span className="flex items-center gap-1.5 font-medium text-foreground">
                <AlertTriangle className="h-3.5 w-3.5 text-status-warning" />
                Allow direct cluster access (break-glass)
              </span>
              <span className="block text-muted-foreground mt-0.5 leading-snug">
                Kubeconfig downloads include a {cluster.name}-direct context that hits the
                cluster API directly. Not audited; revocation requires rotating the
                ServiceAccount on the cluster.
              </span>
            </span>
          </label>
    </ModalShell>
  );
}
