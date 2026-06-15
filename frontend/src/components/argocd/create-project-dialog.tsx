'use client';

// AppProject creation dialog. Captures only the fields most users need on day
// one — name, description, sourceRepos (one per line), and destinations
// (server / namespace pairs, one per line). The backend takes the rest of
// AppProjectSpec from defaults.

import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import { ModalShell } from '@/components/ui/modal-shell';
import { Loader2, FolderTree } from 'lucide-react';
import { createArgoProject } from '@/lib/api';
import { queryKeys } from '@/lib/hooks';
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
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [sourceRepos, setSourceRepos] = useState('*');
  const [destinations, setDestinations] = useState('* | *');
  const [maintenanceEnabled, setMaintenanceEnabled] = useState(false);
  const [maintenanceSchedule, setMaintenanceSchedule] = useState('0 9 * * 1-5');
  const [maintenanceDuration, setMaintenanceDuration] = useState('8h');
  const [blackoutEnabled, setBlackoutEnabled] = useState(false);
  const [blackoutSchedule, setBlackoutSchedule] = useState('0 22 * * 1-5');
  const [blackoutDuration, setBlackoutDuration] = useState('10h');
  const [syncWindowTimeZone, setSyncWindowTimeZone] = useState('UTC');

  const create = useMutation({
    mutationFn: () =>
      createArgoProject(instanceId, {
        name: name.trim(),
        spec: {
          description: description.trim() || undefined,
          sourceRepos: parseLines(sourceRepos),
          destinations: parseDestinations(destinations),
          syncWindows: buildSyncWindows({
            maintenanceEnabled,
            maintenanceSchedule,
            maintenanceDuration,
            blackoutEnabled,
            blackoutSchedule,
            blackoutDuration,
            syncWindowTimeZone,
          }),
        },
    }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.projects(instanceId) });
      toastSuccess(`AppProject ${name} created`);
      onClose();
    },
    onError: (error: Error) => {
      toastApiError('Create failed', error);
    },
  });

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
            onClick={() => create.mutate()}
            disabled={!name.trim() || create.isPending}
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
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="platform-team"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Description</label>
            <input
              type="text"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Owner: Platform Team"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Source Repos (one per line)</label>
            <textarea
              value={sourceRepos}
              onChange={(e) => setSourceRepos(e.target.value)}
              rows={3}
              placeholder="*&#10;https://github.com/org/*"
              className="w-full px-3 py-2 rounded-md border border-border bg-background text-sm font-mono
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
            <p className="text-xs text-muted-foreground">
              Glob patterns. <code>*</code> allows any.
            </p>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Destinations (one per line)</label>
            <textarea
              value={destinations}
              onChange={(e) => setDestinations(e.target.value)}
              rows={3}
              placeholder="* | *&#10;https://kubernetes.default.svc | production"
              className="w-full px-3 py-2 rounded-md border border-border bg-background text-sm font-mono
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
            <p className="text-xs text-muted-foreground">
              Format: <code>&lt;server&gt; | &lt;namespace&gt;</code>. Use <code>*</code> for any.
            </p>
          </div>

          <div className="rounded-lg border border-border bg-background p-4 space-y-3">
            <div className="flex items-center justify-between gap-3">
              <label className="inline-flex items-center gap-2 text-sm font-medium text-foreground">
                <input
                  type="checkbox"
                  checked={maintenanceEnabled}
                  onChange={(e) => setMaintenanceEnabled(e.target.checked)}
                  className="h-4 w-4 rounded border-border"
                />
                Maintenance window
              </label>
              <label className="inline-flex items-center gap-2 text-sm font-medium text-foreground">
                <input
                  type="checkbox"
                  checked={blackoutEnabled}
                  onChange={(e) => setBlackoutEnabled(e.target.checked)}
                  className="h-4 w-4 rounded border-border"
                />
                Blackout window
              </label>
            </div>
            {(maintenanceEnabled || blackoutEnabled) && (
              <div className="space-y-3">
                <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
                  <label className="space-y-1.5">
                    <span className="text-xs font-medium text-muted-foreground">Timezone</span>
                    <input
                      value={syncWindowTimeZone}
                      onChange={(e) => setSyncWindowTimeZone(e.target.value)}
                      className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono"
                    />
                  </label>
                  {maintenanceEnabled && (
                    <>
                      <label className="space-y-1.5">
                        <span className="text-xs font-medium text-muted-foreground">Maintenance schedule</span>
                        <input
                          value={maintenanceSchedule}
                          onChange={(e) => setMaintenanceSchedule(e.target.value)}
                          className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono"
                        />
                      </label>
                      <label className="space-y-1.5">
                        <span className="text-xs font-medium text-muted-foreground">Maintenance duration</span>
                        <input
                          value={maintenanceDuration}
                          onChange={(e) => setMaintenanceDuration(e.target.value)}
                          className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono"
                        />
                      </label>
                    </>
                  )}
                </div>
                {blackoutEnabled && (
                  <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                    <label className="space-y-1.5">
                      <span className="text-xs font-medium text-muted-foreground">Blackout schedule</span>
                      <input
                        value={blackoutSchedule}
                        onChange={(e) => setBlackoutSchedule(e.target.value)}
                        className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono"
                      />
                    </label>
                    <label className="space-y-1.5">
                      <span className="text-xs font-medium text-muted-foreground">Blackout duration</span>
                      <input
                        value={blackoutDuration}
                        onChange={(e) => setBlackoutDuration(e.target.value)}
                        className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono"
                      />
                    </label>
                  </div>
                )}
              </div>
            )}
          </div>
    </ModalShell>
  );
}
