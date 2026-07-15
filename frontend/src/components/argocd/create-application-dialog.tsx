'use client';

// Create Application against an ArgoCD instance. Minimal cover-most-cases form
// — name, project, source (repo + path + revision), destination (server +
// namespace), and an "auto sync" toggle.

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import { ModalShell } from '@/components/ui/modal-shell';
import { Loader2, Rocket } from 'lucide-react';
import { createArgoApplication } from '@/lib/api';
import { queryKeys } from '@/lib/hooks';
import { useAppForm, useStore } from '@/lib/form';

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

  const create = useMutation({
    mutationFn: () => {
      const value = form.state.values;
      return createArgoApplication(instanceId, {
        name: value.name.trim(),
        spec: {
          project: value.project.trim() || 'default',
          source: {
            repoURL: value.repoURL.trim(),
            path: value.path.trim() || undefined,
            targetRevision: value.targetRevision.trim() || 'HEAD',
          },
          destination: {
            server: value.server.trim(),
            namespace: value.namespace.trim(),
          },
          syncPolicy: value.autoSync
            ? {
                automated: { prune: value.prune, selfHeal: value.selfHeal },
              }
            : undefined,
        },
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.liveApps(instanceId) });
      toastSuccess(`Application ${form.state.values.name} created`);
      onClose();
    },
    onError: (error: Error) => {
      toastApiError('Create failed', error);
    },
  });

  const form = useAppForm({
    defaultValues: {
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
    },
    onSubmit: () => create.mutate(),
  });

  // Old disabled gate, recomputed from form state 1:1.
  const canSubmit = useStore(
    form.store,
    (s) => s.values.name.trim() && s.values.repoURL.trim() && s.values.namespace.trim(),
  );
  const autoSync = useStore(form.store, (s) => s.values.autoSync);

  return (
    <ModalShell
      title="Create Application"
      onClose={onClose}
      panelClassName="max-w-xl bg-popover overflow-hidden"
      bodyClassName="max-h-[70vh] overflow-y-auto"
      footerClassName="bg-muted/30"
      titleIcon={(
        <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
          <Rocket className="h-4 w-4 text-muted-foreground" />
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
            onClick={() => void form.handleSubmit()}
            disabled={!canSubmit || create.isPending}
            className="inline-flex items-center gap-1.5 h-8 px-4 rounded text-sm font-medium
              bg-primary text-primary-foreground hover:bg-primary/90 transition-colors
              disabled:opacity-50"
          >
            {create.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Create
          </button>
        </div>
      )}
    >
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Name</label>
              <form.Field name="name">
                {(field) => (
                  <input
                    type="text"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                    placeholder="my-app"
                    className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                      focus:outline-none focus:ring-1 focus:ring-ring"
                  />
                )}
              </form.Field>
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Project</label>
              <form.Field name="project">
                {(field) => (
                  <input
                    type="text"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                    className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                      focus:outline-none focus:ring-1 focus:ring-ring"
                  />
                )}
              </form.Field>
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Repository URL</label>
            <form.Field name="repoURL">
              {(field) => (
                <input
                  type="text"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="https://github.com/org/manifests"
                  className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                    focus:outline-none focus:ring-1 focus:ring-ring"
                />
              )}
            </form.Field>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Path</label>
              <form.Field name="path">
                {(field) => (
                  <input
                    type="text"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                    className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                      focus:outline-none focus:ring-1 focus:ring-ring"
                  />
                )}
              </form.Field>
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Target Revision</label>
              <form.Field name="targetRevision">
                {(field) => (
                  <input
                    type="text"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                    className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                      focus:outline-none focus:ring-1 focus:ring-ring"
                  />
                )}
              </form.Field>
            </div>
          </div>

          <div className="grid grid-cols-3 gap-3">
            <div className="col-span-2 space-y-1.5">
              <label className="text-sm font-medium text-foreground">Destination Server</label>
              <form.Field name="server">
                {(field) => (
                  <input
                    type="text"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                    className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                      focus:outline-none focus:ring-1 focus:ring-ring"
                  />
                )}
              </form.Field>
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Namespace</label>
              <form.Field name="namespace">
                {(field) => (
                  <input
                    type="text"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                    className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                      focus:outline-none focus:ring-1 focus:ring-ring"
                  />
                )}
              </form.Field>
            </div>
          </div>

          <div className="space-y-2 pt-2 border-t border-border">
            <label className="flex items-center gap-2 text-sm">
              <form.Field name="autoSync">
                {(field) => (
                  <input
                    type="checkbox"
                    checked={field.state.value}
                    onChange={(e) => field.handleChange(e.target.checked)}
                    onBlur={field.handleBlur}
                    className="h-4 w-4 rounded border-border"
                  />
                )}
              </form.Field>
              <span className="text-foreground">Enable automated sync</span>
            </label>
            {autoSync && (
              <div className="ml-6 space-y-2">
                <label className="flex items-center gap-2 text-sm">
                  <form.Field name="prune">
                    {(field) => (
                      <input
                        type="checkbox"
                        checked={field.state.value}
                        onChange={(e) => field.handleChange(e.target.checked)}
                        onBlur={field.handleBlur}
                        className="h-4 w-4 rounded border-border"
                      />
                    )}
                  </form.Field>
                  <span className="text-muted-foreground">Prune resources removed from source</span>
                </label>
                <label className="flex items-center gap-2 text-sm">
                  <form.Field name="selfHeal">
                    {(field) => (
                      <input
                        type="checkbox"
                        checked={field.state.value}
                        onChange={(e) => field.handleChange(e.target.checked)}
                        onBlur={field.handleBlur}
                        className="h-4 w-4 rounded border-border"
                      />
                    )}
                  </form.Field>
                  <span className="text-muted-foreground">Self-heal drift</span>
                </label>
              </div>
            )}
          </div>
    </ModalShell>
  );
}
