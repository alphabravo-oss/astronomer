'use client';

// AppProject creation dialog. Captures only the fields most users need on day
// one — name, description, sourceRepos (one per line), and destinations
// (server / namespace pairs, one per line). The backend takes the rest of
// AppProjectSpec from defaults.

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import { ModalShell } from '@/components/ui/modal-shell';
import { Loader2, FolderTree } from 'lucide-react';
import { createArgoProject } from '@/lib/api';
import { queryKeys } from '@/lib/hooks';
import { useAppForm, useStore } from '@/lib/form';
import type { ArgoProjectSyncWindow } from '@/types';

interface CreateProjectDialogProps {
  instanceId: string;
  onClose: () => void;
}

interface DestinationRow {
  server: string;
  namespace: string;
}

function parseDestinations(text: string): DestinationRow[] {
  return text
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean)
    .map((line) => {
      // Format: "<server>|<namespace>" or "<server> <namespace>"
      const parts = line.split(/\s*[|\s]\s*/);
      return {
        server: parts[0] ?? '',
        namespace: parts[1] ?? '*',
      };
    })
    .filter((d) => d.server);
}

function parseLines(text: string): string[] {
  return text
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean);
}

function buildSyncWindows(options: {
  maintenanceEnabled: boolean;
  maintenanceSchedule: string;
  maintenanceDuration: string;
  blackoutEnabled: boolean;
  blackoutSchedule: string;
  blackoutDuration: string;
  syncWindowTimeZone: string;
}): ArgoProjectSyncWindow[] | undefined {
  const windows: ArgoProjectSyncWindow[] = [];
  const timeZone = options.syncWindowTimeZone.trim() || 'UTC';
  if (options.maintenanceEnabled) {
    windows.push({
      kind: 'allow',
      schedule: options.maintenanceSchedule.trim(),
      duration: options.maintenanceDuration.trim(),
      applications: ['*'],
      clusters: ['*'],
      syncOverrun: true,
      timeZone,
      description: 'Maintenance window',
    });
  }
  if (options.blackoutEnabled) {
    windows.push({
      kind: 'deny',
      schedule: options.blackoutSchedule.trim(),
      duration: options.blackoutDuration.trim(),
      applications: ['*'],
      clusters: ['*'],
      manualSync: true,
      timeZone,
      description: 'Blackout window',
    });
  }
  return windows.length ? windows : undefined;
}

export function CreateProjectDialog({ instanceId, onClose }: CreateProjectDialogProps) {
  const queryClient = useQueryClient();

  const create = useMutation({
    mutationFn: () => {
      const value = form.state.values;
      return createArgoProject(instanceId, {
        name: value.name.trim(),
        spec: {
          description: value.description.trim() || undefined,
          sourceRepos: parseLines(value.sourceRepos),
          destinations: parseDestinations(value.destinations),
          syncWindows: buildSyncWindows({
            maintenanceEnabled: value.maintenanceEnabled,
            maintenanceSchedule: value.maintenanceSchedule,
            maintenanceDuration: value.maintenanceDuration,
            blackoutEnabled: value.blackoutEnabled,
            blackoutSchedule: value.blackoutSchedule,
            blackoutDuration: value.blackoutDuration,
            syncWindowTimeZone: value.syncWindowTimeZone,
          }),
        },
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.projects(instanceId) });
      toastSuccess(`AppProject ${form.state.values.name} created`);
      onClose();
    },
    onError: (error: Error) => {
      toastApiError('Create failed', error);
    },
  });

  const form = useAppForm({
    defaultValues: {
      name: '',
      description: '',
      sourceRepos: '*',
      destinations: '* | *',
      maintenanceEnabled: false,
      maintenanceSchedule: '0 9 * * 1-5',
      maintenanceDuration: '8h',
      blackoutEnabled: false,
      blackoutSchedule: '0 22 * * 1-5',
      blackoutDuration: '10h',
      syncWindowTimeZone: 'UTC',
    },
    onSubmit: () => create.mutate(),
  });

  // Old disabled gate (`!name.trim()`) + window-section visibility,
  // recomputed from form state.
  const nameEmpty = useStore(form.store, (s) => !s.values.name.trim());
  const maintenanceEnabled = useStore(form.store, (s) => s.values.maintenanceEnabled);
  const blackoutEnabled = useStore(form.store, (s) => s.values.blackoutEnabled);

  return (
    <ModalShell
      title="Create AppProject"
      onClose={onClose}
      size="lg"
      panelClassName="max-w-2xl max-h-[88vh] bg-popover overflow-hidden flex flex-col"
      bodyClassName="overflow-y-auto"
      footerClassName="bg-muted/30"
      titleIcon={(
        <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
          <FolderTree className="h-4 w-4 text-muted-foreground" />
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
            disabled={nameEmpty || create.isPending}
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
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Name</label>
            <form.Field name="name">
              {(field) => (
                <input
                  type="text"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="platform-team"
                  className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                    focus:outline-none focus:ring-1 focus:ring-ring"
                />
              )}
            </form.Field>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Description</label>
            <form.Field name="description">
              {(field) => (
                <input
                  type="text"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="Owner: Platform Team"
                  className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                    focus:outline-none focus:ring-1 focus:ring-ring"
                />
              )}
            </form.Field>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Source Repos (one per line)</label>
            <form.Field name="sourceRepos">
              {(field) => (
                <textarea
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  rows={3}
                  placeholder="*&#10;https://github.com/org/*"
                  className="w-full px-3 py-2 rounded-md border border-border bg-background text-sm font-mono
                    focus:outline-none focus:ring-1 focus:ring-ring"
                />
              )}
            </form.Field>
            <p className="text-xs text-muted-foreground">
              Glob patterns. <code>*</code> allows any.
            </p>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Destinations (one per line)</label>
            <form.Field name="destinations">
              {(field) => (
                <textarea
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  rows={3}
                  placeholder="* | *&#10;https://kubernetes.default.svc | production"
                  className="w-full px-3 py-2 rounded-md border border-border bg-background text-sm font-mono
                    focus:outline-none focus:ring-1 focus:ring-ring"
                />
              )}
            </form.Field>
            <p className="text-xs text-muted-foreground">
              Format: <code>&lt;server&gt; | &lt;namespace&gt;</code>. Use <code>*</code> for any.
            </p>
          </div>

          <div className="rounded-lg border border-border bg-background p-4 space-y-3">
            <div className="flex items-center justify-between gap-3">
              <label className="inline-flex items-center gap-2 text-sm font-medium text-foreground">
                <form.Field name="maintenanceEnabled">
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
                Maintenance window
              </label>
              <label className="inline-flex items-center gap-2 text-sm font-medium text-foreground">
                <form.Field name="blackoutEnabled">
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
                Blackout window
              </label>
            </div>
            {(maintenanceEnabled || blackoutEnabled) && (
              <div className="space-y-3">
                <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
                  <label className="space-y-1.5">
                    <span className="text-xs font-medium text-muted-foreground">Timezone</span>
                    <form.Field name="syncWindowTimeZone">
                      {(field) => (
                        <input
                          value={field.state.value}
                          onChange={(e) => field.handleChange(e.target.value)}
                          onBlur={field.handleBlur}
                          className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono"
                        />
                      )}
                    </form.Field>
                  </label>
                  {maintenanceEnabled && (
                    <>
                      <label className="space-y-1.5">
                        <span className="text-xs font-medium text-muted-foreground">Maintenance schedule</span>
                        <form.Field name="maintenanceSchedule">
                          {(field) => (
                            <input
                              value={field.state.value}
                              onChange={(e) => field.handleChange(e.target.value)}
                              onBlur={field.handleBlur}
                              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono"
                            />
                          )}
                        </form.Field>
                      </label>
                      <label className="space-y-1.5">
                        <span className="text-xs font-medium text-muted-foreground">Maintenance duration</span>
                        <form.Field name="maintenanceDuration">
                          {(field) => (
                            <input
                              value={field.state.value}
                              onChange={(e) => field.handleChange(e.target.value)}
                              onBlur={field.handleBlur}
                              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono"
                            />
                          )}
                        </form.Field>
                      </label>
                    </>
                  )}
                </div>
                {blackoutEnabled && (
                  <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                    <label className="space-y-1.5">
                      <span className="text-xs font-medium text-muted-foreground">Blackout schedule</span>
                      <form.Field name="blackoutSchedule">
                        {(field) => (
                          <input
                            value={field.state.value}
                            onChange={(e) => field.handleChange(e.target.value)}
                            onBlur={field.handleBlur}
                            className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono"
                          />
                        )}
                      </form.Field>
                    </label>
                    <label className="space-y-1.5">
                      <span className="text-xs font-medium text-muted-foreground">Blackout duration</span>
                      <form.Field name="blackoutDuration">
                        {(field) => (
                          <input
                            value={field.state.value}
                            onChange={(e) => field.handleChange(e.target.value)}
                            onBlur={field.handleBlur}
                            className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono"
                          />
                        )}
                      </form.Field>
                    </label>
                  </div>
                )}
              </div>
            )}
          </div>
    </ModalShell>
  );
}
