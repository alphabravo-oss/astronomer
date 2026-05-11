'use client';

// Create Application against an ArgoCD instance. Minimal cover-most-cases form
// — name, project, source (repo + path + revision), destination (server +
// namespace), and an "auto sync" toggle.

import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { X, Loader2, Rocket } from 'lucide-react';
import { createArgoApplication } from '@/lib/api';

interface CreateApplicationDialogProps {
  instanceId: string;
  defaultProject?: string;
  onClose: () => void;
}

export function CreateApplicationDialog({
  instanceId,
  defaultProject = 'default',
  onClose,
}: CreateApplicationDialogProps) {
  const queryClient = useQueryClient();
  const [form, setForm] = useState({
    name: '',
    project: defaultProject,
    repoURL: '',
    path: '.',
    targetRevision: 'HEAD',
    server: 'https://kubernetes.default.svc',
    namespace: 'default',
    autoSync: false,
    prune: false,
    selfHeal: false,
  });

  const create = useMutation({
    mutationFn: () =>
      createArgoApplication(instanceId, {
        name: form.name.trim(),
        spec: {
          project: form.project.trim() || 'default',
          source: {
            repoURL: form.repoURL.trim(),
            path: form.path.trim() || undefined,
            targetRevision: form.targetRevision.trim() || 'HEAD',
          },
          destination: {
            server: form.server.trim(),
            namespace: form.namespace.trim(),
          },
          syncPolicy: form.autoSync
            ? {
                automated: { prune: form.prune, selfHeal: form.selfHeal },
              }
            : undefined,
        },
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['argocd', 'live-apps', instanceId] });
      toast.success(`Application ${form.name} created`);
      onClose();
    },
    onError: (error: Error) => {
      toast.error(`Create failed: ${error.message}`);
    },
  });

  const canSubmit = form.name.trim() && form.repoURL.trim() && form.namespace.trim();

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-xl rounded-xl border border-border bg-popover shadow-2xl overflow-hidden">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border">
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
              <Rocket className="h-4 w-4 text-muted-foreground" />
            </div>
            <h3 className="text-lg font-semibold text-foreground">Create Application</h3>
          </div>
          <button
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground transition-colors"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="p-6 space-y-4 max-h-[70vh] overflow-y-auto">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Name</label>
              <input
                type="text"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="my-app"
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                  focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Project</label>
              <input
                type="text"
                value={form.project}
                onChange={(e) => setForm({ ...form, project: e.target.value })}
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                  focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Repository URL</label>
            <input
              type="text"
              value={form.repoURL}
              onChange={(e) => setForm({ ...form, repoURL: e.target.value })}
              placeholder="https://github.com/org/manifests"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Path</label>
              <input
                type="text"
                value={form.path}
                onChange={(e) => setForm({ ...form, path: e.target.value })}
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                  focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Target Revision</label>
              <input
                type="text"
                value={form.targetRevision}
                onChange={(e) => setForm({ ...form, targetRevision: e.target.value })}
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                  focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
          </div>

          <div className="grid grid-cols-3 gap-3">
            <div className="col-span-2 space-y-1.5">
              <label className="text-sm font-medium text-foreground">Destination Server</label>
              <input
                type="text"
                value={form.server}
                onChange={(e) => setForm({ ...form, server: e.target.value })}
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                  focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Namespace</label>
              <input
                type="text"
                value={form.namespace}
                onChange={(e) => setForm({ ...form, namespace: e.target.value })}
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                  focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
          </div>

          <div className="space-y-2 pt-2 border-t border-border">
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={form.autoSync}
                onChange={(e) => setForm({ ...form, autoSync: e.target.checked })}
                className="h-4 w-4 rounded border-border"
              />
              <span className="text-foreground">Enable automated sync</span>
            </label>
            {form.autoSync && (
              <div className="ml-6 space-y-2">
                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={form.prune}
                    onChange={(e) => setForm({ ...form, prune: e.target.checked })}
                    className="h-4 w-4 rounded border-border"
                  />
                  <span className="text-muted-foreground">Prune resources removed from source</span>
                </label>
                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={form.selfHeal}
                    onChange={(e) => setForm({ ...form, selfHeal: e.target.checked })}
                    className="h-4 w-4 rounded border-border"
                  />
                  <span className="text-muted-foreground">Self-heal drift</span>
                </label>
              </div>
            )}
          </div>
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border bg-muted/30">
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
              disabled:opacity-50"
          >
            {create.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Create
          </button>
        </div>
      </div>
    </div>
  );
}
